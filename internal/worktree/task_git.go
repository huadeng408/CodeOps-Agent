package worktree

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// Export only HEAD and admitted blobs; never copy host config, other refs or history.
func taskGitSnapshot(reader *gitReader, baseline Baseline, headIndex *index.Index, metadata []plumbing.Hash, fixtures *publicTestFixturePins) (map[string][]byte, error) {
	files := map[string][]byte{
		"HEAD":    []byte(baseline.HeadCommit + "\n"),
		"shallow": []byte(baseline.HeadCommit + "\n"),
		"config":  []byte("[core]\n\trepositoryformatversion = 0\n\tbare = false\n\tfilemode = false\n\tautocrlf = true\n"),
	}
	var total int64
	copyObject := func(hash plumbing.Hash, kind plumbing.ObjectType, approvedFixture string) error {
		name := hash.String()
		path := "objects/" + name[:2] + "/" + name[2:]
		encoded, err := reader.EncodedObject(kind, hash)
		if err != nil {
			return err
		}
		body, err := encoded.Reader()
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(body, maxBaselineFileBytes+1))
		if err := errors.Join(readErr, body.Close()); err != nil {
			return err
		}
		if len(data) > maxBaselineFileBytes {
			return errors.New("task Git object snapshot exceeds bounds")
		}
		if taskCredentialPattern.Match(data) && (approvedFixture == "" || !matchesPublicTestFixtureGitBlob(fixtures, baseline.HeadCommit, approvedFixture, data)) {
			return errors.New("credential-shaped Git content requires removal before task preparation")
		}
		if _, copied := files[path]; copied {
			return nil
		}
		total += int64(len(data))
		if total > maxGitInflatedBytes {
			return errors.New("task Git object snapshot exceeds bounds")
		}
		var compressed bytes.Buffer
		writer := zlib.NewWriter(&compressed)
		_, headerErr := fmt.Fprintf(writer, "%s %d\x00", kind, len(data))
		_, writeErr := writer.Write(data)
		if err := errors.Join(headerErr, writeErr, writer.Close()); err != nil {
			return err
		}
		files[path] = compressed.Bytes()
		return nil
	}
	for i, hash := range metadata {
		kind := plumbing.TreeObject
		if i == 0 {
			kind = plumbing.CommitObject
		}
		if err := copyObject(hash, kind, ""); err != nil {
			return nil, err
		}
	}
	admitted := make(map[string]BaselineFile, len(baseline.Files))
	for _, file := range baseline.Files {
		admitted[file.Path] = file
	}
	for _, entry := range headIndex.Entries {
		file, ok := admitted[entry.Name]
		if !ok {
			// Preserve HEAD's index without materializing excluded credential blobs.
			headIndex.Version, entry.SkipWorktree = 3, true
			continue
		}
		fixture := ""
		if file.PublicTestFixture {
			fixture = file.Path
		}
		if err := copyObject(entry.Hash, plumbing.BlobObject, fixture); err != nil {
			return nil, err
		}
	}
	var encodedIndex bytes.Buffer
	if err := index.NewEncoder(&encodedIndex).Encode(headIndex); err != nil {
		return nil, err
	}
	files["index"] = encodedIndex.Bytes()
	return files, nil
}

func matchesPublicTestFixtureGitBlob(pins *publicTestFixturePins, head, path string, data []byte) bool {
	if matchesPublicTestFixture(pins, head, path, data) {
		return true
	}
	// Git's LF blob may match an exact approved CRLF checkout, with no other changes.
	return !bytes.ContainsRune(data, '\r') && matchesPublicTestFixture(pins, head, path, bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")))
}

func taskGitSnapshotChecksum(ctx context.Context, root *os.Root) (string, error) {
	inventory, err := snapshotGitFilesystem(ctx, root)
	if err != nil {
		return "", err
	}
	entries := make(map[string]string, len(inventory.entries))
	var total int64
	for name, info := range inventory.entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		entries[name] = info.Mode().String()
		if info.IsDir() {
			continue
		}
		total += info.Size()
		if total > maxGitInflatedBytes {
			return "", errors.New("task Git metadata exceeds bounds")
		}
		data, exists, err := readBaselineFile(root, name, 16<<20)
		if err != nil || !exists {
			return "", errors.New("task Git metadata changed or is unsafe")
		}
		entries[name] += ":" + baselineDigest(data)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return "", err
	}
	return baselineDigest(encoded), nil
}
