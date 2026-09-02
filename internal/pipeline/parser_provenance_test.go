package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/objectpath"
	orchestratorclient "code-agent/pkg/orchestrator"
	"code-agent/pkg/tasks"
)

const (
	testRawSourceSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testArtifactSHA  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func validMineruArtifact(documentID string) orchestratorclient.ParsedArtifact {
	return validMineruArtifactWithElementID(documentID, documentID+":p0:e0")
}

func validMineruArtifactWithElementID(documentID, elementID string) orchestratorclient.ParsedArtifact {
	element := map[string]any{
		"document_id":    documentID,
		"element_id":     elementID,
		"type":           "text",
		"text":           "page evidence",
		"parser_name":    "mineru",
		"parser_version": "3.4.4",
		"source_sha256":  testArtifactSHA,
	}
	raw, err := json.Marshal(element)
	if err != nil {
		panic(err)
	}
	return orchestratorclient.ParsedArtifact{
		ParsedText:    "page evidence",
		DocumentID:    documentID,
		ParserName:    "mineru",
		ParserVersion: "3.4.4",
		SourceSHA256:  testRawSourceSHA,
		Elements:      []json.RawMessage{raw},
	}
}

func validNativeArtifact(documentID string) orchestratorclient.ParsedArtifact {
	element := map[string]any{
		"document_id":    documentID,
		"element_id":     documentID + ":sheet:Sheet1:e0",
		"type":           "table",
		"text":           "row",
		"parser_name":    "openpyxl",
		"parser_version": "3.1.5",
		"source_sha256":  testRawSourceSHA,
	}
	raw, err := json.Marshal(element)
	if err != nil {
		panic(err)
	}
	return orchestratorclient.ParsedArtifact{
		ParsedText:    "row",
		DocumentID:    documentID,
		ParserName:    "openpyxl",
		ParserVersion: "3.1.5",
		SourceSHA256:  testRawSourceSHA,
		Elements:      []json.RawMessage{raw},
	}
}

func TestValidateParsedArtifactProvenanceRejectsNativeParserMismatch(t *testing.T) {
	artifact := validNativeArtifact("doc-1")
	artifact.Elements[0] = json.RawMessage(`{"document_id":"doc-1","element_id":"e1","type":"table","parser_name":"python-docx","parser_version":"3.1.5","source_sha256":"` + testRawSourceSHA + `"}`)

	if _, err := validateParsedArtifactProvenance(tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx"}, artifact); err == nil || !strings.Contains(err.Error(), "parser") {
		t.Fatalf("native parser mismatch error = %v, want parser provenance rejection", err)
	}
}

func TestValidateParsedArtifactProvenanceRejectsNativeElementHashMismatch(t *testing.T) {
	artifact := validNativeArtifact("doc-1")
	artifact.Elements[0] = json.RawMessage(`{"document_id":"doc-1","element_id":"e1","type":"table","parser_name":"openpyxl","parser_version":"3.1.5","source_sha256":"` + testArtifactSHA + `"}`)

	if _, err := validateParsedArtifactProvenance(tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx"}, artifact); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("native hash mismatch error = %v, want source hash rejection", err)
	}
}

func TestValidateParsedArtifactProvenanceUsesStableTaskDocumentIDForCorpus(t *testing.T) {
	const documentID = "go@0123456789abcdef0123456789abcdef01234567:doc/guide.xlsx"
	artifact := validNativeArtifact(documentID)
	task := tasks.FileProcessingTask{
		FileMD5:    "file-md5-not-document-id",
		DocumentID: documentID,
		FileName:   "guide.xlsx",
	}

	if _, err := validateParsedArtifactProvenance(task, artifact); err != nil {
		t.Fatalf("corpus artifact provenance rejected: %v", err)
	}
}

func TestProcessChunkExternalArtifactRejectsNativeChunkBindingMismatch(t *testing.T) {
	artifact := validNativeArtifact("doc-1")
	artifactBytes, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	chunk := model.StructuredChunk{
		DocumentID:       "other-doc",
		ChunkID:          "other-doc:chunk:0",
		Text:             "row",
		PageID:           "other-doc:p0",
		ElementIDs:       []string{"other-doc:e0"},
		ElementTypes:     []string{"table"},
		TokenCount:       1,
		ParserName:       "openpyxl",
		ParserVersion:    "3.1.5",
		SourceSHA256:     testRawSourceSHA,
		CorpusGeneration: "techdocs-2026-07-30-v1",
	}
	repo := &fakeVectorRepo{}
	processor := &Processor{
		embeddingCfg:    serverconfig.EmbeddingConfig{Model: "model"},
		docVectorRepo:   repo,
		ingestionClient: &fakeIngestionClient{chunkResult: orchestratorclient.ChunkResult{StructuredChunks: []model.StructuredChunk{chunk}}},
		taskQueue:       &stageTaskQueue{},
		embeddingCache:  &stageCache{},
	}

	err = processor.processChunkExternalArtifact(context.Background(), tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx", Stage: tasks.StageChunk}, "parsed/doc-1.json", artifactBytes)
	if err == nil || !strings.Contains(err.Error(), "document") {
		t.Fatalf("native chunk binding error = %v, want document provenance rejection", err)
	}
	if len(repo.vectors) != 0 {
		t.Fatalf("mismatched native chunk reached persistence: %d vectors", len(repo.vectors))
	}
}

func TestProcessChunkExternalArtifactAcceptsNativeProvenance(t *testing.T) {
	artifact := validNativeArtifact("doc-1")
	artifactBytes, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	chunk := model.StructuredChunk{
		DocumentID:       "doc-1",
		ChunkID:          "doc-1:chunk:0",
		Text:             "row",
		PageID:           "doc-1:p0",
		ElementIDs:       []string{"doc-1:sheet:Sheet1:e0"},
		ElementTypes:     []string{"table"},
		TokenCount:       1,
		ParserName:       "openpyxl",
		ParserVersion:    "3.1.5",
		SourceSHA256:     testRawSourceSHA,
		CorpusGeneration: "techdocs-2026-07-30-v1",
	}
	repo := &fakeVectorRepo{}
	processor := &Processor{
		embeddingCfg:    serverconfig.EmbeddingConfig{Model: "model"},
		docVectorRepo:   repo,
		ingestionClient: &fakeIngestionClient{chunkResult: orchestratorclient.ChunkResult{StructuredChunks: []model.StructuredChunk{chunk}}},
		taskQueue:       &stageTaskQueue{},
		embeddingCache:  &stageCache{},
	}

	err = processor.processChunkExternalArtifact(context.Background(), tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx", Stage: tasks.StageChunk}, "parsed/doc-1.json", artifactBytes)
	if err != nil {
		t.Fatalf("valid native artifact rejected: %v", err)
	}
	if len(repo.vectors) != 1 || repo.vectors[0].ParserName != "openpyxl" || repo.vectors[0].SourceSHA256 != testRawSourceSHA {
		t.Fatalf("persisted native vector = %+v", repo.vectors)
	}
}

func TestProcessChunkExternalArtifactRejectsForeignElementIDBeforePersistence(t *testing.T) {
	artifact := validNativeArtifact("doc-1")
	artifactBytes, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	chunk := model.StructuredChunk{
		DocumentID:       "doc-1",
		ChunkID:          "doc-1:chunk:0",
		Text:             "row",
		PageID:           "doc-1:p0",
		ElementIDs:       []string{"doc-1:sheet:Sheet1:foreign"},
		ElementTypes:     []string{"table"},
		TokenCount:       1,
		ParserName:       "openpyxl",
		ParserVersion:    "3.1.5",
		SourceSHA256:     testRawSourceSHA,
		CorpusGeneration: "techdocs-2026-07-30-v1",
	}
	repo := &fakeVectorRepo{}
	processor := &Processor{
		embeddingCfg:    serverconfig.EmbeddingConfig{Model: "model"},
		docVectorRepo:   repo,
		ingestionClient: &fakeIngestionClient{chunkResult: orchestratorclient.ChunkResult{StructuredChunks: []model.StructuredChunk{chunk}}},
		taskQueue:       &stageTaskQueue{},
		embeddingCache:  &stageCache{},
	}

	err = processor.processChunkExternalArtifact(context.Background(), tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx", Stage: tasks.StageChunk}, "parsed/doc-1.json", artifactBytes)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "element") {
		t.Fatalf("foreign element binding error = %v, want element provenance rejection", err)
	}
	if len(repo.vectors) != 0 {
		t.Fatalf("foreign element reached persistence: %d vectors", len(repo.vectors))
	}
}

func TestProcessParseExternalRejectsInvalidPDFProvenanceBeforePersistence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*orchestratorclient.ParsedArtifact)
	}{
		{
			name: "foreign document",
			mutate: func(artifact *orchestratorclient.ParsedArtifact) {
				artifact.DocumentID = "other-doc"
			},
		},
		{
			name: "prefixed parser",
			mutate: func(artifact *orchestratorclient.ParsedArtifact) {
				artifact.ParserName = "mineru-evil"
			},
		},
		{
			name: "invalid raw hash",
			mutate: func(artifact *orchestratorclient.ParsedArtifact) {
				artifact.SourceSHA256 = "not-a-sha256"
			},
		},
		{
			name: "foreign element",
			mutate: func(artifact *orchestratorclient.ParsedArtifact) {
				artifact.Elements[0] = json.RawMessage(`{"document_id":"other-doc","element_id":"e1","type":"text","parser_name":"mineru","parser_version":"3.4.4","source_sha256":"` + testArtifactSHA + `"}`)
			},
		},
		{
			name: "element version mismatch",
			mutate: func(artifact *orchestratorclient.ParsedArtifact) {
				artifact.Elements[0] = json.RawMessage(`{"document_id":"doc-1","element_id":"e1","type":"text","parser_name":"mineru","parser_version":"3.4.5","source_sha256":"` + testArtifactSHA + `"}`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := validMineruArtifact("doc-1")
			tt.mutate(&artifact)
			store := &stageObjectStore{}
			queue := &stageTaskQueue{}
			processor := &Processor{
				minioCfg:        serverconfig.MinIOConfig{BucketName: "documents"},
				ingestionClient: &fakeIngestionClient{parseResult: artifact},
				objectStore:     store,
				taskQueue:       queue,
			}
			task := tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "scan.pdf", ObjectURL: "https://source.example/scan.pdf", Stage: tasks.StageParse}

			err := processor.processParse(context.Background(), task)
			if err == nil {
				t.Fatal("invalid PDF provenance was accepted")
			}
			if len(store.writes) != 0 || len(queue.tasks) != 0 {
				t.Fatalf("invalid artifact crossed persistence boundary: writes=%d tasks=%d", len(store.writes), len(queue.tasks))
			}
		})
	}
}

func TestProcessParseExternalRejectsCorpusRawSourceHashMismatch(t *testing.T) {
	store := &stageObjectStore{}
	queue := &stageTaskQueue{}
	processor := &Processor{
		minioCfg:        serverconfig.MinIOConfig{BucketName: "documents"},
		ingestionClient: &fakeIngestionClient{parseResult: validMineruArtifact("doc-1")},
		objectStore:     store,
		taskQueue:       queue,
	}
	task := tasks.FileProcessingTask{
		FileMD5:   "doc-1",
		FileName:  "scan.pdf",
		ObjectURL: "https://source.example/scan.pdf",
		Stage:     tasks.StageParse,
		Provenance: &model.CorpusProvenance{
			SourceSHA256: strings.Repeat("c", 64),
		},
	}

	err := processor.processParse(context.Background(), task)
	if err == nil {
		t.Fatal("raw source hash mismatch was accepted")
	}
	if len(store.writes) != 0 || len(queue.tasks) != 0 {
		t.Fatalf("mismatched artifact crossed persistence boundary: writes=%d tasks=%d", len(store.writes), len(queue.tasks))
	}
}

func TestProcessChunkExternalArtifactRejectsTamperedPDFBeforeWorker(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*orchestratorclient.ParsedArtifact)
	}{
		{
			name: "foreign document",
			mutate: func(artifact *orchestratorclient.ParsedArtifact) {
				artifact.DocumentID = "other-doc"
			},
		},
		{
			name: "prefixed parser",
			mutate: func(artifact *orchestratorclient.ParsedArtifact) {
				artifact.ParserName = "mineru-evil"
			},
		},
		{
			name: "element version mismatch",
			mutate: func(artifact *orchestratorclient.ParsedArtifact) {
				artifact.Elements[0] = json.RawMessage(`{"document_id":"doc-1","element_id":"e1","type":"text","parser_name":"mineru","parser_version":"3.4.5","source_sha256":"` + testArtifactSHA + `"}`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			artifact := validMineruArtifact("doc-1")
			tt.mutate(&artifact)
			artifactBytes, err := json.Marshal(artifact)
			if err != nil {
				t.Fatal(err)
			}
			chunk := validPDFStructuredChunk()
			client := &fakeIngestionClient{chunkResult: orchestratorclient.ChunkResult{StructuredChunks: []model.StructuredChunk{chunk}}}
			repo := &fakeVectorRepo{}
			queue := &stageTaskQueue{}
			processor := &Processor{
				embeddingCfg:    serverconfig.EmbeddingConfig{Model: "model"},
				docVectorRepo:   repo,
				ingestionClient: client,
				taskQueue:       queue,
				embeddingCache:  &stageCache{},
			}
			task := tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "scan.pdf", Stage: tasks.StageChunk}

			err = processor.processChunkExternalArtifact(context.Background(), task, "parsed/doc-1.json", artifactBytes)
			if err == nil {
				t.Fatal("tampered PDF artifact was accepted")
			}
			if client.chunkCalls != 0 || len(repo.vectors) != 0 || len(queue.tasks) != 0 {
				t.Fatalf("tampered artifact crossed worker boundary: calls=%d vectors=%d tasks=%d", client.chunkCalls, len(repo.vectors), len(queue.tasks))
			}
		})
	}
}

func TestProcessChunkExternalArtifactPersistsRawPDFSourceHash(t *testing.T) {
	artifact := validMineruArtifactWithElementID("doc-1", "doc-1:p2:e1")
	artifactBytes, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	chunk := validPDFStructuredChunk()
	chunk.SourceSHA256 = testArtifactSHA
	client := &fakeIngestionClient{chunkResult: orchestratorclient.ChunkResult{StructuredChunks: []model.StructuredChunk{chunk}}}
	repo := &fakeVectorRepo{}
	processor := &Processor{
		embeddingCfg:    serverconfig.EmbeddingConfig{Model: "model"},
		docVectorRepo:   repo,
		ingestionClient: client,
		taskQueue:       &stageTaskQueue{},
		embeddingCache:  &stageCache{},
	}
	task := tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "scan.pdf", Stage: tasks.StageChunk}

	if err := processor.processChunkExternalArtifact(context.Background(), task, "parsed/doc-1.json", artifactBytes); err != nil {
		t.Fatalf("processChunkExternalArtifact() error = %v", err)
	}
	if len(repo.vectors) != 1 {
		t.Fatalf("persisted vectors = %d, want 1", len(repo.vectors))
	}
	if repo.vectors[0].SourceSHA256 != testRawSourceSHA {
		t.Fatalf("persisted source_sha256 = %q, want raw PDF hash %q", repo.vectors[0].SourceSHA256, testRawSourceSHA)
	}
}

func TestProcessChunkExternalArtifactRejectsNonPDFDocumentMismatchBeforeWorker(t *testing.T) {
	artifactElement := map[string]any{
		"document_id":    "other-doc",
		"element_id":     "other-doc:sheet:0",
		"type":           "table",
		"text":           "row",
		"parser_name":    "openpyxl",
		"parser_version": "3.1.5",
		"source_sha256":  testArtifactSHA,
	}
	elementBytes, err := json.Marshal(artifactElement)
	if err != nil {
		t.Fatal(err)
	}
	artifact := orchestratorclient.ParsedArtifact{
		ParsedText:    "row",
		DocumentID:    "other-doc",
		ParserName:    "openpyxl",
		ParserVersion: "3.1.5",
		SourceSHA256:  testRawSourceSHA,
		Elements:      []json.RawMessage{elementBytes},
	}
	artifactBytes, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	chunk := validPDFStructuredChunk()
	chunk.DocumentID = "other-doc"
	chunk.ParserName = "openpyxl"
	chunk.ParserVersion = "3.1.5"
	client := &fakeIngestionClient{chunkResult: orchestratorclient.ChunkResult{StructuredChunks: []model.StructuredChunk{chunk}}}
	repo := &fakeVectorRepo{}
	processor := &Processor{
		embeddingCfg:    serverconfig.EmbeddingConfig{Model: "model"},
		docVectorRepo:   repo,
		ingestionClient: client,
		taskQueue:       &stageTaskQueue{},
		embeddingCache:  &stageCache{},
	}
	task := tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx", Stage: tasks.StageChunk}

	err = processor.processChunkExternalArtifact(context.Background(), task, "parsed/doc-1.json", artifactBytes)
	if err == nil {
		t.Fatal("non-PDF structured artifact with a foreign document ID was accepted")
	}
	if !strings.Contains(err.Error(), "document") {
		t.Fatalf("error = %v, want document provenance error", err)
	}
	if client.chunkCalls != 0 || len(repo.vectors) != 0 {
		t.Fatalf("foreign artifact crossed worker/persistence boundary: calls=%d vectors=%d", client.chunkCalls, len(repo.vectors))
	}
}

func TestProcessParseExternalRejectsWorkerHashWhenRawPDFDiffers(t *testing.T) {
	artifact := validMineruArtifact("doc-1")
	store := &stageObjectStore{readData: map[string]string{
		objectpath.MergedObjectName("doc-1", "scan.pdf"): "raw PDF bytes",
	}}
	queue := &stageTaskQueue{}
	processor := &Processor{
		minioCfg:        serverconfig.MinIOConfig{BucketName: "documents"},
		ingestionClient: &fakeIngestionClient{parseResult: artifact},
		objectStore:     store,
		taskQueue:       queue,
	}
	task := tasks.FileProcessingTask{
		FileMD5:   "doc-1",
		FileName:  "scan.pdf",
		ObjectURL: "https://source.example/scan.pdf",
		Stage:     tasks.StageParse,
	}

	err := processor.processParse(context.Background(), task)
	if err == nil {
		t.Fatal("worker-provided PDF hash was accepted when raw object bytes differed")
	}
	if !strings.Contains(err.Error(), "source") && !strings.Contains(err.Error(), "hash") {
		t.Fatalf("error = %v, want raw source hash error", err)
	}
	if len(queue.tasks) != 0 || len(store.writes) != 0 {
		t.Fatalf("hash-mismatched artifact crossed persistence boundary: tasks=%d writes=%d", len(queue.tasks), len(store.writes))
	}
}

func TestProcessParseExternalRejectsStructuredMetadataWithoutElements(t *testing.T) {
	rawSource := "raw office bytes"
	artifact := orchestratorclient.ParsedArtifact{
		ParsedText:    "office text",
		DocumentID:    "doc-1",
		ParserName:    "openpyxl",
		ParserVersion: "3.1.5",
		SourceSHA256:  hashSHA256([]byte(rawSource)),
	}
	store := &stageObjectStore{readData: map[string]string{
		objectpath.MergedObjectName("doc-1", "guide.xlsx"): rawSource,
	}}
	queue := &stageTaskQueue{}
	processor := &Processor{
		minioCfg:        serverconfig.MinIOConfig{BucketName: "documents"},
		ingestionClient: &fakeIngestionClient{parseResult: artifact},
		objectStore:     store,
		taskQueue:       queue,
	}
	task := tasks.FileProcessingTask{
		FileMD5:   "doc-1",
		FileName:  "guide.xlsx",
		ObjectURL: "https://source.example/guide.xlsx",
		Stage:     tasks.StageParse,
	}

	err := processor.processParse(context.Background(), task)
	if err == nil {
		t.Fatal("structured parser metadata without elements was accepted")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "structured") {
		t.Fatalf("error = %v, want structured provenance error", err)
	}
	if len(queue.tasks) != 0 || len(store.writes) != 0 {
		t.Fatalf("incomplete structured artifact crossed persistence boundary: tasks=%d writes=%d", len(queue.tasks), len(store.writes))
	}
}

func TestProcessChunkExternalArtifactRejectsStructuredMetadataWithoutElementsBeforeWorker(t *testing.T) {
	artifact := orchestratorclient.ParsedArtifact{
		ParsedText:    "office text",
		DocumentID:    "doc-1",
		ParserName:    "openpyxl",
		ParserVersion: "3.1.5",
		SourceSHA256:  testRawSourceSHA,
	}
	artifactBytes, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	client := &fakeIngestionClient{chunkResult: orchestratorclient.ChunkResult{Chunks: []string{"legacy"}}}
	processor := &Processor{
		embeddingCfg:    serverconfig.EmbeddingConfig{Model: "model"},
		ingestionClient: client,
		docVectorRepo:   &fakeVectorRepo{},
		taskQueue:       &stageTaskQueue{},
		embeddingCache:  &stageCache{},
	}
	task := tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx", Stage: tasks.StageChunk}

	err = processor.processChunkExternalArtifact(context.Background(), task, "parsed/doc-1.json", artifactBytes)
	if err == nil {
		t.Fatal("structured metadata without elements was accepted by chunk stage")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "element") {
		t.Fatalf("error = %v, want missing element provenance error", err)
	}
	if client.chunkCalls != 0 {
		t.Fatalf("worker calls = %d, want 0 for incomplete structured artifact", client.chunkCalls)
	}
}

func TestValidateParsedArtifactProvenanceRejectsIncompleteNativeElement(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"element_id":     "doc-1:sheet:0",
		"type":           "table",
		"parser_name":    "openpyxl",
		"parser_version": "3.1.5",
		"source_sha256":  testArtifactSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := orchestratorclient.ParsedArtifact{
		ParsedText:    "row",
		DocumentID:    "doc-1",
		ParserName:    "openpyxl",
		ParserVersion: "3.1.5",
		SourceSHA256:  testRawSourceSHA,
		Elements:      []json.RawMessage{raw},
	}

	if _, err := validateParsedArtifactProvenance(tasks.FileProcessingTask{FileMD5: "doc-1", FileName: "guide.xlsx"}, artifact); err == nil {
		t.Fatal("native element without document provenance was accepted")
	}
}
