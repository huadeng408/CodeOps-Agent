# RAG Foundation Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement these plans task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为来源、文档、结构化 chunk 和索引目标建立可版本化、可幂等、可回滚的持久化契约。

**Architecture:** 新增 knowledge_source 与 knowledge_document 元数据表，扩展现有 document_vectors/ES DTO 以保存 provenance；旧字段保持兼容。所有新写入必须携带 corpus_generation 和 target_index，但默认检索仍指向旧 index。

**Tech Stack:** Go, GORM, MySQL, Elasticsearch source DTO, Go unit/integration tests。

---

### Task 1: Add source and document status models

**Files:**
- Create: internal/model/knowledge_source.go
- Create: internal/model/knowledge_document.go
- Create: internal/model/knowledge_contract_test.go

- [ ] Step 1: Write failing contract tests

~~~go
func TestKnowledgeIDsAreStable(t *testing.T) {
    sourceID := SourceVersionID("python", "0123456789abcdef0123456789abcdef01234567", "techdocs-v1")
    if sourceID != "python@0123456789abcdef0123456789abcdef01234567#techdocs-v1" {
        t.Fatalf("source id = %q", sourceID)
    }
    documentID := DocumentID("python", "0123456789abcdef0123456789abcdef01234567", "Doc/library.rst")
    if documentID != "python@0123456789abcdef0123456789abcdef01234567:Doc/library.rst" {
        t.Fatalf("document id = %q", documentID)
    }
}
~~~

- [ ] Step 2: Run the focused test and verify it fails

Run: go test ./internal/model -run TestKnowledgeIDsAreStable -count=1

Expected: FAIL because the ID helpers and models do not exist.

- [ ] Step 3: Add the minimal models and helpers

~~~go
type KnowledgeSourceStatus string
const (
    SourceStaged KnowledgeSourceStatus = "STAGED"
    SourcePiloted KnowledgeSourceStatus = "PILOTED"
    SourceActive KnowledgeSourceStatus = "ACTIVE"
    SourceFailed KnowledgeSourceStatus = "FAILED"
)

type KnowledgeSource struct {
    SourceVersionID string
    SourceID string
    RepositoryURL string
    SourceCommit string
    LicenseSPDX string
    LicensePath string
    LicenseSHA256 string
    CorpusGeneration string
    Status KnowledgeSourceStatus
    LastError string
    FetchedAt *time.Time
    CreatedAt time.Time
    UpdatedAt time.Time
}

func (KnowledgeSource) TableName() string { return "knowledge_source" }
~~~

KnowledgeDocument uses DocumentID as its primary key and stores source path/url, title, language, content SHA-256, file MD5, source version, corpus generation, target index, status, retry count, last error, timestamps. SourceVersionID and DocumentID reject blank components before concatenation. The implementation must add the GORM tags matching the existing model style.

- [ ] Step 4: Run the focused test and verify it passes

Run: go test ./internal/model -run TestKnowledgeIDsAreStable -count=1

Expected: PASS.

- [ ] Step 5: Commit

~~~powershell
git add internal/model/knowledge_source.go internal/model/knowledge_document.go internal/model/knowledge_contract_test.go
git commit -m "feat(rag): add source and document provenance models"
~~~

### Task 2: Persist structured chunk provenance

**Files:**
- Modify: internal/model/document_vector.go
- Modify: internal/model/es_document.go
- Create: internal/model/structured_chunk.go
- Create: internal/model/structured_chunk_test.go

- [ ] Step 1: Write failing serialization tests

~~~go
func TestStructuredChunkRejectsMissingProvenance(t *testing.T) {
    err := (StructuredChunk{ChunkID: "c1", Text: "body"}).Validate()
    if err == nil || !strings.Contains(err.Error(), "document_id") { t.Fatal(err) }
}
~~~

- [ ] Step 2: Run and observe the failure

Run: go test ./internal/model -run TestStructuredChunkRejectsMissingProvenance -count=1

Expected: FAIL because StructuredChunk.Validate is absent.

- [ ] Step 3: Add the typed contract

StructuredChunk must contain DocumentID, ChunkID, ParentChunkID, Text, EmbeddingText, SectionPath, PageID, PageSpan, ElementIDs, ElementTypes, BBoxRefs, AssetRefs, TokenCount, TokenizerID, ParserName, ParserVersion, CorpusGeneration, FileMD5, UserID, OrgTag, and IsPublic. Validate rejects blank document/chunk IDs, non-positive token counts, blank parser/version, and empty provenance. Add the same JSON fields to model.EsDocument; keep the existing legacy fields unchanged.

- [ ] Step 4: Run the tests

Run: go test ./internal/model ./pkg/es -count=1

Expected: PASS, including JSON round-trip coverage for array fields.

- [ ] Step 5: Commit

~~~powershell
git add internal/model/document_vector.go internal/model/es_document.go internal/model/structured_chunk.go internal/model/structured_chunk_test.go
git commit -m "feat(rag): add structured chunk provenance contract"
~~~

### Task 3: Add repositories and migrations without changing the active index

**Files:**
- Create: internal/repository/knowledge_source_repository.go
- Create: internal/repository/knowledge_document_repository.go
- Create: internal/repository/knowledge_repository_test.go
- Modify: cmd/server/main.go
- Modify: pkg/database/migration.go

- [ ] Step 1: Write repository contract tests against the project MySQL integration fixture

The test creates one KnowledgeSource and one KnowledgeDocument, repeats the same CreateOrGet call, and asserts one row, unchanged content hash, and STAGED status. The test must skip unless CODE_AGENT_RUN_DB_INTEGRATION=1; ordinary go test ./... remains offline.

- [ ] Step 2: Run the test in offline mode

Run: go test ./internal/repository -run TestKnowledgeRepository -count=1

Expected: PASS or explicit SKIP with no network call.

- [ ] Step 3: Implement idempotent repository methods

Expose CreateOrGetSource, CreateOrGetDocument, MarkDocumentStatus, ListDocumentsByGeneration, and CountActiveDocuments. Use the stable IDs as conflict keys; a content SHA change creates a new document ID/version rather than overwriting an existing row.

- [ ] Step 4: Wire AutoMigrate and explicit runtime schema statements

Add the two metadata models to database.DB.AutoMigrate in cmd/server/main.go. Add nullable provenance columns to document_vectors via GORM tags and a guarded ALTER TABLE list in pkg/database/migration.go. Startup must continue if a column already exists, but must return an error for a non-compatibility SQL failure.

- [ ] Step 5: Run the foundation verification

Run: go test ./internal/model ./internal/repository ./pkg/database -count=1 and go vet ./internal/model ./internal/repository ./pkg/database.

Expected: PASS; existing knowledge_base is not created, renamed, deleted, or aliased by this task.

- [ ] Step 6: Commit

~~~powershell
git add internal/repository/knowledge_source_repository.go internal/repository/knowledge_document_repository.go internal/repository/knowledge_repository_test.go cmd/server/main.go pkg/database/migration.go
git commit -m "feat(rag): persist corpus provenance metadata"
~~~

### Task 4: Add configuration and a snapshot command

**Files:**
- Modify: internal/serverconfig/config.go
- Modify: configs/server.yaml
- Modify: .env.example
- Create: scripts/rag_snapshot.ps1

- [ ] Step 1: Write configuration tests

Assert defaults are CorpusGeneration=techdocs-2026-07-30-v1, TextIndex=knowledge_base_v2_bge_m3, ReadAlias=knowledge_base_current, VisualAlias=knowledge_page_visual_current, and AllowAliasSwitch=false.

- [ ] Step 2: Implement config fields and safe defaults

Add a CorpusConfig group with Generation, TextIndex, ReadAlias, VisualPilotPrefix, VisualAlias, LoaderUser, and AllowAliasSwitch. Secrets remain environment/local settings; no key is added to YAML or .env.example.

- [ ] Step 3: Add the read-only snapshot command

scripts/rag_snapshot.ps1 must query MySQL counts, ES _count, _mapping, and _alias, and list MinIO object keys without deleting anything. It accepts -OutputPath and writes a redacted JSON report.

- [ ] Step 4: Verify and commit

Run: go test ./internal/serverconfig -count=1; then git diff --check and git diff --stat.

~~~powershell
git add internal/serverconfig/config.go configs/server.yaml .env.example scripts/rag_snapshot.ps1
git commit -m "chore(rag): add corpus generation and snapshot configuration"
~~~

## Rollback Boundary

Revert only the commits from this plan if contract tests fail. Do not drop metadata tables, alter old rows, delete old ES indices, or clear Docker volumes; schema rollback uses additive columns and the existing database backup.
