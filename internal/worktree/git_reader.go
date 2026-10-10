package worktree

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/gcfg"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/idxfile"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

type gitReader struct {
	*filesystem.Storage
	ctx        context.Context
	ignoreCase bool
	fs         *gitMetadataFS
	objects    cache.Object
	packs      map[string]*idxfile.MemoryIndex
	inflated   int64
}

func openGitReader(ctx context.Context, metadata *os.Root) (*gitReader, error) {
	fs, err := snapshotGitFilesystem(ctx, metadata)
	if err != nil {
		return nil, err
	}
	objects := cache.NewObjectLRU(8 << 20)
	store := filesystem.NewStorageWithOptions(fs, objects, filesystem.Options{LargeObjectThreshold: 1})
	config, err := store.Config()
	if err != nil || config.Extensions.ObjectFormat != "" && config.Extensions.ObjectFormat != "sha1" {
		store.Close()
		// shortcut: the selected codec supports SHA-1; add a SHA-256 reader
		// before admitting repositories that use that object format.
		return nil, errors.New("Git object format is unsupported by the rooted reader")
	}
	options, err := readGitReaderOptions(fs, "config")
	if err != nil {
		store.Close()
		return nil, err
	}
	if options.worktreeConfig {
		worktreeOptions, err := readGitReaderOptions(fs, "config.worktree")
		if err != nil {
			store.Close()
			return nil, err
		}
		if worktreeOptions.ignoreCase != nil {
			options.ignoreCase = worktreeOptions.ignoreCase
		}
	}
	fold := options.ignoreCase != nil && *options.ignoreCase
	return &gitReader{Storage: store, ctx: ctx, ignoreCase: fold, fs: fs, objects: objects}, nil
}

func gitConfigBool(value string) (bool, error) {
	switch strings.ToLower(value) {
	case "true", "yes", "on", "1":
		return true, nil
	case "", "false", "no", "off", "0":
		return false, nil
	default:
		return false, errors.New("invalid Git boolean config")
	}
}

type gitReaderOptions struct {
	ignoreCase     *bool
	worktreeConfig bool
}

func readGitReaderOptions(fs *gitMetadataFS, name string) (options gitReaderOptions, err error) {
	file, err := fs.Open(name)
	if errors.Is(err, os.ErrNotExist) {
		return options, nil
	}
	if err != nil {
		return options, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	err = gcfg.ReadWithCallback(file, func(section, subsection, key, value string, bare bool) error {
		if subsection != "" {
			return nil
		}
		core := strings.EqualFold(section, "core") && strings.EqualFold(key, "ignorecase")
		extension := strings.EqualFold(section, "extensions") && strings.EqualFold(key, "worktreeconfig")
		if !core && !extension {
			return nil
		}
		parsed := bare
		if !bare {
			var err error
			parsed, err = gitConfigBool(value)
			if err != nil {
				return err
			}
		}
		if core {
			options.ignoreCase = &parsed
		} else {
			options.worktreeConfig = parsed
		}
		return nil
	})
	if err != nil {
		return options, errors.New("invalid Git reader config")
	}
	return options, nil
}

func (r *gitReader) EncodedObject(kind plumbing.ObjectType, hash plumbing.Hash) (plumbing.EncodedObject, error) {
	encoded, err := r.readGitObject(hash, 0, map[plumbing.Hash]bool{})
	if err == nil && kind != plumbing.AnyObject && encoded.Type() != kind {
		return nil, plumbing.ErrObjectNotFound
	}
	return encoded, err
}

func (r *gitReader) headIndex() (string, *index.Index, error) {
	ref, err := storer.ResolveReference(r.Storage, plumbing.HEAD)
	if err != nil {
		return "", nil, errors.New("baseline requires a committed HEAD")
	}
	commit, err := object.GetCommit(r, ref.Hash())
	if err != nil {
		return "", nil, err
	}
	result := &index.Index{Version: 2}
	type directory struct {
		prefix string
		hash   plumbing.Hash
	}
	pending := []directory{{hash: commit.TreeHash}}
	pathBytes, entries := 0, 0
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		tree, err := object.GetTree(r, current.hash)
		if err != nil {
			return "", nil, err
		}
		for _, entry := range tree.Entries {
			if err := r.ctx.Err(); err != nil {
				return "", nil, err
			}
			if err := validateBaselinePath(entry.Name); err != nil || strings.Contains(entry.Name, "/") {
				return "", nil, errors.New("Git tree contains an unsafe component")
			}
			name := entry.Name
			if current.prefix != "" {
				name = current.prefix + "/" + name
			}
			entries++
			pathBytes += len(name) + 1
			if entries > 100_000 || pathBytes > 8<<20 {
				return "", nil, errors.New("baseline Git inventory limit exceeded")
			}
			if entry.Mode == filemode.Dir {
				pending = append(pending, directory{prefix: name, hash: entry.Hash})
				continue
			}
			result.Entries = append(result.Entries, &index.Entry{Name: name, Hash: entry.Hash, Mode: entry.Mode})
		}
	}
	return ref.Hash().String(), result, nil
}

func sourceInventory(ctx context.Context, root, metadata *os.Root, tracked map[string]bool, fold bool) ([]string, error) {
	var paths []string
	budget := &gitIgnoreBudget{}
	patterns, err := readIgnorePatterns(metadata, "info/exclude", "", nil, fold, budget)
	if err != nil {
		return nil, err
	}
	type directory struct {
		name     string
		patterns *gitIgnoreRules
	}
	pending := []directory{{name: ".", patterns: patterns}}
	entries, pathBytes := 0, 0
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		domain := ""
		if current.name != "." {
			domain = current.name + "/"
		}
		patterns, err := readIgnorePatterns(root, filepath.ToSlash(filepath.Join(current.name, ".gitignore")), domain, current.patterns, fold, budget)
		if err != nil {
			return nil, err
		}
		dir, err := root.Open(filepath.FromSlash(current.name))
		if err != nil {
			return nil, err
		}
		for {
			batch, readErr := dir.ReadDir(128)
			for _, entry := range batch {
				name := filepath.ToSlash(filepath.Join(current.name, entry.Name()))
				if name == ".git" {
					continue
				}
				entries++
				pathBytes += len(name) + 1
				if ctx.Err() != nil || entries > 100_000 || pathBytes > 8<<20 {
					dir.Close()
					return nil, errors.New("source inventory canceled or limit exceeded")
				}
				if err := validateBaselinePath(name); err != nil {
					dir.Close()
					return nil, err
				}
				if !tracked[name] {
					ignored, err := gitIgnored(ctx, patterns, name, entry.IsDir())
					if err != nil {
						dir.Close()
						return nil, err
					}
					if ignored {
						continue
					}
				}
				if _, reason := baselineExclusion(name); reason != "" {
					paths = append(paths, name)
					continue
				}
				info, err := baselineFileInfo(root, name)
				if err != nil {
					dir.Close()
					return nil, err
				}
				if info.IsDir() {
					pending = append(pending, directory{name: name, patterns: patterns})
				} else {
					paths = append(paths, name)
				}
			}
			if readErr != nil {
				dir.Close()
				if !errors.Is(readErr, io.EOF) {
					return nil, readErr
				}
				break
			}
		}
	}
	return paths, nil
}
