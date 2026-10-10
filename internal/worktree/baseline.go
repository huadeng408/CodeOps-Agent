package worktree

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"code-agent/internal/safety"
)

const (
	maxBaselineFileBytes = 8 << 20
	maxBaselineBytes     = 256 << 20
	maxBaselineFiles     = 20_000
)

type BaselineFile struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

type BaselineExclusion struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Baseline contains metadata only; credentials and file contents never enter
// its transport or Ledger representation. Missing and empty files differ.
type Baseline struct {
	RepositoryID string              `json:"repository_id"`
	HeadCommit   string              `json:"head_commit"`
	IndexExists  bool                `json:"index_exists"`
	IndexSHA256  string              `json:"index_sha256,omitempty"`
	Files        []BaselineFile      `json:"files"`
	Excluded     []BaselineExclusion `json:"excluded"`
	Checksum     string              `json:"checksum"`
}

// CaptureBaseline reads only the manager's host-approved repository. Product
// callers must authorize that repository before constructing its manager.
func (m *Manager) CaptureBaseline(ctx context.Context) (Baseline, error) {
	if m == nil {
		return Baseline{}, errors.New("worktree manager is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Baseline{}, err
	}
	volume := filepath.VolumeName(m.root)
	if strings.HasPrefix(m.root, `\\`) || strings.HasPrefix(m.root, "//") || strings.Contains(m.root[len(volume):], ":") || volume != "" && !filepath.IsAbs(m.root) || !utf8.ValidString(m.root) || strings.IndexFunc(m.root, unicode.IsControl) >= 0 {
		return Baseline{}, errors.New("unsafe repository root: UNC, drive-relative and stream paths are not allowed")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	abs, err := filepath.Abs(m.root)
	if err != nil {
		return Baseline{}, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || !sameBaselinePath(abs, resolved) {
		return Baseline{}, errors.New("repository root must not contain symlink or reparse aliases")
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return Baseline{}, err
	}
	defer root.Close()
	initial, err := root.Stat(".")
	if err != nil {
		return Baseline{}, err
	}
	metadata, err := openBaselineMetadata(root)
	if err != nil {
		return Baseline{}, err
	}
	defer metadata.Close()
	if err := validateBaselineMetadata(ctx, metadata); err != nil {
		return Baseline{}, err
	}
	if runtime.GOOS == "windows" {
		release, err := pinTaskMetadata(ctx, metadata, filepath.Join(abs, ".git"))
		if err != nil {
			return Baseline{}, err
		}
		defer release()
		if err := baselineMetadataUnchanged(ctx, root, metadata); err != nil {
			return Baseline{}, err
		}
		if err := baselineRootUnchanged(abs, initial); err != nil {
			return Baseline{}, err
		}
	}
	top, err := baselineGitOutput(ctx, abs, "rev-parse", "--show-toplevel")
	if err != nil || !sameBaselinePath(abs, strings.TrimSpace(top)) {
		return Baseline{}, errors.New("baseline requires the repository root")
	}
	identityPath := abs
	if runtime.GOOS == "windows" {
		identityPath = strings.ToLower(identityPath)
	}
	before, err := captureBaseline(ctx, root, metadata, abs, initial, baselineDigest([]byte(identityPath)))
	if err != nil {
		return Baseline{}, err
	}
	after, err := captureBaseline(ctx, root, metadata, abs, initial, before.RepositoryID)
	if err != nil {
		return Baseline{}, err
	}
	if before.Checksum != after.Checksum {
		return Baseline{}, errors.New("working copy changed during baseline capture")
	}
	return after, nil
}

func captureBaseline(ctx context.Context, root, metadata *os.Root, abs string, initial fs.FileInfo, repositoryID string) (Baseline, error) {
	if err := baselineRootUnchanged(abs, initial); err != nil {
		return Baseline{}, err
	}
	if err := baselineMetadataUnchanged(ctx, root, metadata); err != nil {
		return Baseline{}, err
	}
	head, err := baselineGitOutput(ctx, abs, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return Baseline{}, errors.New("baseline requires a committed HEAD")
	}
	head = strings.TrimSpace(head)
	if len(head) != 40 && len(head) != 64 || strings.Trim(head, "0123456789abcdef") != "" {
		return Baseline{}, errors.New("invalid baseline commit")
	}
	index, indexExists, err := readBaselineFile(metadata, "index", 16<<20)
	if err != nil {
		return Baseline{}, errors.New("repository index is unavailable")
	}
	result := Baseline{RepositoryID: repositoryID, HeadCommit: head, IndexExists: indexExists,
		Files: []BaselineFile{}, Excluded: []BaselineExclusion{}}
	if indexExists {
		result.IndexSHA256 = baselineDigest(index)
	}
	paths := map[string]bool{}
	excluded := map[string]BaselineExclusion{}
	for _, args := range [][]string{{"ls-tree", "-r", "-z", "--name-only", head}, {"ls-files", "-z", "--cached", "--others", "--exclude-standard"}} {
		output, err := baselineGitOutput(ctx, abs, args...)
		if err != nil {
			return Baseline{}, errors.New("baseline file inventory is unavailable")
		}
		for output != "" {
			name, rest, complete := strings.Cut(output, "\x00")
			if !complete {
				return Baseline{}, errors.New("incomplete baseline inventory")
			}
			output = rest
			if name == "" {
				continue
			}
			if err := validateBaselinePath(name); err != nil {
				return Baseline{}, err
			}
			if prefix, reason := baselineExclusion(name); reason != "" {
				excluded[strings.ToLower(prefix)] = BaselineExclusion{Path: prefix, Reason: reason}
				if len(excluded) > maxBaselineFiles {
					return Baseline{}, errors.New("baseline exclusion metadata limit exceeded")
				}
				continue
			}
			paths[name] = true
			if len(paths) > maxBaselineFiles {
				return Baseline{}, errors.New("baseline file limit exceeded")
			}
		}
	}
	for _, exclusion := range excluded {
		result.Excluded = append(result.Excluded, exclusion)
	}
	sort.Slice(result.Excluded, func(i, j int) bool { return result.Excluded[i].Path < result.Excluded[j].Path })
	names := make([]string, 0, len(paths))
	for name := range paths {
		names = append(names, name)
	}
	sort.Strings(names)
	aliases := map[string]bool{}
	var total int64
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return Baseline{}, err
		}
		if err := validateBaselinePath(name); err != nil {
			return Baseline{}, err
		}
		key := strings.ToLower(name)
		if aliases[key] {
			return Baseline{}, errors.New("baseline contains case-colliding paths")
		}
		aliases[key] = true
		data, exists, err := readBaselineFile(root, name, maxBaselineFileBytes)
		if err != nil {
			return Baseline{}, fmt.Errorf("baseline file %s: %w", name, err)
		}
		file := BaselineFile{Path: name, Exists: exists}
		if exists {
			info, err := baselineFileInfo(root, name)
			if err != nil {
				return Baseline{}, err
			}
			file.SHA256, file.Size, file.Mode = baselineDigest(data), int64(len(data)), uint32(info.Mode().Perm())
			total += file.Size
			if total > maxBaselineBytes {
				return Baseline{}, errors.New("baseline byte limit exceeded")
			}
		}
		result.Files = append(result.Files, file)
	}
	if err := baselineRootUnchanged(abs, initial); err != nil {
		return Baseline{}, err
	}
	if err := baselineMetadataUnchanged(ctx, root, metadata); err != nil {
		return Baseline{}, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return Baseline{}, err
	}
	result.Checksum = baselineDigest(encoded)
	return result, nil
}

func openBaselineMetadata(root *os.Root) (*os.Root, error) {
	info, err := baselineFileInfo(root, ".git")
	// shortcut: linked metadata needs an explicit Harness-owned relation, add
	// that authorization when integrating managed task leases.
	if err != nil || !info.IsDir() {
		return nil, errors.New("Git metadata requires an approved in-repository directory")
	}
	metadata, err := root.OpenRoot(".git")
	if err != nil {
		return nil, errors.New("Git metadata root is unavailable")
	}
	for _, pointer := range []string{"commondir", "objects/info/alternates"} {
		_, err := baselineFileInfo(metadata, pointer)
		if !errors.Is(err, fs.ErrNotExist) {
			metadata.Close()
			return nil, errors.New("Git metadata uses an unapproved common or alternate directory")
		}
	}
	return metadata, nil
}

func baselineMetadataUnchanged(ctx context.Context, root, opened *os.Root) error {
	current, err := openBaselineMetadata(root)
	if err != nil {
		return err
	}
	defer current.Close()
	before, err := opened.Stat(".")
	if err != nil {
		return err
	}
	after, err := current.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		return errors.New("Git metadata root was replaced")
	}
	return validateBaselineMetadata(ctx, current)
}

func validateBaselineMetadata(ctx context.Context, metadata *os.Root) error {
	directories := []string{"."}
	entries, pathBytes := 0, 0
	for len(directories) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := directories[len(directories)-1]
		directories = directories[:len(directories)-1]
		directory, err := metadata.Open(name)
		if err != nil {
			return errors.New("Git metadata directory is unavailable")
		}
		for {
			batch, readErr := directory.ReadDir(128)
			for _, entry := range batch {
				if err := ctx.Err(); err != nil {
					directory.Close()
					return err
				}
				path := filepath.ToSlash(filepath.Join(name, entry.Name()))
				entries++
				pathBytes += len(path)
				if entries > 100_000 || pathBytes > 8<<20 {
					directory.Close()
					return errors.New("Git metadata inspection limit exceeded")
				}
				info, err := baselineFileInfo(metadata, path)
				if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
					directory.Close()
					return errors.New("Git metadata alias or special file is not allowed")
				}
				if info.IsDir() {
					directories = append(directories, path)
					continue
				}
				if strings.EqualFold(path, "config") || strings.EqualFold(path, "config.worktree") {
					data, _, err := readBaselineFile(metadata, path, 1<<20)
					if err != nil {
						directory.Close()
						return errors.New("Git metadata configuration is unavailable")
					}
					section := ""
					for _, line := range strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\n") {
						line = strings.TrimSpace(line)
						if !strings.HasPrefix(line, "[") {
							key, _, _ := strings.Cut(line, "=")
							key = strings.TrimSpace(key)
							if strings.EqualFold(section, "extensions") && strings.EqualFold(key, "partialClone") || strings.EqualFold(section, "remote") && strings.EqualFold(key, "promisor") {
								directory.Close()
								return errors.New("partial clone metadata requires an approved dependency preparation stage")
							}
							continue
						}
						section = strings.TrimSpace(strings.TrimPrefix(line, "["))
						end := strings.IndexAny(section, ".\" ]\t")
						if end >= 0 {
							section = section[:end]
						}
						if strings.EqualFold(section, "include") || strings.EqualFold(section, "includeIf") {
							directory.Close()
							return errors.New("Git metadata configuration includes require explicit approval")
						}
					}
				}
			}
			if readErr != nil {
				directory.Close()
				if !errors.Is(readErr, io.EOF) {
					return errors.New("Git metadata directory read failed")
				}
				break
			}
		}
	}
	return nil
}

func readBaselineFile(root *os.Root, name string, limit int64) ([]byte, bool, error) {
	before, err := baselineFileInfo(root, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, false, errors.New("baseline requires a bounded regular file")
	}
	file, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, false, errors.New("baseline file was replaced")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, false, err
	}
	after, err := baselineFileInfo(root, name)
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || int64(len(data)) != before.Size() || int64(len(data)) > limit {
		return nil, false, errors.New("baseline file changed during read")
	}
	return data, true, nil
}

func baselineFileInfo(root *os.Root, name string) (fs.FileInfo, error) {
	var info fs.FileInfo
	var err error
	parts := strings.Split(name, "/")
	for i := range parts {
		info, err = root.Lstat(filepath.FromSlash(strings.Join(parts[:i+1], "/")))
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("baseline symlink or reparse path is not allowed")
		}
		if i < len(parts)-1 && !info.IsDir() {
			return nil, errors.New("baseline ancestor is not a directory")
		}
	}
	return info, nil
}

func baselineRootUnchanged(name string, initial fs.FileInfo) error {
	current, err := os.Lstat(name)
	if err != nil || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(initial, current) {
		return errors.New("repository root was replaced")
	}
	return nil
}

func sameBaselinePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func validateBaselinePath(name string) error {
	if name == "." || !fs.ValidPath(name) || !utf8.ValidString(name) || strings.ContainsAny(name, "\\:") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return errors.New("baseline contains an unsafe relative path")
	}
	for _, part := range strings.Split(name, "/") {
		device := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") || device == "CON" || device == "PRN" || device == "AUX" || device == "NUL" || len(device) == 4 && (strings.HasPrefix(device, "COM") || strings.HasPrefix(device, "LPT")) && device[3] >= '1' && device[3] <= '9' {
			return errors.New("baseline contains a Windows device or alias path")
		}
	}
	return nil
}

func baselineExclusion(name string) (string, string) {
	parts := strings.Split(name, "/")
	for i, original := range parts {
		part := strings.ToLower(original)
		switch part {
		case ".git", ".agent", ".runtime", ".scratch", "node_modules", "dist", "build", "__pycache__", ".venv", "venv", ".pytest_cache", "coverage":
			return strings.Join(parts[:i+1], "/") + "/", "generated or runtime directory"
		}
		switch part {
		case ".ssh", ".aws", ".azure", ".kube", ".gnupg":
			return strings.Join(parts[:i+1], "/") + "/", "credential directory"
		}
		if i > 0 && (strings.EqualFold(parts[i-1], ".codex") && (part == "auth.json" || part == "config.toml") || strings.EqualFold(parts[i-1], ".config") && part == "gcloud") {
			return strings.Join(parts[:i+1], "/"), "credential configuration"
		}
		if strings.HasPrefix(part, ".env") || part == "credentials" || part == "credentials.json" || part == "api密钥.txt" || part == ".npmrc" || part == ".pypirc" || part == ".netrc" || part == "id_rsa" || part == "id_ed25519" || part == "id_ecdsa" || part == "id_dsa" {
			return strings.Join(parts[:i+1], "/"), "credential file"
		}
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pem", ".key", ".p12", ".pfx", ".sqlite", ".db", ".log", ".exe":
		return name, "credential or generated file"
	}
	return "", ""
}

func baselineDigest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

type baselineOutput struct{ buffer bytes.Buffer }

func (buffer *baselineOutput) Write(data []byte) (int, error) {
	if len(data) > (8<<20)-buffer.buffer.Len() {
		return 0, errors.New("baseline Git output limit exceeded")
	}
	return buffer.buffer.Write(data)
}

func baselineGitOutput(ctx context.Context, root string, args ...string) (string, error) {
	return boundedGitOutput(ctx, filepath.Join(root, ".git"), root, args...)
}

func boundedGitOutput(ctx context.Context, gitDir, root string, args ...string) (string, error) {
	parameters := append([]string{"--git-dir=" + gitDir, "--work-tree=" + root, "-c", "core.excludesFile=" + os.DevNull}, safety.HardenedGitArgs(root, args[0], args[1:])...)
	command := exec.CommandContext(ctx, "git", parameters...)
	command.Env = scrubGitEnvironment()
	var output baselineOutput
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("baseline Git %s failed: %w", args[0], err)
	}
	return output.buffer.String(), nil
}
