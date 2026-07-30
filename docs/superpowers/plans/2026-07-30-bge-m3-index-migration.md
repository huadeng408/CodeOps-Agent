# BGE-M3 Text Index Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立原生 1024 维 BGE-M3 文本索引，保留旧 knowledge_base 作为只读回滚来源，并通过 alias 原子切换。

**Architecture:** ES 物理 index 与 alias 分离；v2 写入只接受结构化 chunk 和 BGE-M3 原生向量。搜索先并行 BM25/vector top 100，再以 page/parent 关系执行 RRF 和证据展开；视觉结果在本计划中关闭。

**Tech Stack:** Go Elasticsearch client, BGE-M3-compatible embedding HTTP service, MySQL document_vectors, existing SearchService tests。

---

### Task 1: Add BGE-M3 configuration and preflight

**Files:**
- Modify: internal/serverconfig/config.go
- Modify: configs/server.yaml
- Modify: pkg/embedding/client.go
- Create: pkg/embedding/preflight.go
- Create: pkg/embedding/preflight_test.go

- [ ] Step 1: Write failing preflight tests

~~~go
func TestEmbeddingPreflightRequiresNative1024Dimensions(t *testing.T) {
    err := ValidateEmbeddingContract("BAAI/bge-m3@rev-1", 1024, []float32{0, 1})
    if err == nil || !strings.Contains(err.Error(), "1024") { t.Fatal(err) }
}
~~~

- [ ] Step 2: Run and verify failure

Run: go test ./pkg/embedding -run TestEmbeddingPreflightRequiresNative1024Dimensions -count=1

Expected: FAIL because the contract validator does not exist.

- [ ] Step 3: Implement health and dimension validation

Add embedding fields ModelRevision, ExpectedDimensions, HealthPath, and RequireNativeDimensions. The preflight calls health, checks model name/revision, sends a fixed English/Chinese pair, requires exactly 1024 finite values for each, and rejects resize/padding/truncation. Persist model_version as BAAI/bge-m3@revision in every v2 vector.

- [ ] Step 4: Verify and commit

Run: go test ./pkg/embedding -count=1 and go vet ./pkg/embedding.

~~~powershell
git add internal/serverconfig/config.go configs/server.yaml pkg/embedding/client.go pkg/embedding/preflight.go pkg/embedding/preflight_test.go
git commit -m "feat(rag): enforce native BGE-M3 embedding contract"
~~~

### Task 2: Create mapping-versioned physical index and alias operations

**Files:**
- Modify: pkg/es/client.go
- Create: pkg/es/knowledge_index.go
- Create: pkg/es/knowledge_index_test.go
- Modify: internal/serverconfig/config.go

- [ ] Step 1: Write failing mapping tests

~~~go
func TestKnowledgeV2MappingSeparatesTextAndVisualFields(t *testing.T) {
    mapping := KnowledgeV2Mapping(1024)
    assertPropertyDims(t, mapping, "vector", 1024)
    assertNoProperty(t, mapping, "page_visual_vector")
    assertKeywordProperty(t, mapping, "document_id")
}
~~~

- [ ] Step 2: Run and verify failure

Run: go test ./pkg/es -run TestKnowledgeV2MappingSeparatesTextAndVisualFields -count=1

Expected: FAIL because the v2 mapping builder is absent.

- [ ] Step 3: Implement mapping and atomic alias API

KnowledgeV2Mapping defines text_content/embedding_text text fields, vector dense_vector dims 1024 cosine, source and provenance keyword fields, arrays for element_ids/element_types, and no visual vector field. Add EnsurePhysicalIndex, ReadAlias, ValidateAliasTarget, SwitchAlias, and RollbackAlias. SwitchAlias uses one ES Indices.UpdateAliases request containing remove/add actions and refuses an existing alias whose mapping or model_version is incompatible.

- [ ] Step 4: Verify using an httptest ES server

Run: go test ./pkg/es -run Mapping -count=1 and go test ./pkg/es -run Alias -count=1.

Expected: PASS with tests asserting one atomic update request and no delete request.

- [ ] Step 5: Commit

~~~powershell
git add pkg/es/client.go pkg/es/knowledge_index.go pkg/es/knowledge_index_test.go internal/serverconfig/config.go
git commit -m "feat(rag): add versioned text index and alias operations"
~~~

### Task 3: Write structured vectors and index documents

**Files:**
- Modify: internal/pipeline/processor.go
- Modify: pkg/orchestrator/ingestion_client.go
- Modify: internal/model/es_document.go
- Create: internal/pipeline/structured_index_test.go

- [ ] Step 1: Write a failing dimension/provenance pipeline test

Feed one structured chunk and a 512-dimensional vector into the v2 index path. Assert the path returns a dimension mismatch and makes no ES write. Feed a 1024-dimensional vector and assert the serialized document has document_id, page_id, element_ids, tokenizer_id, corpus_generation, and model_version.

- [ ] Step 2: Run and verify failure

Run: go test ./internal/pipeline -run TestStructuredIndex -count=1

Expected: FAIL until the structured path and dimension guard exist.

- [ ] Step 3: Implement the minimal path

The index stage loads structured chunks from MySQL, calls the BGE-M3 client, validates vector length against the target mapping, and bulk writes EsDocument to knowledge_base_v2_bge_m3. It never writes a vector to the old index when corpus_generation is v2. Legacy tasks retain the old behavior only when corpus_generation is empty.

- [ ] Step 4: Verify and commit

Run: go test ./internal/pipeline ./pkg/es -count=1.

~~~powershell
git add internal/pipeline/processor.go pkg/orchestrator/ingestion_client.go internal/model/es_document.go internal/pipeline/structured_index_test.go
git commit -m "feat(rag): index structured BGE-M3 chunks"
~~~

### Task 4: Add BM25/vector RRF and provenance evidence expansion

**Files:**
- Modify: internal/service/search_service.go
- Modify: internal/model/es_document.go
- Create: internal/service/search_service_v2_test.go
- Modify: internal/handler/orchestrator_handler.go
- Modify: internal/rag/client.go
- Modify: internal/tools/rag.go

- [ ] Step 1: Write failing retrieval tests

~~~go
func TestV2SearchDeduplicatesSamePageAndExpandsEvidence(t *testing.T) {
    result := fuseAndExpand([]retrievalHit{
        {Source: model.EsDocument{PageID: "p1", ParentChunkID: "parent-1", ChunkID: 1}},
        {Source: model.EsDocument{PageID: "p1", ParentChunkID: "parent-1", ChunkID: 2}},
    }, 5)
    if len(result) != 1 || result[0].PageID != "p1" || result[0].ParentChunkID != "parent-1" {
        t.Fatalf("unexpected evidence: %#v", result)
    }
}
~~~

- [ ] Step 2: Run and verify failure

Run: go test ./internal/service -run TestV2SearchDeduplicatesSamePageAndExpandsEvidence -count=1

Expected: FAIL because page-aware fusion is absent.

- [ ] Step 3: Implement retrieval with alias and ACL

Search must query knowledge_base_current only after the alias is explicitly configured. BM25 and vector each return up to 100 candidates, RRF uses k=60, and page/parent keys prevent one page from occupying every slot. The response adds source URL, section path, document/page/element IDs, bbox refs and asset refs while retaining text_content. The existing user/public/org filter is applied before fusion and evidence expansion.

- [ ] Step 4: Verify unit and integration boundaries

Run: go test ./internal/service ./internal/rag ./internal/tools -count=1. Run the explicit Docker-backed runner only with CODE_AGENT_RUN_RAG_E2E=1; assert Chinese query to English public content, correct source URL, and wrong-user isolation.

- [ ] Step 5: Commit

~~~powershell
git add internal/service/search_service.go internal/service/search_service_v2_test.go internal/handler/orchestrator_handler.go internal/rag/client.go internal/tools/rag.go internal/model/es_document.go
git commit -m "feat(rag): add provenance-aware hybrid retrieval"
~~~

## Rollback Boundary

Keep knowledge_base and its current configuration untouched. Rollback removes only an unaliased v2 physical index after evidence reports and backups are retained; alias rollback is a single inverse UpdateAliases request.
