package session

import (
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	fdiff "github.com/go-git/go-git/v5/plumbing/format/diff"
	gitdiff "github.com/go-git/go-git/v5/utils/diff"
)

type proposalPatch struct{ file proposalFilePatch }

func (p proposalPatch) Message() string                { return "" }
func (p proposalPatch) FilePatches() []fdiff.FilePatch { return []fdiff.FilePatch{p.file} }

type proposalFilePatch struct {
	from, to fdiff.File
	chunks   []fdiff.Chunk
}

func (p proposalFilePatch) IsBinary() bool                  { return false }
func (p proposalFilePatch) Files() (fdiff.File, fdiff.File) { return p.from, p.to }
func (p proposalFilePatch) Chunks() []fdiff.Chunk           { return p.chunks }

type proposalDiffFile struct {
	path, content string
	mode          uint32
}

func (f proposalDiffFile) Path() string { return f.path }
func (f proposalDiffFile) Hash() plumbing.Hash {
	return plumbing.ComputeHash(plumbing.BlobObject, []byte(f.content))
}
func (f proposalDiffFile) Mode() filemode.FileMode {
	if f.mode&0111 != 0 {
		return filemode.Executable
	}
	return filemode.Regular
}

type proposalChunk struct {
	text      string
	operation fdiff.Operation
}

func (c proposalChunk) Content() string       { return c.text }
func (c proposalChunk) Type() fdiff.Operation { return c.operation }

func proposalDiff(file ProposalFileView, before, after string) string {
	patch := proposalFilePatch{}
	if file.BeforeExists {
		patch.from = proposalDiffFile{file.Path, before, file.BeforeMode}
	}
	if file.AfterExists {
		patch.to = proposalDiffFile{file.Path, after, file.AfterMode}
	}
	// shortcut: large diffs replace the whole file; add a bounded minimal-diff algorithm if review size requires it.
	if (int64(strings.Count(before, "\n"))+1)*(int64(strings.Count(after, "\n"))+1) > 1_000_000 {
		patch.chunks = []fdiff.Chunk{proposalChunk{before, fdiff.Delete}, proposalChunk{after, fdiff.Add}}
	} else {
		for _, change := range gitdiff.DoWithTimeout(before, after, 0) {
			op := fdiff.Equal
			if change.Type < 0 {
				op = fdiff.Delete
			} else if change.Type > 0 {
				op = fdiff.Add
			}
			patch.chunks = append(patch.chunks, proposalChunk{change.Text, op})
		}
	}
	var text strings.Builder
	_ = fdiff.NewUnifiedEncoder(&text, fdiff.DefaultContextLines).Encode(proposalPatch{patch})
	return text.String()
}
