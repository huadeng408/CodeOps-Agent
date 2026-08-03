package repository

import (
	"errors"
	"testing"
	"time"

	"code-agent/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newKnowledgeDocumentDB opens an in-memory sqlite database (pure-Go via
// modernc) and auto-migrates the knowledge_document table. The suite is
// hermetic: it never touches MySQL, mirroring the pipeline_task_repository
// test harness so GetDocumentByFileMD5 can be exercised in CI/RED phase.
func newKnowledgeDocumentDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.KnowledgeDocument{}); err != nil {
		t.Fatalf("auto-migrate knowledge_document: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

const (
	replayTestCommit     = "abcdef0123456789abcdef0123456789abcdef01"
	replayTestContentSHA = "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
)

func seedDocument(t *testing.T, db *gorm.DB, doc *model.KnowledgeDocument) {
	t.Helper()
	if err := db.Create(doc).Error; err != nil {
		t.Fatalf("seed document: %v", err)
	}
}

// setUpdatedAt forces the updated_at column without triggering gorm's
// autoUpdateTime, so tests can deterministically model "older/newer versions".
func setUpdatedAt(t *testing.T, db *gorm.DB, documentID string, ts time.Time) {
	t.Helper()
	if err := db.Model(&model.KnowledgeDocument{}).
		Where("document_id = ?", documentID).
		UpdateColumn("updated_at", ts).Error; err != nil {
		t.Fatalf("set updated_at for %s: %v", documentID, err)
	}
}

// TestGetDocumentByFileMD5_ReturnsRow asserts a single matching row is found.
func TestGetDocumentByFileMD5_ReturnsRow(t *testing.T) {
	db := newKnowledgeDocumentDB(t)
	repo := NewKnowledgeDocumentRepository(db)
	seedDocument(t, db, &model.KnowledgeDocument{
		DocumentID:       "go@" + replayTestCommit + ":doc/asm.html",
		SourceID:         "go",
		SourcePath:       "doc/asm.html",
		SourceCommit:     replayTestCommit,
		DocumentLanguage: "en",
		ContentSHA256:    replayTestContentSHA,
		FileMD5:          "md5asm000000000000000000000000",
		SourceVersionID:  "go@" + replayTestCommit + ":techdocs-v1",
		CorpusGeneration: "techdocs-v1",
		TargetIndex:      "knowledge_base_v2_bge_m3",
		Status:           model.DocumentActive,
	})

	got, err := repo.GetDocumentByFileMD5("md5asm000000000000000000000000")
	if err != nil {
		t.Fatalf("GetDocumentByFileMD5: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil document")
	}
	if got.ContentSHA256 != replayTestContentSHA {
		t.Fatalf("contentSha256=%q want %q", got.ContentSHA256, replayTestContentSHA)
	}
	if got.CorpusGeneration != "techdocs-v1" {
		t.Fatalf("corpusGeneration=%q want techdocs-v1", got.CorpusGeneration)
	}
}

// TestGetDocumentByFileMD5_MissingReturnsNotFound asserts a missing file_md5
// surfaces gorm.ErrRecordNotFound so callers can treat it as "not corpus".
func TestGetDocumentByFileMD5_MissingReturnsNotFound(t *testing.T) {
	db := newKnowledgeDocumentDB(t)
	repo := NewKnowledgeDocumentRepository(db)

	_, err := repo.GetDocumentByFileMD5("nonexistent00000000000000000000")
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected gorm.ErrRecordNotFound, got %v", err)
	}
}

// TestGetDocumentByFileMD5_PrefersNonFailedOverNewerFailed is the load-bearing
// ordering test for content-changing documents (R7): when a file_md5 has both a
// newer FAILED version (e.g. a re-ingest of asm.html that failed at chunk) and
// an older non-FAILED version, the repo must return the usable non-FAILED row.
// Recency never beats usability.
func TestGetDocumentByFileMD5_PrefersNonFailedOverNewerFailed(t *testing.T) {
	db := newKnowledgeDocumentDB(t)
	repo := NewKnowledgeDocumentRepository(db)
	const fileMD5 = "md5multi00000000000000000000000"

	failed := &model.KnowledgeDocument{
		DocumentID:       "go@" + replayTestCommit + ":doc/asm.html#v2",
		SourceID:         "go",
		SourcePath:       "doc/asm.html",
		SourceCommit:     replayTestCommit,
		DocumentLanguage: "en",
		ContentSHA256:    "9999999999999999999999999999999999999999999999999999999999999999",
		FileMD5:          fileMD5,
		SourceVersionID:  "go@" + replayTestCommit + ":techdocs-v1",
		CorpusGeneration: "techdocs-v1",
		TargetIndex:      "knowledge_base_v2_bge_m3",
		Status:           model.DocumentFailed,
	}
	active := &model.KnowledgeDocument{
		DocumentID:       "go@" + replayTestCommit + ":doc/asm.html#v1",
		SourceID:         "go",
		SourcePath:       "doc/asm.html",
		SourceCommit:     replayTestCommit,
		DocumentLanguage: "en",
		ContentSHA256:    replayTestContentSHA,
		FileMD5:          fileMD5,
		SourceVersionID:  "go@" + replayTestCommit + ":techdocs-v1",
		CorpusGeneration: "techdocs-v1",
		TargetIndex:      "knowledge_base_v2_bge_m3",
		Status:           model.DocumentActive,
	}
	seedDocument(t, db, active)
	seedDocument(t, db, failed)
	// FAILED is the newer row; usability must still win.
	setUpdatedAt(t, db, active.DocumentID, time.Unix(1000, 0))
	setUpdatedAt(t, db, failed.DocumentID, time.Unix(2000, 0))

	got, err := repo.GetDocumentByFileMD5(fileMD5)
	if err != nil {
		t.Fatalf("GetDocumentByFileMD5: %v", err)
	}
	if got.Status != model.DocumentActive {
		t.Fatalf("status=%q want ACTIVE (non-FAILED must win over newer FAILED)", got.Status)
	}
	if got.DocumentID != active.DocumentID {
		t.Fatalf("documentId=%q want %q", got.DocumentID, active.DocumentID)
	}
}

// TestGetDocumentByFileMD5_AllFailedReturnsLatest asserts that when every
// version is FAILED, the most recently updated one is returned (best-effort
// recovery target for a controlled replay).
func TestGetDocumentByFileMD5_AllFailedReturnsLatest(t *testing.T) {
	db := newKnowledgeDocumentDB(t)
	repo := NewKnowledgeDocumentRepository(db)
	const fileMD5 = "md5fail000000000000000000000000"

	older := &model.KnowledgeDocument{
		DocumentID:       "go@" + replayTestCommit + ":doc/x.md#old",
		SourceID:         "go", SourcePath: "doc/x.md", SourceCommit: replayTestCommit,
		DocumentLanguage: "en", ContentSHA256: "1111111111111111111111111111111111111111111111111111111111111111",
		FileMD5: fileMD5, SourceVersionID: "go@" + replayTestCommit + ":techdocs-v1",
		CorpusGeneration: "techdocs-v1", TargetIndex: "knowledge_base_v2_bge_m3", Status: model.DocumentFailed,
	}
	newer := &model.KnowledgeDocument{
		DocumentID:       "go@" + replayTestCommit + ":doc/x.md#new",
		SourceID:         "go", SourcePath: "doc/x.md", SourceCommit: replayTestCommit,
		DocumentLanguage: "en", ContentSHA256: "2222222222222222222222222222222222222222222222222222222222222222",
		FileMD5: fileMD5, SourceVersionID: "go@" + replayTestCommit + ":techdocs-v1",
		CorpusGeneration: "techdocs-v1", TargetIndex: "knowledge_base_v2_bge_m3", Status: model.DocumentFailed,
	}
	seedDocument(t, db, older)
	seedDocument(t, db, newer)
	setUpdatedAt(t, db, older.DocumentID, time.Unix(1000, 0))
	setUpdatedAt(t, db, newer.DocumentID, time.Unix(2000, 0))

	got, err := repo.GetDocumentByFileMD5(fileMD5)
	if err != nil {
		t.Fatalf("GetDocumentByFileMD5: %v", err)
	}
	if got.DocumentID != newer.DocumentID {
		t.Fatalf("documentId=%q want %q (latest FAILED wins when all FAILED)", got.DocumentID, newer.DocumentID)
	}
}

// TestListDocumentsByGenerationAndStatus_FiltersByGenerationAndStatus asserts the
// read-only query used by the importer polling endpoint returns only rows whose
// corpus_generation matches AND status is in the requested whitelist, ordered by
// document_id for deterministic enumeration.
func TestListDocumentsByGenerationAndStatus_FiltersByGenerationAndStatus(t *testing.T) {
	db := newKnowledgeDocumentDB(t)
	repo := NewKnowledgeDocumentRepository(db)
	seedDocument(t, db, &model.KnowledgeDocument{
		DocumentID: "go@" + replayTestCommit + ":doc/active-a.html", SourceID: "go",
		SourcePath: "doc/active-a.html", SourceCommit: replayTestCommit, DocumentLanguage: "en",
		ContentSHA256: "aaaa000000000000000000000000000000000000000000000000000000000000", FileMD5: "md5a000000000000000000000000",
		SourceVersionID: "go@" + replayTestCommit + ":techdocs-v1", CorpusGeneration: "techdocs-v1",
		TargetIndex: "knowledge_base_v2_bge_m3", Status: model.DocumentActive,
	})
	seedDocument(t, db, &model.KnowledgeDocument{
		DocumentID: "go@" + replayTestCommit + ":doc/active-b.html", SourceID: "go",
		SourcePath: "doc/active-b.html", SourceCommit: replayTestCommit, DocumentLanguage: "en",
		ContentSHA256: "bbbb000000000000000000000000000000000000000000000000000000000000", FileMD5: "md5b000000000000000000000000",
		SourceVersionID: "go@" + replayTestCommit + ":techdocs-v1", CorpusGeneration: "techdocs-v1",
		TargetIndex: "knowledge_base_v2_bge_m3", Status: model.DocumentActive,
	})
	// FAILED row in the SAME generation must be excluded by an ACTIVE filter.
	seedDocument(t, db, &model.KnowledgeDocument{
		DocumentID: "go@" + replayTestCommit + ":doc/failed.html", SourceID: "go",
		SourcePath: "doc/failed.html", SourceCommit: replayTestCommit, DocumentLanguage: "en",
		ContentSHA256: "cccc000000000000000000000000000000000000000000000000000000000000", FileMD5: "md5c000000000000000000000000",
		SourceVersionID: "go@" + replayTestCommit + ":techdocs-v1", CorpusGeneration: "techdocs-v1",
		TargetIndex: "knowledge_base_v2_bge_m3", Status: model.DocumentFailed,
	})
	// ACTIVE row in a DIFFERENT generation must be excluded by the generation filter.
	seedDocument(t, db, &model.KnowledgeDocument{
		DocumentID: "go@" + replayTestCommit + ":doc/other.html", SourceID: "go",
		SourcePath: "doc/other.html", SourceCommit: replayTestCommit, DocumentLanguage: "en",
		ContentSHA256: "dddd000000000000000000000000000000000000000000000000000000000000", FileMD5: "md5d000000000000000000000000",
		SourceVersionID: "go@" + replayTestCommit + ":techdocs-v1", CorpusGeneration: "techdocs-DIFFERENT",
		TargetIndex: "knowledge_base_v2_bge_m3", Status: model.DocumentActive,
	})

	got, err := repo.ListDocumentsByGenerationAndStatus("techdocs-v1", []string{string(model.DocumentActive)})
	if err != nil {
		t.Fatalf("ListDocumentsByGenerationAndStatus: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("document count = %d, want 2 (only ACTIVE rows in techdocs-v1)", len(got))
	}
	// Ordered by document_id asc: active-a precedes active-b.
	if got[0].DocumentID != "go@"+replayTestCommit+":doc/active-a.html" {
		t.Errorf("first document_id = %q, want active-a (ordered by document_id)", got[0].DocumentID)
	}
	if got[1].DocumentID != "go@"+replayTestCommit+":doc/active-b.html" {
		t.Errorf("second document_id = %q, want active-b", got[1].DocumentID)
	}
	for _, d := range got {
		if d.Status != model.DocumentActive {
			t.Errorf("returned non-ACTIVE row: %+v", d)
		}
		if d.CorpusGeneration != "techdocs-v1" {
			t.Errorf("returned wrong-generation row: %+v", d)
		}
	}
}

// TestListDocumentsByGenerationAndStatus_AcceptsMultipleStatuses asserts the
// whitelist accepts more than one status (e.g. STAGED + FAILED for recovery views).
func TestListDocumentsByGenerationAndStatus_AcceptsMultipleStatuses(t *testing.T) {
	db := newKnowledgeDocumentDB(t)
	repo := NewKnowledgeDocumentRepository(db)
	seedDocument(t, db, &model.KnowledgeDocument{
		DocumentID: "go@" + replayTestCommit + ":doc/staged.html", SourceID: "go",
		SourcePath: "doc/staged.html", SourceCommit: replayTestCommit, DocumentLanguage: "en",
		ContentSHA256: "eeee000000000000000000000000000000000000000000000000000000000000", FileMD5: "md5e000000000000000000000000",
		SourceVersionID: "go@" + replayTestCommit + ":techdocs-v1", CorpusGeneration: "techdocs-v1",
		TargetIndex: "knowledge_base_v2_bge_m3", Status: model.DocumentStaged,
	})
	seedDocument(t, db, &model.KnowledgeDocument{
		DocumentID: "go@" + replayTestCommit + ":doc/down.html", SourceID: "go",
		SourcePath: "doc/down.html", SourceCommit: replayTestCommit, DocumentLanguage: "en",
		ContentSHA256: "ffff000000000000000000000000000000000000000000000000000000000000", FileMD5: "md5f000000000000000000000000",
		SourceVersionID: "go@" + replayTestCommit + ":techdocs-v1", CorpusGeneration: "techdocs-v1",
		TargetIndex: "knowledge_base_v2_bge_m3", Status: model.DocumentFailed,
	})

	got, err := repo.ListDocumentsByGenerationAndStatus("techdocs-v1", []string{
		string(model.DocumentStaged), string(model.DocumentFailed),
	})
	if err != nil {
		t.Fatalf("ListDocumentsByGenerationAndStatus: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("document count = %d, want 2 (STAGED + FAILED)", len(got))
	}
}

// TestListDocumentsByGenerationAndStatus_EmptyResultReturnsNoError asserts a
// generation with no matching rows returns an empty slice (not an error), so the
// polling endpoint can treat "no documents yet" as a normal empty list.
func TestListDocumentsByGenerationAndStatus_EmptyResultReturnsNoError(t *testing.T) {
	db := newKnowledgeDocumentDB(t)
	repo := NewKnowledgeDocumentRepository(db)

	got, err := repo.ListDocumentsByGenerationAndStatus("techdocs-v1", []string{string(model.DocumentActive)})
	if err != nil {
		t.Fatalf("ListDocumentsByGenerationAndStatus on empty table: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected empty non-nil slice, got %v", got)
	}
}
