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
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	gitindex "github.com/go-git/go-git/v5/plumbing/format/index"
)

// TaskWorkspace is a retained preparation lease, not an executing Agent. Its
// path is derived from host-approved storage; the Ledger stores no file bodies.
type TaskWorkspace struct {
	LeaseID        string    `json:"lease_id"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	Baseline       Baseline  `json:"baseline"`
	IndexSHA256    string    `json:"index_sha256,omitempty"`
}

func (m *Manager) PlanTask(ctx context.Context) (TaskWorkspace, error) {
	baseline, err := m.CaptureBaseline(ctx)
	if err != nil {
		return TaskWorkspace{}, err
	}
	id, err := newLeaseID()
	if err != nil {
		return TaskWorkspace{}, err
	}
	return TaskWorkspace{LeaseID: id, LeaseExpiresAt: time.Now().UTC().Add(m.leaseTTL), Baseline: baseline}, nil
}

// PrepareTask only creates a new detached, locked worktree. A failed copy is
// retained for reconciliation; no retry overwrites or deletes an existing path.
func (m *Manager) PrepareTask(ctx context.Context, storage string, task *TaskWorkspace) error {
	if task == nil || task.IndexSHA256 != "" {
		return errors.New("task preparation requires an unprepared lease")
	}
	if err := task.Validate(); err != nil {
		return err
	}
	current, err := m.CaptureBaseline(ctx)
	if err != nil {
		return err
	}
	if current.Checksum != task.Baseline.Checksum {
		return errors.New("task baseline changed before preparation")
	}
	if time.Now().After(task.LeaseExpiresAt) {
		return errors.New("task preparation lease expired")
	}
	source, err := openTaskRoot(m.root)
	if err != nil {
		return err
	}
	defer source.Close()
	sourceInfo, err := source.Stat(".")
	if err != nil {
		return err
	}
	metadata, err := openBaselineMetadata(source)
	if err != nil {
		return err
	}
	defer metadata.Close()
	store, err := m.openTaskStorage(storage)
	if err != nil {
		return err
	}
	defer store.Close()
	storeInfo, err := store.Stat(".")
	if err != nil {
		return err
	}
	release, err := safety.PinDirectories(m.root, filepath.Join(m.root, ".git"), storage)
	if err != nil {
		return err
	}
	defer release()
	if _, err := baselineFileInfo(metadata, "worktrees/"+task.LeaseID); !errors.Is(err, fs.ErrNotExist) {
		return errors.New("task Git registration already exists or is unsafe")
	}
	// Scan all admitted bytes before any copy; this does not sanitize Git history.
	for _, item := range task.Baseline.Files {
		data, exists, err := readBaselineFile(source, item.Path, maxBaselineFileBytes)
		if err != nil || !matchesTaskFile(item, data, exists) {
			return errors.New("task source changed or is unsafe")
		}
		if taskCredentialPattern.Match(data) {
			return errors.New("credential-shaped content requires removal before task preparation")
		}
	}
	if err := baselineMetadataUnchanged(ctx, source, metadata); err != nil {
		return err
	}
	if err := baselineRootUnchanged(m.root, sourceInfo); err != nil {
		return err
	}
	if err := baselineRootUnchanged(storage, storeInfo); err != nil {
		return err
	}
	if err := metadata.Mkdir("worktrees", 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if _, err := baselineFileInfo(metadata, "worktrees"); err != nil {
		return err
	}
	releaseRegistrations, err := safety.PinDirectories(filepath.Join(m.root, ".git", "worktrees"))
	if err != nil {
		return err
	}
	defer releaseRegistrations()
	releaseMetadata, err := pinTaskMetadata(ctx, metadata, filepath.Join(m.root, ".git"))
	if err != nil {
		return err
	}
	defer releaseMetadata()
	if err := baselineMetadataUnchanged(ctx, source, metadata); err != nil {
		return err
	}
	if err := baselineRootUnchanged(m.root, sourceInfo); err != nil {
		return err
	}
	if err := baselineRootUnchanged(storage, storeInfo); err != nil {
		return err
	}
	reader, err := openGitReader(ctx, metadata)
	if err != nil {
		return err
	}
	defer reader.Close()
	head, headIndex, err := reader.headIndex()
	if err != nil || head != task.Baseline.HeadCommit {
		return errors.New("task Git baseline changed before registration")
	}
	if err := store.Mkdir(task.LeaseID, 0700); err != nil {
		return errors.New("task storage already exists or is unavailable")
	}
	target := filepath.Join(storage, task.LeaseID)
	releaseTarget, err := safety.PinDirectories(target)
	if err != nil {
		return err
	}
	defer releaseTarget()
	if err := safety.ProtectPrivatePath(target); err != nil {
		return errors.New("private task directory could not be secured")
	}
	destination, err := store.OpenRoot(task.LeaseID)
	if err != nil {
		return err
	}
	defer destination.Close()
	destinationInfo, err := destination.Stat(".")
	if err != nil {
		return err
	}
	if err := validateBaselineMetadata(ctx, metadata); err != nil {
		return err
	}
	gitDir := filepath.Join(m.root, ".git", "worktrees", task.LeaseID)
	if err := metadata.Mkdir("worktrees/"+task.LeaseID, 0700); err != nil {
		return err
	}
	releaseGit, err := safety.PinDirectories(gitDir)
	if err != nil {
		return err
	}
	defer releaseGit()
	if err := safety.ProtectPrivatePath(gitDir); err != nil {
		return err
	}
	admin, err := metadata.OpenRoot("worktrees/" + task.LeaseID)
	if err != nil {
		return err
	}
	defer admin.Close()
	for _, control := range []struct{ name, value string }{
		{"commondir", "../..\n"}, {"HEAD", task.Baseline.HeadCommit + "\n"},
		{"locked", task.LeaseID + "\n"}, {"gitdir", filepath.ToSlash(filepath.Join(target, ".git")) + "\n"},
	} {
		if err := writeGitControl(admin, control.name, control.value); err != nil {
			return err
		}
	}
	if err := writeGitControl(destination, ".git", "gitdir: "+filepath.ToSlash(gitDir)+"\n"); err != nil {
		return err
	}
	if err := baselineMetadataUnchanged(ctx, source, metadata); err != nil {
		return err
	}
	if err := baselineRootUnchanged(target, destinationInfo); err != nil {
		return err
	}
	indexFile, err := admin.OpenFile("index", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	encodeErr := gitindex.NewEncoder(indexFile).Encode(headIndex)
	syncErr := indexFile.Sync()
	closeErr := indexFile.Close()
	if err := errors.Join(encodeErr, syncErr, closeErr); err != nil {
		return err
	}
	index, exists, err := readBaselineFile(admin, "index", 16<<20)
	if err != nil || !exists {
		return errors.New("task Git index was not initialized")
	}
	task.IndexSHA256 = baselineDigest(index)
	releaseAdmin, err := pinTaskMetadata(ctx, admin, gitDir)
	if err != nil {
		return err
	}
	defer releaseAdmin()
	if _, err := m.taskGitDir(metadata, destination, target, *task); err != nil {
		return err
	}
	for _, item := range task.Baseline.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, exists, err := readBaselineFile(source, item.Path, maxBaselineFileBytes)
		if err != nil || !matchesTaskFile(item, data, exists) || taskCredentialPattern.Match(data) {
			return errors.New("task source changed or is unsafe")
		}
		if !exists {
			continue
		}
		if err := destination.MkdirAll(filepath.Dir(filepath.FromSlash(item.Path)), 0700); err != nil {
			return err
		}
		file, err := destination.OpenFile(filepath.FromSlash(item.Path), os.O_CREATE|os.O_EXCL|os.O_WRONLY, fs.FileMode(item.Mode))
		if err != nil {
			return err
		}
		_, writeErr := file.Write(data)
		modeErr := file.Chmod(fs.FileMode(item.Mode))
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if modeErr != nil {
			return modeErr
		}
	}
	if err := baselineRootUnchanged(target, destinationInfo); err != nil {
		return err
	}
	if err := baselineRootUnchanged(storage, storeInfo); err != nil {
		return err
	}
	if err := baselineRootUnchanged(m.root, sourceInfo); err != nil {
		return err
	}
	current, err = m.CaptureBaseline(ctx)
	if err != nil || current.Checksum != task.Baseline.Checksum {
		return errors.New("task source changed during preparation; retain and reconcile")
	}
	return m.VerifyTask(ctx, storage, *task)
}

// VerifyTask validates the explicit Harness-owned linked metadata relation and
// the copied baseline. It never promotes a partial or modified copy to ready.
func (m *Manager) VerifyTask(ctx context.Context, storage string, task TaskWorkspace) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := task.Validate(); err != nil {
		return err
	}
	store, err := m.openTaskStorage(storage)
	if err != nil {
		return err
	}
	defer store.Close()
	storeInfo, err := store.Stat(".")
	if err != nil {
		return err
	}
	if _, err := baselineFileInfo(store, task.LeaseID); err != nil {
		return err
	}
	destination, err := store.OpenRoot(task.LeaseID)
	if err != nil {
		return err
	}
	defer destination.Close()
	initial, err := destination.Stat(".")
	if err != nil {
		return err
	}
	source, err := openTaskRoot(m.root)
	if err != nil {
		return err
	}
	defer source.Close()
	sourceInfo, err := source.Stat(".")
	if err != nil {
		return err
	}
	metadata, err := openBaselineMetadata(source)
	if err != nil {
		return err
	}
	defer metadata.Close()
	if err := validateBaselineMetadata(ctx, metadata); err != nil {
		return err
	}
	target := filepath.Join(storage, task.LeaseID)
	if _, err := m.taskGitDir(metadata, destination, target, task); err != nil {
		return err
	}
	admin, err := metadata.OpenRoot("worktrees/" + task.LeaseID)
	if err != nil {
		return err
	}
	defer admin.Close()
	index, exists, err := readBaselineFile(admin, "index", 16<<20)
	if err != nil || !exists || task.IndexSHA256 == "" || baselineDigest(index) != task.IndexSHA256 {
		return errors.New("task Git index does not match prepared lease")
	}
	for _, item := range task.Baseline.Files {
		data, exists, err := readBaselineFile(destination, item.Path, maxBaselineFileBytes)
		if err != nil || !matchesTaskFile(item, data, exists) {
			return errors.New("task copy does not match recorded baseline")
		}
		if exists {
			info, err := baselineFileInfo(destination, item.Path)
			if err != nil || uint32(info.Mode().Perm()) != item.Mode {
				return errors.New("task file mode does not match baseline")
			}
		}
	}
	for _, item := range task.Baseline.Excluded {
		if _, err := destination.Lstat(filepath.FromSlash(strings.TrimSuffix(item.Path, "/"))); !errors.Is(err, fs.ErrNotExist) {
			return errors.New("excluded path is present in task copy")
		}
	}
	admitted := map[string]bool{".git": true}
	for _, item := range task.Baseline.Files {
		if item.Exists {
			admitted[item.Path] = true
			for dir := filepath.ToSlash(filepath.Dir(item.Path)); dir != "."; dir = filepath.ToSlash(filepath.Dir(dir)) {
				admitted[dir] = true
			}
		}
	}
	if err := verifyTaskInventory(ctx, destination, admitted); err != nil {
		return err
	}
	if err := baselineMetadataUnchanged(ctx, source, metadata); err != nil {
		return err
	}
	if err := baselineRootUnchanged(storage, storeInfo); err != nil {
		return err
	}
	if err := baselineRootUnchanged(m.root, sourceInfo); err != nil {
		return err
	}
	return baselineRootUnchanged(target, initial)
}

func verifyTaskInventory(ctx context.Context, root *os.Root, admitted map[string]bool) error {
	directories := []string{"."}
	for len(directories) > 0 {
		name := directories[len(directories)-1]
		directories = directories[:len(directories)-1]
		directory, err := root.Open(name)
		if err != nil {
			return err
		}
		for {
			batch, readErr := directory.ReadDir(128)
			for _, entry := range batch {
				if err := ctx.Err(); err != nil {
					directory.Close()
					return err
				}
				path := filepath.ToSlash(filepath.Join(name, entry.Name()))
				if !admitted[path] {
					directory.Close()
					return errors.New("unrecorded path in prepared task")
				}
				info, err := baselineFileInfo(root, path)
				if err != nil {
					directory.Close()
					return err
				}
				if info.IsDir() {
					directories = append(directories, path)
				}
			}
			if readErr != nil {
				directory.Close()
				if !errors.Is(readErr, io.EOF) {
					return readErr
				}
				break
			}
		}
	}
	return nil
}

// Keep the rooted reader's admitted files immutable while reading Git objects.
func pinTaskMetadata(ctx context.Context, root *os.Root, absoluteRoot string) (func(), error) {
	directories, pending, files := []string{absoluteRoot}, []string{"."}, []string{}
	entries, pathBytes := 0, 0
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		directory, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		for {
			batch, readErr := directory.ReadDir(128)
			for _, entry := range batch {
				path := filepath.ToSlash(filepath.Join(name, entry.Name()))
				entries++
				pathBytes += len(path)
				if ctx.Err() != nil || entries > 100_000 || pathBytes > 8<<20 {
					directory.Close()
					return nil, errors.New("metadata pin canceled or limit exceeded")
				}
				info, err := baselineFileInfo(root, path)
				if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
					directory.Close()
					return nil, errors.New("unsafe metadata pin path")
				}
				absolute := filepath.Join(absoluteRoot, filepath.FromSlash(path))
				if info.IsDir() {
					directories = append(directories, absolute)
					pending = append(pending, path)
				} else {
					files = append(files, absolute)
				}
			}
			if readErr != nil {
				directory.Close()
				if !errors.Is(readErr, io.EOF) {
					return nil, readErr
				}
				break
			}
		}
	}
	releaseDirectories, err := safety.PinDirectories(directories...)
	if err != nil {
		return nil, err
	}
	releaseFiles, err := safety.PinReadOnlyFiles(files...)
	if err != nil {
		releaseDirectories()
		return nil, err
	}
	return func() { releaseFiles(); releaseDirectories() }, nil
}

func (m *Manager) openTaskStorage(storage string) (*os.Root, error) {
	if pathWithin(m.root, storage, true) || pathWithin(storage, m.root, true) {
		return nil, errors.New("task storage must be separate from repository")
	}
	return openTaskRoot(storage)
}

func openTaskRoot(name string) (*os.Root, error) {
	volume := filepath.VolumeName(name)
	if !filepath.IsAbs(name) || !utf8.ValidString(name) || strings.HasPrefix(name, `\\`) || strings.HasPrefix(name, "//") || strings.Contains(name[len(volume):], ":") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return nil, errors.New("task root must be an absolute local path")
	}
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil || !sameBaselinePath(name, resolved) {
		return nil, errors.New("task root alias or unavailable directory")
	}
	if err := safety.RejectReparsePoint(name); err != nil {
		return nil, err
	}
	return os.OpenRoot(name)
}

func (m *Manager) taskGitDir(metadata, destination *os.Root, target string, task TaskWorkspace) (string, error) {
	gitDir := filepath.Join(m.root, ".git", "worktrees", task.LeaseID)
	link, exists, err := readBaselineFile(destination, ".git", 4096)
	if err != nil || !exists || !strings.HasPrefix(string(link), "gitdir: ") || !sameBaselinePath(strings.TrimRight(strings.TrimPrefix(string(link), "gitdir: "), "\r\n"), gitDir) {
		return "", errors.New("task Git link does not match approved lease")
	}
	if _, err := baselineFileInfo(metadata, "worktrees/"+task.LeaseID); err != nil {
		return "", err
	}
	admin, err := metadata.OpenRoot("worktrees/" + task.LeaseID)
	if err != nil {
		return "", errors.New("task Git registration unavailable")
	}
	defer admin.Close()
	for name, expected := range map[string]string{"commondir": "../..", "HEAD": task.Baseline.HeadCommit, "locked": task.LeaseID} {
		value, exists, err := readBaselineFile(admin, name, 4096)
		if err != nil || !exists || strings.TrimRight(string(value), "\r\n") != expected {
			return "", errors.New("task Git registration does not match lease")
		}
	}
	back, exists, err := readBaselineFile(admin, "gitdir", 4096)
	if err != nil || !exists || !sameBaselinePath(strings.TrimRight(string(back), "\r\n"), filepath.Join(target, ".git")) {
		return "", errors.New("task Git registration points elsewhere")
	}
	return gitDir, nil
}

func (task TaskWorkspace) Validate() error {
	if err := validateLeaseID(task.LeaseID); err != nil {
		return err
	}
	if task.LeaseExpiresAt.IsZero() {
		return errors.New("task lease has no expiry")
	}
	if task.IndexSHA256 != "" && (len(task.IndexSHA256) != 64 || strings.Trim(task.IndexSHA256, "0123456789abcdef") != "") {
		return errors.New("invalid task index checksum")
	}
	baseline := task.Baseline
	if len(baseline.Files) > maxBaselineFiles || len(baseline.Excluded) > maxBaselineFiles || len(baseline.RepositoryID) != 64 || (len(baseline.HeadCommit) != 40 && len(baseline.HeadCommit) != 64) || strings.Trim(baseline.HeadCommit, "0123456789abcdef") != "" {
		return errors.New("invalid task baseline identity or limits")
	}
	checksum := baseline.Checksum
	baseline.Checksum = ""
	encoded, err := json.Marshal(baseline)
	if err != nil || baselineDigest(encoded) != checksum {
		return errors.New("task baseline checksum mismatch")
	}
	for _, item := range baseline.Files {
		if err := validateBaselinePath(item.Path); err != nil {
			return err
		}
		if _, reason := baselineExclusion(item.Path); reason != "" {
			return errors.New("excluded file in task baseline")
		}
	}
	for _, item := range baseline.Excluded {
		if err := validateBaselinePath(strings.TrimSuffix(item.Path, "/")); err != nil {
			return err
		}
	}
	return nil
}

func matchesTaskFile(item BaselineFile, data []byte, exists bool) bool {
	return exists == item.Exists && (!exists || int64(len(data)) == item.Size && baselineDigest(data) == item.SHA256)
}

// shortcut: detects recognizable keys, not every secret format; keep provider
// credentials outside source and expand this policy for newly supported formats.
var taskCredentialPattern = regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----|\b(?:sk-[A-Za-z0-9_-]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|AKIA[A-Z0-9]{16})\b`)

func writeGitControl(root *os.Root, name, value string) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(value)
	syncErr := file.Sync()
	return errors.Join(writeErr, syncErr, file.Close())
}
