package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"code-agent/internal/safety"
	"code-agent/internal/tools"
)

type taskSpillStore struct {
	root  string
	store tools.SpillStore
}

func newTaskSpillStore(storage, namespace string) (tools.SpillStore, error) {
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
	store, err := tools.NewFileSpillStore(path)
	if err != nil {
		return nil, err
	}
	return &taskSpillStore{root: path, store: store}, nil
}

func (s *taskSpillStore) Save(ctx context.Context, request tools.SpillRequest) (tools.SpillRef, error) {
	release, err := safety.PinDirectories(s.root)
	if err != nil {
		return tools.SpillRef{}, err
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
