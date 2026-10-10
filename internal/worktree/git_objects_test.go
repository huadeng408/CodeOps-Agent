package worktree

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/format/idxfile"
)

func testGitReader(t *testing.T, root string) *gitReader {
	t.Helper()
	metadata, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { metadata.Close() })
	reader, err := openGitReader(context.Background(), metadata)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	return reader
}

func compressGitFixture(t *testing.T, data []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func TestGitReaderBoundsLooseObjectsBeforeDecode(t *testing.T) {
	for _, test := range []struct {
		name, header string
		body         []byte
		valid        bool
	}{
		{"valid", "blob 3\x00", []byte("abc"), true},
		{"long header", strings.Repeat("x", 4096), nil, false},
		{"oversized declaration", "blob 8388609\x00", nil, false},
		{"underdeclared inflate", "blob 1\x00", bytes.Repeat([]byte("x"), 256<<10), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeBaselineFile(t, root, "config", "[core]\nrepositoryformatversion = 0\n")
			hash := plumbing.ComputeHash(plumbing.BlobObject, test.body)
			writeBaselineFile(t, root, "objects/"+hash.String()[:2]+"/"+hash.String()[2:], string(compressGitFixture(t, append([]byte(test.header), test.body...))))
			reader := testGitReader(t, root)
			object, err := reader.EncodedObject(plumbing.BlobObject, hash)
			if test.valid {
				if err != nil || object.Size() != 3 {
					t.Fatal("valid bounded object refused:", err)
				}
			} else if err == nil {
				t.Fatal("invalid compressed object was admitted")
			}
			if reader.inflated > 8 {
				t.Fatal("invalid input reached bulk object allocation")
			}
		})
	}
}

type gitPackFixture struct {
	hash        plumbing.Hash
	typeID      plumbing.ObjectType
	size        int64
	base        plumbing.Hash
	data        []byte
	offset, crc uint32
}

func writeGitPackFixture(t *testing.T, root string, entries []gitPackFixture) {
	t.Helper()
	var pack bytes.Buffer
	pack.WriteString("PACK")
	binary.Write(&pack, binary.BigEndian, uint32(2))
	binary.Write(&pack, binary.BigEndian, uint32(len(entries)))
	for i := range entries {
		entry := &entries[i]
		entry.offset = uint32(pack.Len())
		first, remaining := byte(entry.typeID)<<4|byte(entry.size&15), uint64(entry.size)>>4
		for remaining != 0 {
			pack.WriteByte(first | 0x80)
			first, remaining = byte(remaining&127), remaining>>7
		}
		pack.WriteByte(first)
		if entry.typeID == plumbing.REFDeltaObject {
			pack.Write(entry.base[:])
		}
		pack.Write(compressGitFixture(t, entry.data))
		entry.crc = crc32.ChecksumIEEE(pack.Bytes()[entry.offset:])
	}
	checksum := sha1.Sum(pack.Bytes())
	pack.Write(checksum[:])
	name := "objects/pack/pack-" + plumbing.Hash(checksum).String()
	writeBaselineFile(t, root, name+".pack", pack.String())
	idx := idxfile.NewMemoryIndex()
	idx.Version, idx.PackfileChecksum = 2, checksum
	for i := range idx.FanoutMapping {
		idx.FanoutMapping[i] = -1
	}
	sort.Slice(entries, func(i, j int) bool { return bytes.Compare(entries[i].hash[:], entries[j].hash[:]) < 0 })
	for _, entry := range entries {
		bucket := int(entry.hash[0])
		if idx.FanoutMapping[bucket] == -1 {
			idx.FanoutMapping[bucket] = len(idx.Names)
			idx.Names, idx.CRC32, idx.Offset32 = append(idx.Names, nil), append(idx.CRC32, nil), append(idx.Offset32, nil)
		}
		position := idx.FanoutMapping[bucket]
		idx.Names[position] = append(idx.Names[position], entry.hash[:]...)
		idx.CRC32[position] = binary.BigEndian.AppendUint32(idx.CRC32[position], entry.crc)
		idx.Offset32[position] = binary.BigEndian.AppendUint32(idx.Offset32[position], entry.offset)
		for i := bucket; i < 256; i++ {
			idx.Fanout[i]++
		}
	}
	var encoded bytes.Buffer
	if _, err := idxfile.NewEncoder(&encoded).Encode(idx); err != nil {
		t.Fatal(err)
	}
	writeBaselineFile(t, root, name+".idx", encoded.String())
}

func TestGitReaderBoundsPackedDeltaObjects(t *testing.T) {
	baseHash := plumbing.ComputeHash(plumbing.BlobObject, []byte("abc"))
	targetHash := plumbing.ComputeHash(plumbing.BlobObject, []byte("abd"))
	for _, test := range []struct {
		name     string
		delta    []byte
		declared int64
		valid    bool
	}{
		{"valid", []byte{3, 3, 3, 'a', 'b', 'd'}, 6, true},
		{"underdeclared inflate", bytes.Repeat([]byte{1}, 256<<10), 1, false},
		{"oversized delta", []byte{3, 3}, 8<<20 + 1, false},
		{"oversized result", []byte{3, 0x81, 0x80, 0x80, 4}, 5, false},
		{"invalid instruction", []byte{3, 3, 0}, 3, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeBaselineFile(t, root, "config", "[core]\nrepositoryformatversion = 0\n")
			writeGitPackFixture(t, root, []gitPackFixture{
				{hash: baseHash, typeID: plumbing.BlobObject, size: 3, data: []byte("abc")},
				{hash: targetHash, typeID: plumbing.REFDeltaObject, size: test.declared, base: baseHash, data: test.delta},
			})
			reader := testGitReader(t, root)
			object, err := reader.EncodedObject(plumbing.BlobObject, targetHash)
			if !test.valid {
				if err == nil {
					t.Fatal("invalid packed object was admitted")
				}
				if reader.inflated > 32 {
					t.Fatal("invalid delta reached bulk allocation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			body, err := object.Reader()
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(body)
			body.Close()
			if err != nil || string(got) != "abd" {
				t.Fatal("delta result differs")
			}
		})
	}
}

func TestGitReaderRejectsDeltaCycleAndAggregateDecodeLimit(t *testing.T) {
	root := t.TempDir()
	writeBaselineFile(t, root, "config", "[core]\nrepositoryformatversion = 0\n")
	hash := plumbing.ComputeHash(plumbing.BlobObject, []byte("abc"))
	writeGitPackFixture(t, root, []gitPackFixture{{hash: hash, typeID: plumbing.REFDeltaObject, size: 6, base: hash, data: []byte{3, 3, 3, 'a', 'b', 'c'}}})
	reader := testGitReader(t, root)
	if _, err := reader.EncodedObject(plumbing.AnyObject, hash); err == nil {
		t.Fatal("cyclic delta admitted")
	}
	reader.inflated = maxGitInflatedBytes
	if err := reader.reserveGitBytes(1); err == nil {
		t.Fatal("aggregate decode limit ignored")
	}
	if reader.inflated != maxGitInflatedBytes {
		t.Fatal("rejected reservation changed the limit")
	}
}
