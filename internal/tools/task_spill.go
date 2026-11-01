package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"code-agent/internal/safety"
)

type taskSpillStore struct {
	root  string
	store SpillStore
}

// NewTaskSpillStore keeps Harness output outside the prepared task inventory.
func NewTaskSpillStore(storage, namespace string) (SpillStore, error) {
	if namespace == "" || namespace == "." || namespace == ".." || len(namespace) > 128 || filepath.Base(namespace) != namespace || strings.ContainsAny(namespace, "/\\:\x00\r\n") {
		return nil, errors.New("invalid task spill namespace")
	}
	release, err := safety.PinDirectories(storage)
	if err != nil {
		return nil, err
	}
	defer release()
	root, err := os.OpenRoot(storage)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, relative := range []string{".runtime", filepath.Join(".runtime", "spill"), filepath.Join(".runtime", "spill", namespace)} {
		if err := root.Mkdir(relative, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		path := filepath.Join(storage, relative)
		unpin, err := safety.PinDirectories(path)
		if err != nil {
			return nil, err
		}
		defer unpin()
		if err := safety.ProtectPrivatePath(path); err != nil {
			return nil, err
		}
	}
	path := filepath.Join(storage, ".runtime", "spill", namespace)
	store, err := NewFileSpillStore(path)
	if err != nil {
		return nil, err
	}
	return &taskSpillStore{path, store}, nil
}

func (s *taskSpillStore) Save(ctx context.Context, request SpillRequest) (SpillRef, error) {
	release, err := safety.PinDirectories(s.root)
	if err != nil {
		return SpillRef{}, err
	}
	defer release()
	return s.store.Save(ctx, request)
}
func (s *taskSpillStore) Load(ctx context.Context, locator string) (string, error) {
	release, err := safety.PinDirectories(s.root)
	if err != nil {
		return "", err
	}
	defer release()
	return s.store.Load(ctx, locator)
}
