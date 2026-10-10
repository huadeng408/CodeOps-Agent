package worktree

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"strconv"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/idxfile"
	"github.com/go-git/go-git/v5/plumbing/format/packfile"
)

const maxGitInflatedBytes = 64 << 20

// Bound decompression before codecs can allocate from loose or delta headers.
func (r *gitReader) reserveGitBytes(size int64) error {
	if size < 0 || size > maxBaselineFileBytes || r.inflated+size > maxGitInflatedBytes {
		return errors.New("Git object size or aggregate decode limit exceeded")
	}
	r.inflated += size
	return r.ctx.Err()
}

func (r *gitReader) readGitObject(hash plumbing.Hash, depth int, visiting map[plumbing.Hash]bool) (result plumbing.EncodedObject, err error) {
	if err := r.ctx.Err(); err != nil {
		return nil, err
	}
	if depth > 64 || visiting[hash] {
		return nil, errors.New("Git delta cycle or depth limit exceeded")
	}
	if cached, ok := r.objects.Get(hash); ok {
		return cached, nil
	}
	visiting[hash] = true
	defer delete(visiting, hash)
	name := hash.String()
	file, err := r.fs.Open("objects/" + name[:2] + "/" + name[2:])
	var kind plumbing.ObjectType
	var data []byte
	if err == nil {
		kind, data, err = r.readLooseObject(file)
		err = errors.Join(err, file.Close())
	} else if errors.Is(err, fs.ErrNotExist) {
		kind, data, err = r.readPackedObject(hash, depth, visiting)
	}
	if err != nil {
		return nil, err
	}
	if plumbing.ComputeHash(kind, data) != hash {
		return nil, errors.New("Git object checksum mismatch")
	}
	if err := r.reserveGitBytes(int64(len(data))); err != nil {
		return nil, err
	}
	encoded := &plumbing.MemoryObject{}
	encoded.SetType(kind)
	if _, err := encoded.Write(data); err != nil {
		return nil, err
	}
	r.objects.Put(encoded)
	return encoded, nil
}

func (r *gitReader) readLooseObject(file billy.File) (kind plumbing.ObjectType, data []byte, err error) {
	zr, err := zlib.NewReader(file)
	if err != nil {
		return 0, nil, errors.New("invalid compressed Git object")
	}
	defer func() { err = errors.Join(err, zr.Close()) }()
	buffered := bufio.NewReaderSize(zr, 128)
	header, err := buffered.ReadSlice(0)
	if err != nil {
		return 0, nil, errors.New("Git object header is invalid or too long")
	}
	typeName, sizeName, ok := strings.Cut(string(header[:len(header)-1]), " ")
	size, sizeErr := strconv.ParseInt(sizeName, 10, 64)
	kind, err = plumbing.ParseObjectType(typeName)
	if !ok || sizeErr != nil || err != nil || kind < plumbing.CommitObject || kind > plumbing.TagObject {
		return 0, nil, errors.New("invalid Git object header")
	}
	if err := r.reserveGitBytes(size); err != nil {
		return 0, nil, err
	}
	data, err = io.ReadAll(io.LimitReader(buffered, size+1))
	if err != nil || int64(len(data)) != size {
		return 0, nil, errors.New("Git object inflated size mismatch")
	}
	return kind, data, nil
}

func (r *gitReader) loadPackIndexes() error {
	if r.packs != nil {
		return nil
	}
	r.packs = map[string]*idxfile.MemoryIndex{}
	entries, err := r.fs.ReadDir("objects/pack")
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var total int64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "pack-") || !strings.HasSuffix(name, ".idx") {
			continue
		}
		total += entry.Size()
		if total > 64<<20 {
			return errors.New("Git pack index aggregate limit exceeded")
		}
		file, err := r.fs.Open("objects/pack/" + name)
		if err != nil {
			return err
		}
		idx := idxfile.NewMemoryIndex()
		decodeErr := idxfile.NewDecoder(file).Decode(idx)
		if err := errors.Join(decodeErr, file.Close()); err != nil {
			return err
		}
		if name != "pack-"+plumbing.Hash(idx.PackfileChecksum).String()+".idx" {
			return errors.New("Git pack index checksum mismatch")
		}
		r.packs["objects/pack/"+strings.TrimSuffix(name, ".idx")+".pack"] = idx
	}
	return nil
}

func (r *gitReader) readPackedObject(hash plumbing.Hash, depth int, visiting map[plumbing.Hash]bool) (plumbing.ObjectType, []byte, error) {
	if err := r.loadPackIndexes(); err != nil {
		return 0, nil, err
	}
	for name, idx := range r.packs {
		offset, err := idx.FindOffset(hash)
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			continue
		}
		if err != nil {
			return 0, nil, err
		}
		return r.readPackEntry(name, idx, offset, depth, visiting)
	}
	return 0, nil, plumbing.ErrObjectNotFound
}

func (r *gitReader) readPackEntry(name string, idx *idxfile.MemoryIndex, offset int64, depth int, visiting map[plumbing.Hash]bool) (kind plumbing.ObjectType, data []byte, err error) {
	file, err := r.fs.Open(name)
	if err != nil {
		return 0, nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	scanner := packfile.NewScanner(file)
	header, err := scanner.SeekObjectHeader(offset)
	if err != nil {
		return 0, nil, err
	}
	if err := r.reserveGitBytes(header.Length); err != nil {
		return 0, nil, err
	}
	body, err := scanner.ReadObject()
	if err != nil {
		return 0, nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(body, header.Length+1))
	if err := errors.Join(readErr, body.Close()); err != nil || int64(len(data)) != header.Length {
		return 0, nil, errors.New("Git pack inflated size mismatch")
	}
	if header.Type >= plumbing.CommitObject && header.Type <= plumbing.TagObject {
		return header.Type, data, nil
	}
	var baseHash plumbing.Hash
	switch header.Type {
	case plumbing.OFSDeltaObject:
		baseHash, err = idx.FindHash(header.OffsetReference)
	case plumbing.REFDeltaObject:
		baseHash = header.Reference
	default:
		return 0, nil, errors.New("unsupported Git pack object")
	}
	if err != nil {
		return 0, nil, err
	}
	delta := bytes.NewReader(data)
	sourceSize, sourceErr := binary.ReadUvarint(delta)
	targetSize, targetErr := binary.ReadUvarint(delta)
	if sourceErr != nil || targetErr != nil || sourceSize > maxBaselineFileBytes || targetSize > maxBaselineFileBytes {
		return 0, nil, errors.New("Git delta size limit exceeded")
	}
	if err := r.reserveGitBytes(int64(targetSize)); err != nil {
		return 0, nil, err
	}
	base, err := r.readGitObject(baseHash, depth+1, visiting)
	if err != nil {
		return 0, nil, err
	}
	if base.Size() != int64(sourceSize) {
		return 0, nil, errors.New("Git delta base size mismatch")
	}
	baseReader, err := base.Reader()
	if err != nil {
		return 0, nil, err
	}
	source, readErr := io.ReadAll(io.LimitReader(baseReader, base.Size()+1))
	if err := errors.Join(readErr, baseReader.Close()); err != nil {
		return 0, nil, err
	}
	patched, err := packfile.PatchDelta(source, data)
	return base.Type(), patched, err
}
