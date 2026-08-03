// Package service contains business logic.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/kafka"
	"code-agent/pkg/tasks"
)

// ErrCorpusValidation is the sentinel error returned when a corpus ingest
// request violates the server-side corpus contract (wrong generation / target
// index, mismatched content hash, or unauthorized loader user). Handlers map it
// to HTTP 400 and must not persist or enqueue anything when it fires.
var ErrCorpusValidation = errors.New("corpus validation failed")

// CorpusIngestRequest carries everything the dedicated corpus entry needs to
// persist source/document lifecycle rows and enqueue the first pipeline stage.
// The handler is responsible for computing FileMD5/ContentSHA256 over the raw
// bytes and for storing the merged object; the service is the authority for the
// corpus contract and lifecycle persistence.
type CorpusIngestRequest struct {
	UserID    uint
	OrgTag    string
	IsPublic  bool
	FileMD5   string
	FileName  string
	TotalSize int64
	// ObjectURL is the presigned URL of the already-merged object. When empty
	// the parse worker derives the object name from FileMD5+FileName, so the
	// field is informational for the result echo and the task payload.
	ObjectURL string
	// ContentSHA256 is the handler-computed SHA-256 of the raw staging bytes.
	// The service validates it is non-empty and matches the committed
	// Provenance.SourceSHA256, which prevents a forged hash from routing
	// unrelated content into the corpus index.
	ContentSHA256 string
	// RunID optionally scopes the enqueued task to a controlled run. Empty
	// keeps the legacy consumer dedup semantics; the import entry (Task 8)
	// passes import-<unix> and admin replay (Task 5) passes replay-<unix>.
	RunID string
	// Provenance is the pinned source identity validated at this trust
	// boundary. It is forwarded to the worker via the task payload.
	Provenance model.CorpusProvenance
	// SourceVersionID and DocumentID are optional pre-computed primary keys.
	// When empty the service derives them from Provenance so callers cannot
	// accidentally split one source version across mismatched identifiers.
	SourceVersionID string
	DocumentID      string
}

// CorpusIngestResult echoes the enqueued document identity back to the caller.
type CorpusIngestResult struct {
	FileMD5    string
	FileName   string
	ObjectURL  string
	DocumentID string
}

// CorpusIngestService is the dedicated corpus entry: validate the corpus
// contract, persist STAGED source/document rows, and enqueue the parse task.
type CorpusIngestService interface {
	Ingest(ctx context.Context, req CorpusIngestRequest) (*CorpusIngestResult, error)
}

type corpusIngestService struct {
	sourceRepo repository.KnowledgeSourceRepository
	docRepo    repository.KnowledgeDocumentRepository
	cfg        serverconfig.CorpusConfig
	producer   func(tasks.FileProcessingTask) error
}

// NewCorpusIngestService wires the dedicated corpus entry. A nil producer falls
// back to the real Kafka producer so production wiring stays explicit while
// tests inject a recording fake.
func NewCorpusIngestService(
	sourceRepo repository.KnowledgeSourceRepository,
	docRepo repository.KnowledgeDocumentRepository,
	cfg serverconfig.CorpusConfig,
	producer func(tasks.FileProcessingTask) error,
) CorpusIngestService {
	if producer == nil {
		producer = kafka.ProduceFileTask
	}
	return &corpusIngestService{
		sourceRepo: sourceRepo,
		docRepo:    docRepo,
		cfg:        cfg,
		producer:   producer,
	}
}

// Ingest validates the corpus contract, persists STAGED source/document rows,
// and enqueues the parse task. Validation failures return ErrCorpusValidation
// with zero persistence; storage/producer failures return the wrapped error so
// the handler surfaces a 5xx without leaking internal details.
func (s *corpusIngestService) Ingest(ctx context.Context, req CorpusIngestRequest) (*CorpusIngestResult, error) {
	if err := s.validate(req); err != nil {
		return nil, err
	}

	prov := req.Provenance
	sourceVersionID := strings.TrimSpace(req.SourceVersionID)
	if sourceVersionID == "" {
		sourceVersionID = model.SourceVersionID(prov.SourceID, prov.SourceCommit, prov.CorpusGeneration)
	}
	documentID := strings.TrimSpace(req.DocumentID)
	if documentID == "" {
		documentID = model.DocumentID(prov.SourceID, prov.SourceCommit, prov.SourcePath)
	}

	source := &model.KnowledgeSource{
		SourceVersionID:  sourceVersionID,
		SourceID:         prov.SourceID,
		RepositoryURL:    prov.SourceURL,
		SourceCommit:     prov.SourceCommit,
		CorpusGeneration: prov.CorpusGeneration,
		Status:           model.SourceStaged,
	}
	if _, err := s.sourceRepo.CreateOrGetSource(source); err != nil {
		return nil, fmt.Errorf("corpus ingest: create source version: %w", err)
	}

	document := &model.KnowledgeDocument{
		DocumentID:       documentID,
		SourceID:         prov.SourceID,
		SourcePath:       prov.SourcePath,
		SourceURL:        prov.SourceURL,
		SourceCommit:     prov.SourceCommit,
		DocumentLanguage: corpusDefaultLanguage,
		ContentSHA256:    req.ContentSHA256,
		FileMD5:          req.FileMD5,
		SourceVersionID:  sourceVersionID,
		CorpusGeneration: prov.CorpusGeneration,
		TargetIndex:      s.cfg.TextIndex,
		Status:           model.DocumentStaged,
	}
	stored, err := s.docRepo.CreateOrGetDocument(document)
	if err != nil {
		return nil, fmt.Errorf("corpus ingest: create document: %w", err)
	}

	task := tasks.FileProcessingTask{
		FileMD5:          req.FileMD5,
		ObjectURL:        req.ObjectURL,
		FileName:         req.FileName,
		UserID:           req.UserID,
		OrgTag:           req.OrgTag,
		IsPublic:         req.IsPublic,
		Stage:            tasks.StageParse,
		CorpusGeneration: prov.CorpusGeneration,
		DocumentID:       stored.DocumentID,
		Provenance:       &prov,
		RunID:            req.RunID,
	}
	if err := s.producer(task); err != nil {
		return nil, fmt.Errorf("corpus ingest: enqueue parse task: %w", err)
	}

	return &CorpusIngestResult{
		FileMD5:    req.FileMD5,
		FileName:   req.FileName,
		ObjectURL:  req.ObjectURL,
		DocumentID: stored.DocumentID,
	}, nil
}

// corpusDefaultLanguage is the placeholder language written to the staged
// document row; the pipeline detects the real language during parsing.
const corpusDefaultLanguage = "auto"

// validate enforces the server-authoritative corpus contract. Every rejection
// wraps ErrCorpusValidation so the handler can uniformly map it to HTTP 400.
func (s *corpusIngestService) validate(req CorpusIngestRequest) error {
	if err := req.Provenance.Validate(); err != nil {
		return fmt.Errorf("%w: provenance: %v", ErrCorpusValidation, err)
	}
	if req.UserID != s.cfg.LoaderUser {
		return fmt.Errorf("%w: loader_user mismatch", ErrCorpusValidation)
	}
	if req.Provenance.CorpusGeneration != s.cfg.Generation {
		return fmt.Errorf("%w: corpus_generation mismatch", ErrCorpusValidation)
	}
	if req.Provenance.TargetIndex != s.cfg.TextIndex {
		return fmt.Errorf("%w: target_index mismatch", ErrCorpusValidation)
	}
	contentSHA := strings.TrimSpace(req.ContentSHA256)
	if contentSHA == "" {
		return fmt.Errorf("%w: content_sha256 is required", ErrCorpusValidation)
	}
	if contentSHA != req.Provenance.SourceSHA256 {
		return fmt.Errorf("%w: content_sha256 does not match source_sha256", ErrCorpusValidation)
	}
	return nil
}
