package sandbox

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"code-agent/internal/safety"
)

const (
	maxInputFiles      = 20_000
	maxInputFileBytes  = 8 << 20
	maxInputBytes      = 256 << 20
	maxInputPathBytes  = 8 << 20
	inputModeTransform = "readonly-v1"
)

// InputFile retains the original source state, before the read-only test transform.
type InputFile struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

type FrozenInput struct {
	Path          string      `json:"path"`
	ArchiveSHA256 string      `json:"archive_sha256"`
	SourceSHA256  string      `json:"source_sha256"`
	Files         []InputFile `json:"files"`
	ModeTransform string      `json:"mode_transform"`
}

// FreezeInput creates a closed archive from pinned files; new foreign paths cannot join the test input.
func FreezeInput(ctx context.Context, sourceRoot, storageRoot, operationID string, files []InputFile) (*FrozenInput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !openSandboxHex(operationID, 64) {
		return nil, errors.New("invalid frozen input operation")
	}
	files, headers, sourceSHA, err := inputManifest(files)
	if err != nil {
		return nil, err
	}
	sourceRoot, err = filepath.Abs(sourceRoot)
	if err != nil {
		return nil, err
	}
	storageRoot, err = filepath.Abs(storageRoot)
	if err != nil {
		return nil, err
	}
	paths := []string{sourceRoot, storageRoot}
	releaseDirectories, err := safety.PinDirectories(paths...)
	if err != nil {
		return nil, err
	}
	defer releaseDirectories()
	paths = nil
	for _, file := range files {
		if file.Exists {
			paths = append(paths, filepath.Join(sourceRoot, filepath.FromSlash(file.Path)))
		}
	}
	releaseFiles, err := safety.PinReadOnlyFiles(paths...)
	if err != nil {
		return nil, err
	}
	defer releaseFiles()
	source, err := os.OpenRoot(sourceRoot)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	for _, file := range files {
		if !file.Exists {
			if _, err := source.Lstat(filepath.FromSlash(file.Path)); !errors.Is(err, os.ErrNotExist) {
				return nil, errors.New("absent frozen input path changed")
			}
		}
	}
	storage, err := os.OpenRoot(storageRoot)
	if err != nil {
		return nil, err
	}
	defer storage.Close()
	directory := "input-" + operationID
	if err := storage.Mkdir(directory, 0700); err != nil {
		return nil, errors.New("frozen input artifact already exists or storage is unavailable")
	}
	if err := safety.ProtectPrivatePath(filepath.Join(storageRoot, directory)); err != nil {
		return nil, err
	}
	archivePath := filepath.Join(storageRoot, directory, "source.tar")
	archive, err := storage.OpenFile(directory+"/source.tar", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	digest := sha256.New()
	writer := tar.NewWriter(io.MultiWriter(archive, digest))
	items := make(map[string]InputFile, len(files))
	for _, item := range files {
		items[item.Path] = item
	}
	var writeErr error
	for _, header := range headers {
		if writeErr = ctx.Err(); writeErr != nil {
			break
		}
		if writeErr = writer.WriteHeader(header); writeErr != nil {
			break
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		file, openErr := source.Open(filepath.FromSlash(header.Name))
		if openErr != nil {
			writeErr = openErr
			break
		}
		info, statErr := file.Stat()
		item := items[header.Name]
		if statErr != nil || !info.Mode().IsRegular() || info.Size() != item.Size || uint32(info.Mode().Perm()) != item.Mode {
			file.Close()
			writeErr = errors.New("frozen input metadata changed")
			break
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(writer, hash), io.LimitReader(file, item.Size+1))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || written != item.Size || hex.EncodeToString(hash.Sum(nil)) != item.SHA256 {
			writeErr = errors.New("frozen input content changed")
			break
		}
	}
	writeErr = errors.Join(writeErr, writer.Close(), archive.Sync(), archive.Close())
	if writeErr != nil {
		return nil, writeErr
	}
	if err := safety.ProtectPrivatePath(archivePath); err != nil {
		return nil, err
	}
	return &FrozenInput{Path: archivePath, ArchiveSHA256: hex.EncodeToString(digest.Sum(nil)), SourceSHA256: sourceSHA, Files: files, ModeTransform: inputModeTransform}, nil
}

func inputManifest(input []InputFile) ([]InputFile, []*tar.Header, string, error) {
	if len(input) == 0 || len(input) > maxInputFiles {
		return nil, nil, "", errors.New("frozen input file count exceeds its contract")
	}
	files := slices.Clone(input)
	slices.SortFunc(files, func(left, right InputFile) int { return strings.Compare(left.Path, right.Path) })
	names, directories := map[string]string{}, map[string]string{}
	var total, pathBytes, directoryPathBytes int64
	for _, file := range files {
		if !safeInputPath(file.Path) || file.Mode > 0777 || file.Size < 0 || file.Size > maxInputFileBytes || file.Exists && !openSandboxHex(file.SHA256, 64) || !file.Exists && (file.SHA256 != "" || file.Size != 0 || file.Mode != 0) {
			return nil, nil, "", errors.New("frozen input manifest is invalid")
		}
		key := strings.ToLower(file.Path)
		if _, duplicate := names[key]; duplicate {
			return nil, nil, "", errors.New("frozen input contains duplicate or aliased paths")
		}
		names[key] = file.Path
		total += file.Size
		pathBytes += int64(len(file.Path))
		if total > maxInputBytes || pathBytes > maxInputPathBytes {
			return nil, nil, "", errors.New("frozen input exceeds its byte bound")
		}
	}
	for _, file := range files {
		if !file.Exists {
			continue
		}
		for parent := path.Dir(file.Path); parent != "."; parent = path.Dir(parent) {
			if _, collision := names[strings.ToLower(parent)]; collision {
				return nil, nil, "", errors.New("frozen input file and directory collide")
			}
			key := strings.ToLower(parent)
			if previous, exists := directories[key]; exists && previous != parent {
				return nil, nil, "", errors.New("frozen input directories are aliased")
			}
			if _, exists := directories[key]; !exists {
				directoryPathBytes += int64(len(parent))
			}
			directories[key] = parent
			if len(directories) > maxInputFiles || directoryPathBytes > maxInputPathBytes {
				return nil, nil, "", errors.New("frozen input directory count exceeds its contract")
			}
		}
	}
	paths := make([]string, 0, len(directories))
	for _, directory := range directories {
		paths = append(paths, directory)
	}
	slices.Sort(paths)
	headers := make([]*tar.Header, 0, len(paths)+len(files))
	for _, directory := range paths {
		headers = append(headers, &tar.Header{Name: directory + "/", Typeflag: tar.TypeDir, Mode: 0700, Format: tar.FormatPAX})
	}
	for _, file := range files {
		if !file.Exists {
			continue
		}
		mode := int64(0444)
		if file.Mode&0111 != 0 {
			mode = 0555
		}
		headers = append(headers, &tar.Header{Name: file.Path, Typeflag: tar.TypeReg, Size: file.Size, Mode: mode, Format: tar.FormatPAX})
	}
	data, err := json.Marshal(files)
	if err != nil {
		return nil, nil, "", err
	}
	digest := sha256.Sum256(data)
	return files, headers, hex.EncodeToString(digest[:]), nil
}

func safeInputPath(name string) bool {
	if name == "" || name == "." || path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\:*?\"<>|") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." || strings.TrimRight(part, ". ") != part {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		serial := strings.TrimPrefix(strings.TrimPrefix(base, "COM"), "LPT")
		serialDevice := (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && len([]rune(serial)) == 1 && strings.Contains("0123456789¹²³", serial)
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CONIN$" || base == "CONOUT$" || base == "CLOCK$" || serialDevice {
			return false
		}
	}
	return true
}

// pinFrozenInput verifies both the archive bytes and the canonical source manifest through cleanup.
func pinFrozenInput(ctx context.Context, storage string, request Request) (func(), error) {
	input := request.FrozenInput
	goTest := request.Program == "go" && slices.Equal(request.Args, []string{"test", "./...", "-count=1"}) && request.DependencyOperationID == ""
	pythonTest := request.Program == "python" && slices.Equal(request.Args, []string{"-c", PythonUnittestScript}) && openSandboxHex(request.DependencyOperationID, 64)
	if input == nil || input.ModeTransform != inputModeTransform || !openSandboxHex(input.ArchiveSHA256, 64) || !openSandboxHex(input.SourceSHA256, 64) || !openSandboxHex(request.OperationID, 64) || input.Path != request.Workspace || input.Path != filepath.Join(storage, "input-"+request.OperationID, "source.tar") || !goTest && !pythonTest || request.WorkingDir != "" || request.Command != "" || request.Interactive {
		return nil, errors.New("frozen input request is outside its test contract")
	}
	files, headers, sourceSHA, err := inputManifest(input.Files)
	if err != nil || sourceSHA != input.SourceSHA256 || !slices.Equal(files, input.Files) {
		return nil, errors.New("frozen input source identity differs")
	}
	release, err := safety.PinReadOnlyFiles(input.Path)
	if err != nil {
		return nil, err
	}
	if err := verifyFrozenArchive(ctx, input, headers); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func verifyFrozenArchive(ctx context.Context, input *FrozenInput, headers []*tar.Header) error {
	archive, err := os.Open(input.Path)
	if err != nil {
		return err
	}
	defer archive.Close()
	info, err := archive.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxInputBytes+64<<20 {
		return errors.New("frozen input archive exceeds its contract")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, archive); err != nil || hex.EncodeToString(digest.Sum(nil)) != input.ArchiveSHA256 {
		return errors.New("frozen input archive checksum differs")
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := tar.NewReader(archive)
	canonical := sha256.New()
	writer := tar.NewWriter(canonical)
	items := make(map[string]InputFile, len(input.Files))
	for _, item := range input.Files {
		items[item.Path] = item
	}
	for _, expected := range headers {
		if err := ctx.Err(); err != nil {
			return err
		}
		actual, err := reader.Next()
		if err != nil || actual.Name != expected.Name || actual.Typeflag != expected.Typeflag || actual.Size != expected.Size || actual.Mode != expected.Mode || actual.Uid != 0 || actual.Gid != 0 || actual.Linkname != "" || actual.Uname != "" || actual.Gname != "" {
			return errors.New("frozen input archive manifest differs")
		}
		if err := writer.WriteHeader(expected); err != nil {
			return err
		}
		if expected.Typeflag == tar.TypeDir {
			continue
		}
		item := items[expected.Name]
		hash := sha256.New()
		size, err := io.Copy(io.MultiWriter(writer, hash), reader)
		if err != nil || size != item.Size || hex.EncodeToString(hash.Sum(nil)) != item.SHA256 {
			return errors.New("frozen input archive source content differs")
		}
	}
	if _, err := reader.Next(); err != io.EOF {
		return errors.New("frozen input archive contains undeclared entries")
	}
	if err := writer.Close(); err != nil || hex.EncodeToString(canonical.Sum(nil)) != input.ArchiveSHA256 {
		return errors.New("frozen input archive is not canonical")
	}
	return nil
}
