package worktree

import (
	"code-agent/internal/safety"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type FileApplication struct {
	Path, Destination, Operation string
	Before, After                string
	BeforeExists, AfterExists    bool
	BeforeMode, AfterMode        uint32
}

type FileApplicationResult struct {
	State              string   `json:"state"`
	Reason             string   `json:"reason,omitempty"`
	CreatedDirectories []string `json:"createdDirectories,omitempty"`
}

// ApplyFiles preflights the whole set. A nil outcome records the next item's
// intent; a non-nil outcome records its result before any following effect.
func (m *Manager) ApplyFiles(ctx context.Context, baseline Baseline, changes []FileApplication, record func(int, *FileApplicationResult) error) ([]FileApplicationResult, error) {
	results := make([]FileApplicationResult, len(changes))
	for index := range results {
		results[index].State = "not_started"
	}
	if record == nil || len(changes) == 0 || len(changes) > maxBaselineFiles {
		return results, errors.New("application requires bounded changes and a recorder")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	copy := baseline
	copy.Checksum = ""
	encoded, err := json.Marshal(copy)
	if err != nil || baselineDigest(encoded) != baseline.Checksum {
		return results, errors.New("invalid application baseline")
	}
	current, err := m.CaptureBaseline(ctx)
	if err != nil || current.Checksum != baseline.Checksum {
		return results, errors.New("original repository baseline changed")
	}
	root, err := openTaskRoot(m.root)
	if err != nil {
		return results, err
	}
	defer root.Close()
	metadata, err := openBaselineMetadata(root)
	if err != nil {
		return results, err
	}
	defer metadata.Close()
	stopMetadata, err := pinTaskMetadata(ctx, metadata, filepath.Join(m.root, ".git"))
	if err != nil {
		return results, err
	}
	defer stopMetadata()
	affected := map[string]bool{}
	parents := []string{m.root}
	total := 0
	for _, change := range changes {
		if err := validateFileApplication(change); err != nil {
			return results, err
		}
		total += len(change.After)
		if total > maxBaselineBytes {
			return results, errors.New("application exceeds source bounds")
		}
		for _, name := range []string{change.Path, change.Destination} {
			if name == "" {
				continue
			}
			for prior := range affected {
				if sameBaselinePath(name, prior) || strings.HasPrefix(strings.ToLower(name), strings.ToLower(prior)+"/") || strings.HasPrefix(strings.ToLower(prior), strings.ToLower(name)+"/") {
					return results, errors.New("application paths overlap or alias")
				}
			}
			affected[name] = true
			parent, err := applicationParent(root, name)
			if err != nil {
				return results, err
			}
			parents = append(parents, filepath.Join(m.root, filepath.FromSlash(parent)))
		}
		var original BaselineFile
		for _, file := range baseline.Files {
			if sameBaselinePath(file.Path, change.Path) {
				if file.Path != change.Path || file.PublicTestFixture {
					return results, errors.New("application aliases an original path or public fixture")
				}
				original = file
				break
			}
		}
		if original.Exists != change.BeforeExists || original.Exists && (original.SHA256 != baselineDigest([]byte(change.Before)) || original.Mode != change.BeforeMode) {
			return results, errors.New("application contradicts original source")
		}
	}
	stopParents, err := safety.PinDirectories(parents...)
	if err != nil {
		return results, err
	}
	defer stopParents()
	unaffected := []string{}
	for _, file := range baseline.Files {
		if file.Exists && !affected[file.Path] {
			unaffected = append(unaffected, filepath.Join(m.root, filepath.FromSlash(file.Path)))
		}
	}
	stopFiles, err := safety.PinReadOnlyFiles(unaffected...)
	if err != nil {
		return results, err
	}
	defer stopFiles()
	current, err = m.CaptureBaseline(ctx)
	if err != nil || current.Checksum != baseline.Checksum {
		return results, errors.New("original repository baseline changed")
	}
	handles := make([]*os.File, len(changes))
	defer func() {
		for _, file := range handles {
			if file != nil {
				file.Close()
			}
		}
	}()
	for index, change := range changes {
		if !change.BeforeExists {
			if _, err := baselineFileInfo(root, change.Path); !errors.Is(err, fs.ErrNotExist) {
				return results, errors.New("creation target exists or is unsafe")
			}
		} else {
			if _, err := baselineFileInfo(root, change.Path); err != nil {
				return results, err
			}
			file, err := safety.OpenExclusiveFile(filepath.Join(m.root, filepath.FromSlash(change.Path)), change.Operation)
			if err != nil {
				return results, err
			}
			handles[index] = file
			data, err := io.ReadAll(io.LimitReader(file, maxBaselineFileBytes+1))
			info, statErr := file.Stat()
			if err != nil || statErr != nil || string(data) != change.Before || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != change.BeforeMode {
				return results, errors.New("application source changed")
			}
		}
		if change.Destination != "" {
			if _, err := baselineFileInfo(root, change.Destination); !errors.Is(err, fs.ErrNotExist) {
				return results, errors.New("rename target exists or is unsafe")
			}
		}
	}
	for index, change := range changes {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		if err := record(index, nil); err != nil {
			return results, err
		}
		result, applyErr := applyFileApplication(ctx, root, m.root, change, handles[index])
		handles[index] = nil
		results[index] = result
		if err := record(index, &result); err != nil {
			results[index].State = "unknown"
			results[index].Reason = "item outcome was not persisted"
			return results, err
		}
		if applyErr != nil {
			return results, applyErr
		}
	}
	return results, nil
}

func validateFileApplication(change FileApplication) error {
	if validateBaselinePath(change.Path) != nil || len(change.Before) > maxBaselineFileBytes || len(change.After) > maxBaselineFileBytes || taskCredentialPattern.MatchString(change.After) {
		return errors.New("invalid application file")
	}
	if _, reason := baselineExclusion(change.Path); reason != "" {
		return errors.New("excluded application file")
	}
	switch change.Operation {
	case "create":
		if change.BeforeExists || !change.AfterExists || change.Before != "" || change.BeforeMode != 0 || change.Destination != "" {
			return errors.New("invalid file creation")
		}
	case "edit":
		if !change.BeforeExists || !change.AfterExists || change.Destination != "" || change.Before == change.After && change.BeforeMode == change.AfterMode {
			return errors.New("invalid file edit")
		}
	case "delete":
		if !change.BeforeExists || change.AfterExists || change.After != "" || change.Destination != "" || change.AfterMode != 0 {
			return errors.New("invalid file deletion")
		}
	case "rename":
		if !change.BeforeExists || !change.AfterExists || validateBaselinePath(change.Destination) != nil || sameBaselinePath(change.Path, change.Destination) {
			return errors.New("invalid file rename")
		}
		if _, reason := baselineExclusion(change.Destination); reason != "" {
			return errors.New("excluded rename target")
		}
	default:
		return errors.New("unsupported file application")
	}
	if change.AfterExists && change.AfterMode != 0666 {
		return errors.New("unsupported Windows application mode")
	}
	return nil
}

func applicationParent(root *os.Root, name string) (string, error) {
	parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(name)))
	for {
		info, err := baselineFileInfo(root, parent)
		if err == nil {
			if !info.IsDir() {
				return "", errors.New("application parent is not a directory")
			}
			return parent, nil
		}
		if !errors.Is(err, fs.ErrNotExist) || parent == "." {
			return "", err
		}
		parent = filepath.ToSlash(filepath.Dir(filepath.FromSlash(parent)))
	}
}

func applyFileApplication(ctx context.Context, root *os.Root, absolute string, change FileApplication, file *os.File) (result FileApplicationResult, err error) {
	result.State = "conflict"
	effected := false
	defer func() {
		if file != nil {
			if closeErr := file.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}
		if err != nil {
			if effected {
				result.State = "unknown"
			}
			result.Reason = "file application requires reconciliation"
		}
	}()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	target := change.Path
	if change.Destination != "" {
		target = change.Destination
	}
	parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(target)))
	missing := []string{}
	for name := parent; name != "."; name = filepath.ToSlash(filepath.Dir(filepath.FromSlash(name))) {
		if _, parentErr := baselineFileInfo(root, name); errors.Is(parentErr, fs.ErrNotExist) {
			missing = append(missing, name)
		} else if parentErr != nil {
			return result, parentErr
		} else {
			break
		}
	}
	for index := len(missing) - 1; index >= 0; index-- {
		if err = root.Mkdir(missing[index], 0755); err != nil {
			return result, err
		}
		effected = true
		result.CreatedDirectories = append(result.CreatedDirectories, missing[index])
	}
	release, err := safety.PinDirectories(filepath.Join(absolute, filepath.FromSlash(parent)))
	if err != nil {
		return result, err
	}
	defer release()
	if change.Operation == "create" {
		file, err = safety.OpenExclusiveFile(filepath.Join(absolute, filepath.FromSlash(change.Path)), "create")
		if err != nil {
			if errors.Is(err, safety.ErrExclusiveOutcomeUnknown) {
				effected = true
			}
			return result, err
		}
		effected = true
	}
	if change.Operation == "create" || change.Operation == "edit" || change.Operation == "rename" && change.Before != change.After {
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return result, err
		}
		effected = true
		if _, err = file.WriteString(change.After); err != nil {
			return result, err
		}
		if err = file.Truncate(int64(len(change.After))); err != nil {
			return result, err
		}
		if err = file.Sync(); err != nil {
			return result, err
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return result, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxBaselineFileBytes+1))
		if readErr != nil || string(data) != change.After {
			return result, errors.New("application content outcome unknown")
		}
	}
	if change.Operation == "delete" {
		effected = true
		if err = safety.DeleteExclusiveFile(file); err != nil {
			return result, err
		}
	}
	if change.Operation == "rename" {
		effected = true
		if err = safety.RenameExclusiveFile(file, filepath.Join(absolute, filepath.FromSlash(change.Destination))); err != nil {
			return result, err
		}
	}
	err = file.Close()
	file = nil
	if err != nil {
		return result, err
	}
	if change.Operation == "delete" || change.Operation == "rename" {
		if _, statErr := baselineFileInfo(root, change.Path); !errors.Is(statErr, fs.ErrNotExist) {
			return result, errors.New("application source existence unknown")
		}
	}
	if change.AfterExists {
		data, exists, readErr := readBaselineFile(root, target, maxBaselineFileBytes)
		if readErr != nil || !exists || string(data) != change.After {
			return result, errors.New("application target outcome unknown")
		}
	}
	result.State = "applied"
	return result, nil
}
