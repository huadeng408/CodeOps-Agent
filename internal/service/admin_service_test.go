package service

import (
	"errors"
	"strings"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/pkg/tasks"
)

// replayUploadRepo stubs only GetFileUploadRecordByMD5 — the sole
// UploadRepository method ReplayPipelineTask touches. Other methods stay
// nil-embedded (any stray call panics, which is desirable in a focused test).
type replayUploadRepo struct {
	repository.UploadRepository
	record *model.FileUpload
	err    error
}

func (r *replayUploadRepo) GetFileUploadRecordByMD5(fileMD5 string) (*model.FileUpload, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.record, nil
}

// newReplayService wires an adminService against in-memory fakes. The four
// repositories ReplayPipelineTask never touches are left nil; the recording
// producer captures emitted tasks for assertion.
func newReplayService(t *testing.T, upload *model.FileUpload, docRepo repository.KnowledgeDocumentRepository, produced *[]tasks.FileProcessingTask) AdminService {
	t.Helper()
	producer := func(task tasks.FileProcessingTask) error {
		*produced = append(*produced, task)
		return nil
	}
	uploadRepo := &replayUploadRepo{record: upload}
	return NewAdminService(nil, nil, nil, nil, uploadRepo, docRepo, producer)
}

func replayUploadRecord() *model.FileUpload {
	return &model.FileUpload{
		FileMD5:  "abc123abc123abc123abc123abc12300",
		FileName: "asm.html",
		UserID:   42,
		OrgTag:   "corpus",
		IsPublic: true,
	}
}

func replayCorpusDocument(fileMD5 string) *model.KnowledgeDocument {
	return &model.KnowledgeDocument{
		DocumentID:       "go@" + testCorpusCommit + ":doc/asm.html",
		SourceID:         "go",
		SourcePath:       "doc/asm.html",
		SourceURL:        "https://go.dev/blob/" + testCorpusCommit + "/doc/asm.html",
		SourceCommit:     testCorpusCommit,
		ContentSHA256:    testCorpusContentSHA,
		FileMD5:          fileMD5,
		SourceVersionID:  "go@" + testCorpusCommit + ":techdocs-v1",
		CorpusGeneration: "techdocs-v1",
		TargetIndex:      "knowledge_base_v2_bge_m3",
		// asm.html failed at chunk on a prior run; status is FAILED. Replay of a
		// FAILED document is the load-bearing scenario for run-aware dedup.
		Status: model.DocumentFailed,
	}
}

// TestReplayPipelineTask_CorpusDocumentCarriesProvenance covers spec (a) and
// (e): a corpus document replayed at stage=chunk with runID="r1" emits exactly
// one task carrying RunID, CorpusGeneration, DocumentID and a fully-populated
// Provenance (SourceSHA256 == doc.ContentSHA256, TargetIndex, source identity,
// SourceURL). The document being FAILED must NOT block replay (run semantics).
func TestReplayPipelineTask_CorpusDocumentCarriesProvenance(t *testing.T) {
	upload := replayUploadRecord()
	doc := replayCorpusDocument(upload.FileMD5)
	docRepo := &fakeDocumentRepo{byFileMD5: map[string]*model.KnowledgeDocument{upload.FileMD5: doc}}

	var produced []tasks.FileProcessingTask
	svc := newReplayService(t, upload, docRepo, &produced)

	if err := svc.ReplayPipelineTask(upload.FileMD5, tasks.StageChunk, "r1"); err != nil {
		t.Fatalf("ReplayPipelineTask returned error: %v", err)
	}
	if len(produced) != 1 {
		t.Fatalf("expected producer called exactly once, got %d", len(produced))
	}
	task := produced[0]
	if task.RunID != "r1" {
		t.Fatalf("RunID=%q want r1", task.RunID)
	}
	if task.Stage != tasks.StageChunk {
		t.Fatalf("Stage=%q want chunk", task.Stage)
	}
	if task.FileMD5 != upload.FileMD5 {
		t.Fatalf("FileMD5=%q want %q", task.FileMD5, upload.FileMD5)
	}
	if task.ObjectURL != "" {
		t.Fatalf("ObjectURL=%q want empty (external path presigns on demand)", task.ObjectURL)
	}
	if task.CorpusGeneration != doc.CorpusGeneration {
		t.Fatalf("CorpusGeneration=%q want %q", task.CorpusGeneration, doc.CorpusGeneration)
	}
	if task.DocumentID != doc.DocumentID {
		t.Fatalf("DocumentID=%q want %q", task.DocumentID, doc.DocumentID)
	}
	if task.Provenance == nil {
		t.Fatal("expected non-nil Provenance on corpus replay task")
	}
	p := task.Provenance
	if p.SourceID != doc.SourceID {
		t.Fatalf("Provenance.SourceID=%q want %q", p.SourceID, doc.SourceID)
	}
	if p.SourcePath != doc.SourcePath {
		t.Fatalf("Provenance.SourcePath=%q want %q", p.SourcePath, doc.SourcePath)
	}
	if p.SourceCommit != doc.SourceCommit {
		t.Fatalf("Provenance.SourceCommit=%q want %q", p.SourceCommit, doc.SourceCommit)
	}
	if p.SourceURL != doc.SourceURL {
		t.Fatalf("Provenance.SourceURL=%q want %q", p.SourceURL, doc.SourceURL)
	}
	if p.SourceSHA256 != doc.ContentSHA256 {
		t.Fatalf("Provenance.SourceSHA256=%q want doc.ContentSHA256 %q", p.SourceSHA256, doc.ContentSHA256)
	}
	if p.TargetIndex != doc.TargetIndex {
		t.Fatalf("Provenance.TargetIndex=%q want %q", p.TargetIndex, doc.TargetIndex)
	}
	if p.CorpusGeneration != doc.CorpusGeneration {
		t.Fatalf("Provenance.CorpusGeneration=%q want %q", p.CorpusGeneration, doc.CorpusGeneration)
	}
}

// TestReplayPipelineTask_NonCorpusKeepsLegacyShape covers spec (b): an ordinary
// upload with no knowledge_document row must NOT be promoted into the corpus
// pipeline — the task carries no Provenance/DocumentID/CorpusGeneration. RunID
// is still attached so the consumer bypasses any stale SUCCESS and the replay
// actually runs (the entire purpose of run-aware dedup).
func TestReplayPipelineTask_NonCorpusKeepsLegacyShape(t *testing.T) {
	upload := replayUploadRecord()
	docRepo := &fakeDocumentRepo{} // no byFileMD5 entries → ErrRecordNotFound

	var produced []tasks.FileProcessingTask
	svc := newReplayService(t, upload, docRepo, &produced)

	if err := svc.ReplayPipelineTask(upload.FileMD5, tasks.StageChunk, "r1"); err != nil {
		t.Fatalf("ReplayPipelineTask returned error: %v", err)
	}
	if len(produced) != 1 {
		t.Fatalf("expected producer called once, got %d", len(produced))
	}
	task := produced[0]
	if task.Provenance != nil {
		t.Fatalf("non-corpus replay must not carry provenance, got %+v", task.Provenance)
	}
	if task.DocumentID != "" {
		t.Fatalf("non-corpus DocumentID=%q want empty", task.DocumentID)
	}
	if task.CorpusGeneration != "" {
		t.Fatalf("non-corpus CorpusGeneration=%q want empty (no v2 promotion)", task.CorpusGeneration)
	}
	if task.RunID != "r1" {
		t.Fatalf("RunID=%q want r1 (replay must be run-scoped)", task.RunID)
	}
}

// TestReplayPipelineTask_AutoGeneratesRunID covers spec (c): an empty runID is
// replaced by a server-generated non-empty value (replay-<unixnano>).
func TestReplayPipelineTask_AutoGeneratesRunID(t *testing.T) {
	upload := replayUploadRecord()
	docRepo := &fakeDocumentRepo{}

	var produced []tasks.FileProcessingTask
	svc := newReplayService(t, upload, docRepo, &produced)

	if err := svc.ReplayPipelineTask(upload.FileMD5, "", ""); err != nil {
		t.Fatalf("ReplayPipelineTask returned error: %v", err)
	}
	task := produced[0]
	if !strings.HasPrefix(task.RunID, "replay-") {
		t.Fatalf("RunID=%q want prefix replay-", task.RunID)
	}
	if task.RunID == "replay-" {
		t.Fatalf("RunID=%q must carry a non-empty suffix after replay-", task.RunID)
	}
	// Empty stage defaults to parse (existing behavior preserved).
	if task.Stage != tasks.StageParse {
		t.Fatalf("Stage=%q want parse (default)", task.Stage)
	}
}

// TestReplayPipelineTask_UnknownStageErrors covers spec (d): an unsupported
// stage is rejected with no produce.
func TestReplayPipelineTask_UnknownStageErrors(t *testing.T) {
	upload := replayUploadRecord()
	docRepo := &fakeDocumentRepo{}

	var produced []tasks.FileProcessingTask
	svc := newReplayService(t, upload, docRepo, &produced)

	err := svc.ReplayPipelineTask(upload.FileMD5, tasks.Stage("nonsense"), "r1")
	if err == nil {
		t.Fatal("expected error for unknown stage, got nil")
	}
	if len(produced) != 0 {
		t.Fatalf("expected zero produces on validation failure, got %d", len(produced))
	}
}

// TestReplayPipelineTask_EmptyFileMD5Errors asserts the existing fileMd5
// validation still rejects an empty identifier before any lookup.
func TestReplayPipelineTask_EmptyFileMD5Errors(t *testing.T) {
	docRepo := &fakeDocumentRepo{}
	var produced []tasks.FileProcessingTask
	svc := newReplayService(t, nil, docRepo, &produced)

	if err := svc.ReplayPipelineTask("   ", tasks.StageChunk, "r1"); err == nil {
		t.Fatal("expected error for empty fileMd5, got nil")
	}
	if len(produced) != 0 {
		t.Fatalf("expected zero produces on empty fileMd5, got %d", len(produced))
	}
}

// TestReplayPipelineTask_ProducerErrorPropagates enforces Global Constraint #4:
// a Kafka produce failure must surface to the caller rather than being swallowed.
func TestReplayPipelineTask_ProducerErrorPropagates(t *testing.T) {
	upload := replayUploadRecord()
	docRepo := &fakeDocumentRepo{}
	producerErr := errors.New("kafka broker unavailable")
	producer := func(tasks.FileProcessingTask) error { return producerErr }
	svc := NewAdminService(nil, nil, nil, nil, &replayUploadRepo{record: upload}, docRepo, producer)

	if err := svc.ReplayPipelineTask(upload.FileMD5, tasks.StageParse, "r1"); !errors.Is(err, producerErr) {
		t.Fatalf("expected producer error to propagate, got %v", err)
	}
}
