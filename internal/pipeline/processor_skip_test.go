package pipeline

import (
	"context"
	"strings"
	"testing"

	"code-agent/internal/model"
	orchestratorclient "code-agent/pkg/orchestrator"
)

// (g) A corpus document whose parsed text comes back empty (k8s _index.md that
// is pure front matter, MinerU/Tika returns no body) is gracefully skipped: the
// document is marked SKIPPED, no chunk task is enqueued, and the parse stage
// returns nil so the task counts as SUCCESS and is not retried as FAILED.
func TestProcessParseExternalSkipsCorpusDocumentOnEmptyParsedText(t *testing.T) {
	ingestion := &fakeIngestionClient{parseResult: orchestratorclient.ParsedArtifact{ParsedText: ""}}
	docRepo := &fakeDocumentRepo{}
	p := newExternalChunkProcessor(&fakeVectorRepo{}, ingestion, docRepo)

	task := fileTask()
	task.DocumentID = lifecycleDocumentID()
	// ObjectURL is set so the storage.GetPresignedURL global is never touched;
	// the empty-text branch returns before any MinIO PutObject or Kafka produce.
	task.ObjectURL = "https://example/raw/content/en/docs/_index.md"

	err := p.processParseExternal(context.Background(), task)
	if err != nil {
		t.Fatalf("processParseExternal returned %v, want nil (skip is a success)", err)
	}
	if len(docRepo.markCalls) != 1 {
		t.Fatalf("expected exactly 1 SKIPPED mark, got %d (%+v)", len(docRepo.markCalls), docRepo.markCalls)
	}
	call := docRepo.markCalls[0]
	if call.documentID != task.DocumentID || call.status != model.DocumentSkipped {
		t.Fatalf("unexpected mark: %+v", call)
	}
	if !strings.Contains(strings.ToLower(call.lastError), "empty") {
		t.Fatalf("lastError should mention empty content, got %q", call.lastError)
	}
}

// (h) A legacy upload (no DocumentID) whose parsed text is empty still fails the
// parse stage with the original error and never touches the document repo —
// backward compatible so a normal empty-file upload is not silently swallowed.
func TestProcessParseExternalFailsLegacyTaskOnEmptyParsedText(t *testing.T) {
	ingestion := &fakeIngestionClient{parseResult: orchestratorclient.ParsedArtifact{ParsedText: ""}}
	docRepo := &fakeDocumentRepo{}
	p := newExternalChunkProcessor(&fakeVectorRepo{}, ingestion, docRepo)

	task := fileTask() // DocumentID intentionally empty — legacy upload, no lifecycle row.
	task.ObjectURL = "https://example/raw/empty.txt"

	err := p.processParseExternal(context.Background(), task)
	if err == nil || !strings.Contains(err.Error(), "extracted text is empty") {
		t.Fatalf("expected empty-text error for legacy task, got %v", err)
	}
	if len(docRepo.markCalls) != 0 {
		t.Fatalf("legacy task must not touch the document repo, got %d marks (%+v)", len(docRepo.markCalls), docRepo.markCalls)
	}
}
