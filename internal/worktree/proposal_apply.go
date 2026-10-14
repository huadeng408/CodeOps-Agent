package worktree

import (
	"code-agent/internal/safety"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
)

// ApplySingleFile admits one existing file. The callback durably records intent
// while the file and its ancestors are pinned, before the first write.
func (m *Manager) ApplySingleFile(ctx context.Context, baseline Baseline, change TaskFileChange, intent func() error) (known bool, err error) {
	copy := baseline
	copy.Checksum = ""
	encoded, encodeErr := json.Marshal(copy)
	if encodeErr != nil || baselineDigest(encoded) != baseline.Checksum {
		return true, errors.New("invalid application baseline")
	}
	if intent == nil || validateBaselinePath(change.Path) != nil || len(change.After) > maxBaselineFileBytes || change.Before == change.After {
		return true, errors.New("invalid single-file application")
	}
	if _, reason := baselineExclusion(change.Path); reason != "" {
		return true, errors.New("application path is excluded")
	}
	var expected *BaselineFile
	for _, item := range baseline.Files {
		if item.Path == change.Path {
			copy := item
			expected = &copy
			break
		}
	}
	if expected == nil || !expected.Exists || expected.PublicTestFixture || baselineDigest([]byte(change.Before)) != expected.SHA256 {
		return true, errors.New("application requires an existing baseline file")
	}
	current, err := m.CaptureBaseline(ctx)
	if err != nil || current.Checksum != baseline.Checksum {
		return true, errors.New("original repository baseline changed")
	}
	root, err := openTaskRoot(m.root)
	if err != nil {
		return true, err
	}
	defer root.Close()
	metadata, err := openBaselineMetadata(root)
	if err != nil {
		return true, err
	}
	defer metadata.Close()
	releaseMetadata, err := pinTaskMetadata(ctx, metadata, filepath.Join(m.root, ".git"))
	if err != nil {
		return true, err
	}
	defer releaseMetadata()
	releaseParents, err := safety.PinDirectories(m.root, filepath.Dir(filepath.Join(m.root, filepath.FromSlash(change.Path))))
	if err != nil {
		return true, err
	}
	defer releaseParents()
	paths := []string{}
	for _, item := range baseline.Files {
		if item.Exists && item.Path != change.Path {
			paths = append(paths, filepath.Join(m.root, filepath.FromSlash(item.Path)))
		}
	}
	releaseFiles, err := safety.PinReadOnlyFiles(paths...)
	if err != nil {
		return true, err
	}
	defer releaseFiles()
	current, err = m.CaptureBaseline(ctx)
	if err != nil || current.Checksum != baseline.Checksum {
		return true, errors.New("original repository baseline changed")
	}
	if _, err := baselineFileInfo(root, change.Path); err != nil {
		return true, err
	}
	file, err := openApplicationFile(filepath.Join(m.root, filepath.FromSlash(change.Path)))
	if err != nil {
		return true, err
	}
	wrote := false
	defer func() {
		if closeErr := file.Close(); closeErr != nil && err == nil {
			err = closeErr
			known = !wrote
		}
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != expected.Mode {
		return true, errors.New("application file mode changed")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBaselineFileBytes+1))
	if err != nil || string(data) != change.Before {
		return true, errors.New("application file content changed")
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if err := intent(); err != nil {
		return true, err
	}
	if err := ctx.Err(); err != nil {
		return true, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return true, err
	}
	// Windows denies other readers/writers/deletion on this handle. A partial
	// write or crash stays unknown; recovery belongs to the application receipt.
	wrote = true
	if _, err := file.WriteString(change.After); err != nil {
		return false, err
	}
	if err := file.Truncate(int64(len(change.After))); err != nil {
		return false, err
	}
	if err := file.Sync(); err != nil {
		return false, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	data, err = io.ReadAll(io.LimitReader(file, maxBaselineFileBytes+1))
	if err != nil || string(data) != change.After {
		return false, errors.New("application result cannot be confirmed")
	}
	return true, nil
}
