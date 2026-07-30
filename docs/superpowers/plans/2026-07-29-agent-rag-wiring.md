# Agent RAG Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the local Agent search the existing knowledge base through an LLM tool and ingest workspace documents through a `/ingest` command, using the server's internal-token boundary and an explicitly configured RAG user.

**Architecture:** Add a small `internal/rag` HTTP client shared by the executor and CLI. The client calls `/internal/orchestrator/knowledge-search` and a new authenticated `/internal/orchestrator/knowledge-ingest` endpoint; the server endpoint reuses `UploadService` so every PDF still enters the existing MinerU-backed pipeline. RAG is disabled by default and never performs network I/O unless all required settings are present.

**Tech Stack:** Go `net/http`, Gin, multipart upload, existing MySQL/Redis/MinIO/Kafka/Elasticsearch pipeline, Python tool registry, Docker Compose.

---

### Task 1: RAG HTTP client and configuration

**Files:**
- Create: `internal/rag/client.go`
- Create: `internal/rag/client_test.go`
- Modify: `internal/config/loader.go`
- Create: `internal/config/loader_rag_test.go`

- [ ] **Step 1: Write failing client contract tests**

Test with `httptest.Server` that `Search` sends `POST /internal/orchestrator/knowledge-search`, `X-Internal-Token`, the configured `user.id`, query/topK/mode/rerank fields, and decodes the standard `{code,data,message}` envelope. Test that disabled or incomplete configuration returns a typed unavailable error without dialing the server. Test `Ingest` multipart fields (`file`, `userId`, `orgTag`, `isPublic`) and response decoding.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/rag -run Test -v`

Expected: FAIL because `internal/rag` and its client API do not exist.

- [ ] **Step 3: Implement the minimal client**

Expose `Searcher` and `Ingester` interfaces plus a `Client` created from:

```go
type Config struct {
    Enabled        bool
    BaseURL       string
    InternalToken string
    UserID        uint
    OrgTag        string
    IngestPublic  bool
    HTTPClient    *http.Client
}
```

Use JSON for search and streaming multipart for ingest. Reject blank query, invalid mode, missing file, non-2xx responses, malformed envelopes, and incomplete enabled configuration with actionable errors. Do not silently substitute user ID 1.

- [ ] **Step 4: Write failing config merge tests**

Cover `rag_enabled`, `rag_server_url`, `rag_internal_secret`, `rag_user_id`, `rag_org_tag`, and `rag_ingest_public`, including explicit `false` merge semantics.

- [ ] **Step 5: Verify RED, implement config fields, verify GREEN**

Run: `go test ./internal/config ./internal/rag -v`

Expected: PASS.

### Task 2: SearchKnowledge model tool and Agent injection

**Files:**
- Create: `internal/tools/rag.go`
- Create: `internal/tools/rag_test.go`
- Modify: `internal/tools/executor.go`
- Modify: `internal/permission/controller.go`
- Modify: `internal/cli/app.go`
- Modify: `orchestrator/runtime/tools.py`
- Modify: `tests/test_tools.py` or the existing tool-registry test module

- [ ] **Step 1: Write failing executor tests**

Inject a recording `rag.Searcher`; assert `SearchKnowledge` validates `query`, applies defaults `top_k=5`, `mode=hybrid`, `disable_rerank=false`, returns readable source/chunk/score/text output, and truncates through the executor limits. Assert a nil/disabled searcher returns a normal tool error and performs no call.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/tools -run RAG -v`

Expected: FAIL because the setter and tool branch do not exist.

- [ ] **Step 3: Implement executor support and injection**

Add `SetRAGSearcher`, the `SearchKnowledge` switch branch, permission default, and construct/inject the client in `cli.NewApp` only from loaded configuration.

- [ ] **Step 4: Write and run the Python registry test first**

Assert `SearchKnowledge` is exposed with `query`, `top_k`, `mode`, and `disable_rerank`, and uses `AUTO_ALLOW` because it is read-only.

Run: `python -m pytest -q <selected-tool-registry-test>` before and after modifying `orchestrator/runtime/tools.py`.

- [ ] **Step 5: Verify GREEN**

Run: `go test ./internal/tools ./internal/cli ./internal/permission -v` and the selected Python test.

Expected: PASS.

### Task 3: Internal knowledge-ingest endpoint

**Files:**
- Create: `internal/handler/knowledge_ingest_handler.go`
- Create: `internal/handler/knowledge_ingest_handler_test.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: Write failing handler tests**

Use a recording `UploadService` and multipart requests. Cover missing/invalid `userId`, missing/empty file, successful single-chunk upload+merge, multi-chunk ordering, per-chunk MD5, configured org/public flags, and propagation of upload/merge failures. Route tests must prove missing or wrong `X-Internal-Token` is rejected before the handler.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/handler -run KnowledgeIngest -v`

Expected: FAIL because the handler does not exist.

- [ ] **Step 3: Implement minimal streaming ingestion**

The handler reads the multipart file via `multipart.File`, computes full MD5, seeks back, streams `service.DefaultChunkSize` chunks through `UploadService.UploadChunk`, then calls `MergeChunks`. It returns:

```json
{"code":202,"data":{"fileMd5":"...","fileName":"...","objectUrl":"..."},"message":"ingestion queued"}
```

Register `POST /internal/orchestrator/knowledge-ingest` under the existing `InternalAuth` group. Do not add a JWT bypass to public upload routes.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/handler ./cmd/server -v`

Expected: PASS.

### Task 4: `/ingest` CLI command

**Files:**
- Modify: `internal/cli/app.go`
- Create or modify: `internal/cli/app_rag_test.go`
- Modify: `README.md`
- Modify: `.env.example`

- [ ] **Step 1: Write failing command tests**

Construct an App with a recording ingester and assert `/ingest <path>` appears in help, rejects disabled RAG, missing paths, directories, paths escaping the project root (including symlinks), and prints the returned MD5/status for a valid workspace file.

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/cli -run Ingest -v`

Expected: FAIL because the command does not exist.

- [ ] **Step 3: Implement the command**

Store the RAG client behind an `Ingester` interface on `App`, resolve paths relative to the current Agent working directory, canonicalize symlinks, enforce project-root containment, and call `Ingest` with the turn context. Document the six JSON settings and the internal token requirement.

- [ ] **Step 4: Verify GREEN**

Run: `go test ./internal/cli -run Ingest -v`

Expected: PASS.

### Task 5: Explicit real integration and final verification

**Files:**
- Create: `internal/rag/client_integration_test.go`
- Create: `scripts/rag-agent-e2e.ps1`

- [ ] **Step 1: Write the opt-in real integration test**

Guard with `CODE_AGENT_RUN_RAG_E2E=1`. The test creates a unique text document, calls the real internal ingest endpoint, polls the real `SearchKnowledge` client until the unique marker is returned, and fails if the result did not traverse the server/pipeline. It must use `CODE_AGENT_RAG_SERVER_URL`, `CODE_AGENT_RAG_INTERNAL_SECRET`, and `CODE_AGENT_RAG_USER_ID` rather than embedded credentials.

- [ ] **Step 2: Run the actual stack**

Start MySQL, Redis, MinIO, Kafka, Elasticsearch, embedding, and the Go server. Run the E2E script with the local configuration. Capture the exact marker, file MD5, and top search hit.

- [ ] **Step 3: Run all regression gates**

Run:

```powershell
go test ./...
go vet ./...
python -m pytest -q
git diff --check
```

Expected: all pass; no tracked secret or unrelated untracked file is staged.

- [ ] **Step 4: Review and commit**

Perform spec-compliance review, then code-quality review, fix and re-review any findings. Commit only task files with `feat(rag): wire agent knowledge search and ingestion`, push `feature/complete-design-implementation`, and leave the two user Markdown files plus `tmp/` untouched.
