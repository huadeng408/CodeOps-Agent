package worktree

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-billy/v5"
	gitbinary "github.com/go-git/go-git/v5/utils/binary"
)

// Git readers see one bounded inventory, never the host namespace or entries
// inserted after capture. Open handles remain anchored by os.Root.
type gitMetadataFS struct {
	ctx      context.Context
	root     *os.Root
	prefix   string
	entries  map[string]fs.FileInfo
	children map[string][]fs.FileInfo
}

func snapshotGitFilesystem(ctx context.Context, root *os.Root) (*gitMetadataFS, error) {
	initial, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	result := &gitMetadataFS{ctx: ctx, root: root, entries: map[string]fs.FileInfo{".": initial}, children: map[string][]fs.FileInfo{}}
	pending, pathBytes := []string{"."}, 0
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		dir, err := root.Open(filepath.FromSlash(name))
		if err != nil {
			return nil, err
		}
		for {
			batch, readErr := dir.ReadDir(128)
			for _, entry := range batch {
				path := filepath.ToSlash(filepath.Join(name, entry.Name()))
				pathBytes += len(path)
				if ctx.Err() != nil || len(result.entries) >= 100_001 || pathBytes > 8<<20 {
					dir.Close()
					return nil, errors.New("Git inventory canceled or limit exceeded")
				}
				if err := validateBaselinePath(path); err != nil {
					dir.Close()
					return nil, err
				}
				info, err := baselineFileInfo(root, path)
				if err != nil || !info.IsDir() && !info.Mode().IsRegular() {
					dir.Close()
					return nil, errors.New("unsafe Git inventory entry")
				}
				result.entries[path] = info
				result.children[name] = append(result.children[name], info)
				if info.IsDir() {
					pending = append(pending, path)
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
		sort.Slice(result.children[name], func(i, j int) bool { return result.children[name][i].Name() < result.children[name][j].Name() })
	}
	return result, nil
}

func (s *gitMetadataFS) path(name string) (string, error) {
	if err := s.ctx.Err(); err != nil {
		return "", err
	}
	name = filepath.ToSlash(name)
	if name == "" || name == "." {
		name = "."
	} else if err := validateBaselinePath(name); err != nil {
		return "", billy.ErrCrossedBoundary
	}
	path := filepath.ToSlash(filepath.Join(s.prefix, name))
	if _, ok := s.entries[path]; !ok {
		return "", fs.ErrNotExist
	}
	return path, nil
}

func (s *gitMetadataFS) Open(name string) (billy.File, error) {
	return s.OpenFile(name, os.O_RDONLY, 0)
}

func (s *gitMetadataFS) OpenFile(name string, flag int, _ os.FileMode) (billy.File, error) {
	if flag != os.O_RDONLY {
		return nil, billy.ErrReadOnly
	}
	path, err := s.path(name)
	if err != nil {
		return nil, err
	}
	info := s.entries[path]
	if !info.Mode().IsRegular() || filepath.Ext(path) != ".pack" && info.Size() > 16<<20 {
		return nil, errors.New("Git reader requires a bounded regular file")
	}
	current, err := baselineFileInfo(s.root, path)
	if err != nil || !sameGitFile(info, current) {
		return nil, errors.New("Git inventory file changed")
	}
	file, err := s.root.Open(filepath.FromSlash(path))
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !sameGitFile(info, opened) {
		file.Close()
		return nil, errors.New("Git inventory file replaced")
	}
	if err := validateGitIndexHeader(path, info.Size(), file); err != nil {
		file.Close()
		return nil, err
	}
	return &gitMetadataFile{File: file, ctx: s.ctx, initial: info, name: name}, nil
}

// Check declared counts before a codec can allocate from an untrusted header.
func validateGitIndexHeader(name string, size int64, file *os.File) error {
	if name == "index" {
		var header [12]byte
		if _, err := file.ReadAt(header[:], 0); err != nil || string(header[:4]) != "DIRC" || size < 32 || uint64(binary.BigEndian.Uint32(header[8:]))*62 > uint64(size-32) {
			return errors.New("invalid or oversized Git index header")
		}
		if err := validateGitIndexNames(file, size, binary.BigEndian.Uint32(header[4:8]), binary.BigEndian.Uint32(header[8:])); err != nil {
			return err
		}
	}
	if strings.HasPrefix(name, "objects/pack/") && strings.HasSuffix(name, ".idx") {
		var header [1032]byte
		if _, err := file.ReadAt(header[:], 0); err != nil {
			return errors.New("invalid Git pack index header")
		}
		start, minimum, base := 0, int64(24), int64(1064)
		if binary.BigEndian.Uint32(header[:4]) == 0xff744f63 {
			if binary.BigEndian.Uint32(header[4:8]) != 2 {
				return errors.New("unsupported Git pack index")
			}
			start, minimum, base = 8, 28, 1072
		}
		var previous uint32
		for i := 0; i < 256; i++ {
			count := binary.BigEndian.Uint32(header[start+i*4:])
			if count < previous {
				return errors.New("invalid Git pack index fanout")
			}
			previous = count
		}
		if size < base || int64(previous) > (size-base)/minimum {
			return errors.New("invalid or oversized Git pack index count")
		}
	}
	return nil
}

// Version 4 prefix compression can expand far beyond the index's disk size.
func validateGitIndexNames(file *os.File, size int64, version, count uint32) error {
	if version < 2 || version > 4 {
		return errors.New("unsupported Git index version")
	}
	reader := bufio.NewReader(io.NewSectionReader(file, 12, size-32))
	var previous, total int64
	for i := uint32(0); i < count; i++ {
		var header [62]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return err
		}
		fixed := int64(62)
		flags := binary.BigEndian.Uint16(header[60:])
		if flags&0x4000 != 0 {
			if _, err := io.CopyN(io.Discard, reader, 2); err != nil {
				return err
			}
			fixed += 2
		}
		length, consumed := int64(flags&0xfff), int64(flags&0xfff)
		if version == 4 || length == 0xfff {
			prefix := int64(0)
			if version == 4 {
				strip, err := gitbinary.ReadVariableWidthInt(reader)
				if err != nil || strip < 0 || strip > previous {
					return errors.New("invalid Git index prefix")
				}
				prefix = previous - strip
			}
			length, consumed = prefix, 0
			for {
				value, err := reader.ReadByte()
				if err != nil {
					return err
				}
				consumed++
				if value == 0 {
					break
				}
				length++
				if length > 8<<20 {
					return errors.New("Git index path length limit exceeded")
				}
			}
		} else if _, err := io.CopyN(io.Discard, reader, length); err != nil {
			return err
		}
		total += length + 1
		if total > 8<<20 {
			return errors.New("Git index decoded path limit exceeded")
		}
		if version != 4 {
			padding := 8 - (fixed+length)%8 - (consumed - length)
			if padding > 0 {
				if _, err := io.CopyN(io.Discard, reader, padding); err != nil {
					return err
				}
			}
		}
		previous = length
	}
	return nil
}

func sameGitFile(left, right fs.FileInfo) bool {
	return right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func (s *gitMetadataFS) Stat(name string) (os.FileInfo, error) {
	path, err := s.path(name)
	if err != nil {
		return nil, err
	}
	return s.entries[path], nil
}
func (s *gitMetadataFS) Lstat(name string) (os.FileInfo, error) { return s.Stat(name) }
func (s *gitMetadataFS) ReadDir(name string) ([]os.FileInfo, error) {
	path, err := s.path(name)
	if err != nil {
		return nil, err
	}
	if !s.entries[path].IsDir() {
		return nil, errors.New("Git inventory path is not a directory")
	}
	return append([]os.FileInfo(nil), s.children[path]...), nil
}
func (s *gitMetadataFS) Chroot(name string) (billy.Filesystem, error) {
	path, err := s.path(name)
	if err != nil {
		return nil, err
	}
	if !s.entries[path].IsDir() {
		return nil, billy.ErrCrossedBoundary
	}
	copy := *s
	copy.prefix = path
	return &copy, nil
}
func (s *gitMetadataFS) Root() string                { return "." }
func (s *gitMetadataFS) Join(names ...string) string { return filepath.Join(names...) }
func (s *gitMetadataFS) Capabilities() billy.Capability {
	return billy.ReadCapability | billy.SeekCapability
}
func (s *gitMetadataFS) Create(string) (billy.File, error)           { return nil, billy.ErrReadOnly }
func (s *gitMetadataFS) TempFile(string, string) (billy.File, error) { return nil, billy.ErrReadOnly }
func (s *gitMetadataFS) Rename(string, string) error                 { return billy.ErrReadOnly }
func (s *gitMetadataFS) Remove(string) error                         { return billy.ErrReadOnly }
func (s *gitMetadataFS) MkdirAll(string, os.FileMode) error          { return billy.ErrReadOnly }
func (s *gitMetadataFS) Symlink(string, string) error                { return billy.ErrReadOnly }
func (s *gitMetadataFS) Readlink(string) (string, error)             { return "", billy.ErrNotSupported }

type gitMetadataFile struct {
	*os.File
	ctx     context.Context
	initial fs.FileInfo
	name    string
}

func (f *gitMetadataFile) Name() string { return f.name }

func (f *gitMetadataFile) Read(data []byte) (int, error) {
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	return f.File.Read(data)
}
func (f *gitMetadataFile) ReadAt(data []byte, offset int64) (int, error) {
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	return f.File.ReadAt(data, offset)
}
func (f *gitMetadataFile) Close() error {
	current, err := f.File.Stat()
	if err == nil && !sameGitFile(f.initial, current) {
		err = errors.New("Git file changed while reading")
	}
	return errors.Join(err, f.File.Close())
}
func (f *gitMetadataFile) Write([]byte) (int, error) { return 0, billy.ErrReadOnly }
func (f *gitMetadataFile) Truncate(int64) error      { return billy.ErrReadOnly }
func (f *gitMetadataFile) Lock() error               { return billy.ErrNotSupported }
func (f *gitMetadataFile) Unlock() error             { return billy.ErrNotSupported }
