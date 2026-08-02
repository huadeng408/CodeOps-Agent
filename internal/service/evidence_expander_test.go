package service

import (
	"context"
	"testing"

	"code-agent/internal/model"
)

// fakeVectorStore is an in-memory DocumentVectorRepository exposing
// parent/neighbor lookup for evidence expansion tests.
type fakeVectorStore struct {
	vectors []*model.DocumentVector
}

func (f *fakeVectorStore) FindByParentChunkID(parentChunkID string) ([]*model.DocumentVector, error) {
	var out []*model.DocumentVector
	for _, v := range f.vectors {
		if v.ParentChunkID == parentChunkID {
			out = append(out, v)
		}
	}
	return out, nil
}

func (f *fakeVectorStore) FindByFileMD5(fileMD5 string) ([]*model.DocumentVector, error) {
	var out []*model.DocumentVector
	for _, v := range f.vectors {
		if v.FileMD5 == fileMD5 {
			out = append(out, v)
		}
	}
	return out, nil
}

func (f *fakeVectorStore) FindByFileMD5Range(fileMD5 string, offset, limit int) ([]*model.DocumentVector, error) {
	return f.FindByFileMD5(fileMD5)
}

func (f *fakeVectorStore) CountByFileMD5(fileMD5 string) (int64, error) {
	var n int64
	for _, v := range f.vectors {
		if v.FileMD5 == fileMD5 {
			n++
		}
	}
	return n, nil
}

func (f *fakeVectorStore) BatchCreate([]*model.DocumentVector) error { return nil }
func (f *fakeVectorStore) DeleteByFileMD5(fileMD5 string) error      { return nil }

func evVector(chunkID int, parent, page string, userID uint, orgTag string, text string, tokens int) *model.DocumentVector {
	return &model.DocumentVector{
		ChunkID:       chunkID,
		ParentChunkID: parent,
		PageID:        page,
		UserID:        userID,
		OrgTag:        orgTag,
		TextContent:   text,
		TokenCount:    tokens,
		DocumentID:    "doc-1",
		ElementIDs:    []string{"e" + itoa(chunkID)},
		ElementTypes:  []string{"text"},
		BBoxRefs:      []string{"e" + itoa(chunkID) + ":0,0,1,1"},
		CorpusGeneration: "techdocs-2026-07-30-v1",
	}
}

func TestEvidenceExpanderLoadsParentAndNeighbors(t *testing.T) {
	store := &fakeVectorStore{vectors: []*model.DocumentVector{
		evVector(1, "parent-1", "p1", 7, "research", "child text", 20),
		evVector(2, "parent-1", "p1", 7, "research", "neighbor text", 30),
		evVector(3, "parent-2", "p2", 7, "research", "unrelated", 10),
	}}
	expander := NewEvidenceExpander(store, 7, []string{"research"})

	hits := []model.DocumentVector{*evVector(1, "parent-1", "p1", 7, "research", "child text", 20)}
	expanded, err := expander.Expand(context.Background(), hits, 1000)
	if err != nil {
		t.Fatal(err)
	}
	// child + sibling neighbor from the same parent
	if len(expanded) < 2 {
		t.Fatalf("expected child + neighbor, got %d: %#v", len(expanded), expanded)
	}
	foundNeighbor := false
	for _, e := range expanded {
		if e.Source.ChunkID == 2 {
			foundNeighbor = true
		}
		if e.Source.ChunkID == 3 {
			t.Fatalf("unrelated parent chunk leaked into evidence")
		}
	}
	if !foundNeighbor {
		t.Fatalf("neighbor chunk missing: %#v", expanded)
	}
	if expanded[0].ExpansionStatus != "full" {
		t.Fatalf("expected full expansion, got %s", expanded[0].ExpansionStatus)
	}
}

func TestEvidenceExpanderRejectsCrossTenantNeighbor(t *testing.T) {
	store := &fakeVectorStore{vectors: []*model.DocumentVector{
		evVector(1, "parent-1", "p1", 7, "research", "child text", 20),
		evVector(2, "parent-1", "p1", 999, "attacker-org", "other tenant", 30),
	}}
	expander := NewEvidenceExpander(store, 7, []string{"research"})

	hits := []model.DocumentVector{*evVector(1, "parent-1", "p1", 7, "research", "child text", 20)}
	expanded, err := expander.Expand(context.Background(), hits, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range expanded {
		if e.Source.UserID == 999 {
			t.Fatalf("cross-tenant neighbor leaked: %#v", e.Source)
		}
	}
}

func TestEvidenceExpanderPartialWhenParentMissing(t *testing.T) {
	store := &fakeVectorStore{vectors: []*model.DocumentVector{
		// The child claims a parent chunk that does not exist in the store.
		evVector(1, "ghost-parent", "p1", 7, "research", "orphan child", 20),
	}}
	expander := NewEvidenceExpander(store, 7, []string{"research"})

	hits := []model.DocumentVector{*evVector(1, "ghost-parent", "p1", 7, "research", "orphan child", 20)}
	expanded, err := expander.Expand(context.Background(), hits, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(expanded) != 1 {
		t.Fatalf("expected orphan child only, got %d", len(expanded))
	}
	if expanded[0].ExpansionStatus != "partial" {
		t.Fatalf("expected partial status, got %s", expanded[0].ExpansionStatus)
	}
}

func TestEvidenceExpanderDeduplicatesAndRespectsTokenBudget(t *testing.T) {
	store := &fakeVectorStore{vectors: []*model.DocumentVector{
		evVector(1, "parent-1", "p1", 7, "research", "child", 50),
		evVector(2, "parent-1", "p1", 7, "research", "same page sibling", 60),
		evVector(3, "parent-1", "p1", 7, "research", "second sibling", 60),
	}}
	expander := NewEvidenceExpander(store, 7, []string{"research"})

	// Budget 80 tokens constrains NEIGHBOR loading only: the hit itself is
	// always kept. child(50) + first sibling(60) = 110 > 80 => the second
	// sibling is skipped, so evidence = child + 1 neighbor.
	hits := []model.DocumentVector{*evVector(1, "parent-1", "p1", 7, "research", "child", 50)}
	expanded, err := expander.Expand(context.Background(), hits, 80)
	if err != nil {
		t.Fatal(err)
	}
	if len(expanded) != 2 {
		t.Fatalf("expected hit + 1 neighbor within budget, got %d: %#v", len(expanded), expanded)
	}
	for _, e := range expanded {
		if e.Source.ChunkID == 3 {
			t.Fatalf("second sibling exceeded token budget: %#v", e.Source)
		}
	}
}
