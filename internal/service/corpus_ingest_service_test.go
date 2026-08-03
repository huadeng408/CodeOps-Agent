package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/tasks"

	"gorm.io/gorm"
)

// fakeSourceRepo is an in-memory KnowledgeSourceRepository for corpus tests.
type fakeSourceRepo struct {
	creates []*model.KnowledgeSource
	err     error
}

func (f *fakeSourceRepo) CreateOrGetSource(source *model.KnowledgeSource) (*model.KnowledgeSource, error) {
	if f.err != nil {
		return nil, f.err
	}
	cp := *source
	f.creates = append(f.creates, &cp)
	return source, nil
}

// fakeDocumentRepo mirrors the idempotent semantics of the real document repo.
type fakeDocumentRepo struct {
	documents map[string]*model.KnowledgeDocument
	// byFileMD5 backs GetDocumentByFileMD5 lookups (admin replay path). Empty
	// by default so corpus ingest tests, which never query by file_md5, observe
	// the same "not found" behavior as a real repo with no matching row.
	byFileMD5 map[string]*model.KnowledgeDocument
	creates   []*model.KnowledgeDocument
	markCalls []fakeDocMarkCall
	err       error
}

type fakeDocMarkCall struct {
	documentID string
	status     model.KnowledgeDocumentStatus
	lastError  string
}

func (f *fakeDocumentRepo) CreateOrGetDocument(document *model.KnowledgeDocument) (*model.KnowledgeDocument, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.documents == nil {
		f.documents = make(map[string]*model.KnowledgeDocument)
	}
	if existing, ok := f.documents[document.DocumentID]; ok {
		if existing.ContentSHA256 == document.ContentSHA256 {
			return existing, nil
		}
		document.DocumentID = document.DocumentID + "#" + document.ContentSHA256
	}
	stored := *document
	f.documents[stored.DocumentID] = &stored
	f.creates = append(f.creates, &stored)
	return &stored, nil
}

func (f *fakeDocumentRepo) MarkDocumentStatus(documentID string, status model.KnowledgeDocumentStatus, lastError string) error {
	f.markCalls = append(f.markCalls, fakeDocMarkCall{documentID: documentID, status: status, lastError: lastError})
	return nil
}

func (f *fakeDocumentRepo) ListDocumentsByGeneration(generation string) ([]model.KnowledgeDocument, error) {
	return nil, nil
}

func (f *fakeDocumentRepo) ListDocumentsByGenerationAndStatus(string, []string) ([]model.KnowledgeDocument, error) {
	return nil, nil
}

func (f *fakeDocumentRepo) CountActiveDocuments(generation string) (int64, error) {
	return 0, nil
}

// GetDocumentByFileMD5 mirrors the real repository: returns the configured row
// for a file_md5, or gorm.ErrRecordNotFound when none is present so callers can
// treat a missing file_md5 as "not a corpus document".
func (f *fakeDocumentRepo) GetDocumentByFileMD5(fileMD5 string) (*model.KnowledgeDocument, error) {
	if f.err != nil {
		return nil, f.err
	}
	if doc, ok := f.byFileMD5[fileMD5]; ok {
		return doc, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func newTestCorpusConfig() serverconfig.CorpusConfig {
	return serverconfig.CorpusConfig{
		Generation: "techdocs-test-v1",
		TextIndex:  "knowledge_base_test_idx",
		LoaderUser: 42,
	}
}

const (
	testCorpusCommit     = "abcdef0123456789abcdef0123456789abcdef01"                         // 40 lowercase hex
	testCorpusContentSHA = "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef" // 64 lowercase hex
)

func newValidCorpusRequest() CorpusIngestRequest {
	prov := model.CorpusProvenance{
		SourceID:         "go",
		SourcePath:       "doc/asm.html",
		SourceURL:        "https://example.test/go/blob/abc/doc/asm.html",
		SourceCommit:     testCorpusCommit,
		SourceSHA256:     testCorpusContentSHA,
		TargetIndex:      "knowledge_base_test_idx",
		CorpusGeneration: "techdocs-test-v1",
	}
	return CorpusIngestRequest{
		UserID:        42,
		OrgTag:        "corpus",
		IsPublic:      true,
		FileMD5:       "deadbeefdeadbeefdeadbeefdeadbeef",
		FileName:      "asm.html",
		TotalSize:     2048,
		ObjectURL:     "https://minio.test/merged/asm.html",
		ContentSHA256: testCorpusContentSHA,
		RunID:         "import-1730000000",
		Provenance:    prov,
	}
}

func TestCorpusIngestService_ValidRequest(t *testing.T) {
	sourceRepo := &fakeSourceRepo{}
	docRepo := &fakeDocumentRepo{}
	cfg := newTestCorpusConfig()

	var produced []tasks.FileProcessingTask
	producer := func(task tasks.FileProcessingTask) error {
		produced = append(produced, task)
		return nil
	}

	svc := NewCorpusIngestService(sourceRepo, docRepo, cfg, producer)
	req := newValidCorpusRequest()

	res, err := svc.Ingest(context.Background(), req)
	if err != nil {
		t.Fatalf("Ingest returned unexpected error: %v", err)
	}

	if len(sourceRepo.creates) != 1 {
		t.Fatalf("expected 1 source row, got %d", len(sourceRepo.creates))
	}
	if sourceRepo.creates[0].Status != model.SourceStaged {
		t.Errorf("expected source STAGED, got %s", sourceRepo.creates[0].Status)
	}
	if sourceRepo.creates[0].CorpusGeneration != cfg.Generation {
		t.Errorf("expected source generation %q, got %q", cfg.Generation, sourceRepo.creates[0].CorpusGeneration)
	}
	if sourceRepo.creates[0].SourceVersionID == "" {
		t.Error("expected non-empty source_version_id")
	}

	if len(docRepo.creates) != 1 {
		t.Fatalf("expected 1 document row, got %d", len(docRepo.creates))
	}
	doc := docRepo.creates[0]
	if doc.Status != model.DocumentStaged {
		t.Errorf("expected document STAGED, got %s", doc.Status)
	}
	if doc.TargetIndex != cfg.TextIndex {
		t.Errorf("expected target_index %q, got %q", cfg.TextIndex, doc.TargetIndex)
	}
	if doc.ContentSHA256 != req.ContentSHA256 {
		t.Errorf("expected content_sha256 %q, got %q", req.ContentSHA256, doc.ContentSHA256)
	}
	if doc.FileMD5 != req.FileMD5 {
		t.Errorf("expected file_md5 %q, got %q", req.FileMD5, doc.FileMD5)
	}
	if doc.SourceVersionID != sourceRepo.creates[0].SourceVersionID {
		t.Errorf("document source_version_id %q != source %q", doc.SourceVersionID, sourceRepo.creates[0].SourceVersionID)
	}

	if len(produced) != 1 {
		t.Fatalf("expected exactly 1 produce, got %d", len(produced))
	}
	task := produced[0]
	if task.Stage != tasks.StageParse {
		t.Errorf("expected stage %q, got %q", tasks.StageParse, task.Stage)
	}
	if task.CorpusGeneration != cfg.Generation {
		t.Errorf("expected task corpus_generation %q, got %q", cfg.Generation, task.CorpusGeneration)
	}
	if task.UserID != cfg.LoaderUser {
		t.Errorf("expected task user_id %d, got %d", cfg.LoaderUser, task.UserID)
	}
	if task.DocumentID != doc.DocumentID {
		t.Errorf("expected task document_id %q, got %q", doc.DocumentID, task.DocumentID)
	}
	if task.Provenance == nil {
		t.Fatal("expected non-nil provenance on task")
	}
	if task.Provenance.SourceSHA256 != req.ContentSHA256 {
		t.Errorf("expected task provenance source_sha256 %q, got %q", req.ContentSHA256, task.Provenance.SourceSHA256)
	}
	if task.RunID != req.RunID {
		t.Errorf("expected task run_id %q, got %q", req.RunID, task.RunID)
	}

	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.DocumentID != doc.DocumentID {
		t.Errorf("expected result document_id %q, got %q", doc.DocumentID, res.DocumentID)
	}
}

func TestCorpusIngestService_UserIDMismatch(t *testing.T) {
	sourceRepo := &fakeSourceRepo{}
	docRepo := &fakeDocumentRepo{}
	cfg := newTestCorpusConfig()

	var produced []tasks.FileProcessingTask
	producer := func(task tasks.FileProcessingTask) error {
		produced = append(produced, task)
		return nil
	}

	svc := NewCorpusIngestService(sourceRepo, docRepo, cfg, producer)
	req := newValidCorpusRequest()
	req.UserID = cfg.LoaderUser + 1

	if _, err := svc.Ingest(context.Background(), req); !errors.Is(err, ErrCorpusValidation) {
		t.Fatalf("expected ErrCorpusValidation, got %v", err)
	}
	if len(sourceRepo.creates) != 0 || len(docRepo.creates) != 0 || len(produced) != 0 {
		t.Fatalf("expected zero persistence/produce on validation failure: sources=%d docs=%d produces=%d",
			len(sourceRepo.creates), len(docRepo.creates), len(produced))
	}
}

func TestCorpusIngestService_FieldValidation(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(req *CorpusIngestRequest)
	}{
		{
			name: "generation_mismatch",
			mutate: func(r *CorpusIngestRequest) {
				r.Provenance.CorpusGeneration = "other-gen"
			},
		},
		{
			name: "target_index_mismatch",
			mutate: func(r *CorpusIngestRequest) {
				r.Provenance.TargetIndex = "other-idx"
			},
		},
		{
			name: "content_sha256_mismatch",
			mutate: func(r *CorpusIngestRequest) {
				r.ContentSHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
			},
		},
		{
			name: "content_sha256_empty",
			mutate: func(r *CorpusIngestRequest) {
				r.ContentSHA256 = ""
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sourceRepo := &fakeSourceRepo{}
			docRepo := &fakeDocumentRepo{}
			cfg := newTestCorpusConfig()

			var produced []tasks.FileProcessingTask
			producer := func(task tasks.FileProcessingTask) error {
				produced = append(produced, task)
				return nil
			}

			svc := NewCorpusIngestService(sourceRepo, docRepo, cfg, producer)
			req := newValidCorpusRequest()
			tc.mutate(&req)

			if _, err := svc.Ingest(context.Background(), req); !errors.Is(err, ErrCorpusValidation) {
				t.Fatalf("expected ErrCorpusValidation, got %v", err)
			}
			if len(sourceRepo.creates) != 0 || len(docRepo.creates) != 0 || len(produced) != 0 {
				t.Fatalf("expected zero persistence/produce on validation failure: sources=%d docs=%d produces=%d",
					len(sourceRepo.creates), len(docRepo.creates), len(produced))
			}
		})
	}
}

func TestCorpusIngestService_ProducerError(t *testing.T) {
	sourceRepo := &fakeSourceRepo{}
	docRepo := &fakeDocumentRepo{}
	cfg := newTestCorpusConfig()

	producerErr := errors.New("kafka broker unavailable")
	producer := func(task tasks.FileProcessingTask) error {
		return producerErr
	}

	svc := NewCorpusIngestService(sourceRepo, docRepo, cfg, producer)
	req := newValidCorpusRequest()

	if _, err := svc.Ingest(context.Background(), req); !errors.Is(err, producerErr) {
		t.Fatalf("expected producer error to propagate, got %v", err)
	}
}

func TestCorpusIngestService_DocumentCreateFailure(t *testing.T) {
	sourceRepo := &fakeSourceRepo{}
	docRepo := &fakeDocumentRepo{}
	cfg := newTestCorpusConfig()

	var produced []tasks.FileProcessingTask
	producer := func(task tasks.FileProcessingTask) error {
		produced = append(produced, task)
		return nil
	}

	docErr := errors.New("db connection lost")
	docRepo.err = docErr

	svc := NewCorpusIngestService(sourceRepo, docRepo, cfg, producer)
	req := newValidCorpusRequest()

	if _, err := svc.Ingest(context.Background(), req); !errors.Is(err, docErr) {
		t.Fatalf("expected document create error to propagate, got %v", err)
	}
	if len(produced) != 0 {
		t.Fatalf("expected zero produce on document create failure, got %d", len(produced))
	}
}

func TestCorpusIngestService_IdempotentDocument(t *testing.T) {
	sourceRepo := &fakeSourceRepo{}
	docRepo := &fakeDocumentRepo{}
	cfg := newTestCorpusConfig()

	var produced []tasks.FileProcessingTask
	producer := func(task tasks.FileProcessingTask) error {
		produced = append(produced, task)
		return nil
	}

	svc := NewCorpusIngestService(sourceRepo, docRepo, cfg, producer)
	req := newValidCorpusRequest()

	if _, err := svc.Ingest(context.Background(), req); err != nil {
		t.Fatalf("first Ingest returned unexpected error: %v", err)
	}
	firstProduces := len(produced)
	if firstProduces != 1 {
		t.Fatalf("expected first Ingest to produce once, got %d", firstProduces)
	}
	firstDocCount := len(docRepo.creates)

	// Second ingest with the same document_id + content_sha256: the repo returns
	// the existing row, but the service must still enqueue the parse task exactly once.
	if _, err := svc.Ingest(context.Background(), req); err != nil {
		t.Fatalf("second Ingest returned unexpected error: %v", err)
	}
	secondRoundProduces := len(produced) - firstProduces
	if secondRoundProduces != 1 {
		t.Fatalf("expected second Ingest to produce exactly once, got %d", secondRoundProduces)
	}
	if len(docRepo.creates) != firstDocCount {
		t.Fatalf("expected idempotent document create to not add a new row, got %d (want %d)",
			len(docRepo.creates), firstDocCount)
	}
	// Sanity: the produced tasks carry matching provenance.
	lastTask := produced[len(produced)-1]
	if lastTask.Provenance == nil || lastTask.Provenance.SourceSHA256 != req.ContentSHA256 {
		t.Fatalf("produced task provenance missing or mismatched: %+v", lastTask.Provenance)
	}
	if strings.TrimSpace(lastTask.DocumentID) == "" {
		t.Fatal("produced task must carry a non-empty document_id")
	}
}
