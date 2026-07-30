# Retrieval Evaluation and Alias Cutover Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用可复现 qrels、指标、审计和真实 Agent E2E 验证文本/多模态检索，最后才原子切换 knowledge_base_current。

**Architecture:** 评测数据与生产 corpus 分离；指标 runner 对 BM25、BGE-M3、hybrid RRF、reranker、page visual 和 ColQwen2 分别出报告。切换脚本先执行一致性和门槛检查，再用一次 ES UpdateAliases 原子操作，失败时执行逆操作。

**Tech Stack:** Python metrics runner, JSONL qrels, Go integration runner, Elasticsearch aliases, PowerShell orchestration。

---

### Task 1: Define qrels and deterministic metrics

**Files:**
- Create: data/eval/techdocs/qrels.schema.json
- Create: orchestrator/eval/__init__.py
- Create: orchestrator/eval/metrics.py
- Create: tests/test_retrieval_metrics.py

- [ ] Step 1: Write failing metric tests

~~~python
def test_metrics_are_deterministic_and_use_stable_document_section_ids():
    qrels = [{"query_id": "q1", "document_id": "doc-1", "section_path": ["API"], "relevance": 2}]
    run = {"q1": [{"document_id": "doc-1", "section_path": ["API"], "rank": 1}]}
    scores = score_run(qrels, run, ks=(5, 10))
    assert scores["recall@5"] == 1.0
    assert scores["mrr@10"] == 1.0
    assert scores["ndcg@10"] == 1.0
~~~

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_retrieval_metrics.py -q

Expected: FAIL because the evaluator is absent.

- [ ] Step 3: Implement qrels validation and metric calculation

Validate query_id, document_id, section_path, relevance, language, query_type, and evidence_type. Use document_id/section_path rather than chunk ordinal. Calculate Recall@5, MRR@10, nDCG@10, latency, bbox hit rate, and failure-page counts with stable sorting for ties.

- [ ] Step 4: Verify and commit

Run: python -m pytest tests/test_retrieval_metrics.py -q.

~~~powershell
git add data/eval/techdocs/qrels.schema.json orchestrator/eval tests/test_retrieval_metrics.py
git commit -m "feat(rag): add deterministic retrieval metrics"
~~~

### Task 2: Add reviewed text and multimodal qrels workflow

**Files:**
- Create: data/eval/techdocs/README.md
- Create: data/eval/techdocs/qrels.text.jsonl
- Create: data/eval/multimodal/README.md
- Create: orchestrator/eval/annotation_export.py
- Create: tests/test_annotation_export.py

- [ ] Step 1: Write schema/export tests

The fixture export rejects answer text, generated responses, and unstable chunk ordinals; it accepts document_id, section_path, language, query_type, evidence_type, relevance, and reviewer_id hash.

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_annotation_export.py -q

Expected: FAIL because the review export does not exist.

- [ ] Step 3: Implement the review workflow

Generate a blind annotation worksheet from the locked source corpus. The text set must contain exactly 180 reviewed queries, 30 per source, with half Chinese queries over English documents. The multimodal set must contain at least 120 reviewed qrels covering text, image, table, equation, layout, multi-page relation, and Chinese-to-English queries. Store only stable IDs and redacted reviewer hashes; never write answers or qrels into production aliases.

- [ ] Step 4: Verify and commit

Run: python -m pytest tests/test_annotation_export.py -q and validate both JSONL files against qrels.schema.json. A missing reviewer count or unstable ID returns a non-zero exit code.

~~~powershell
git add data/eval/techdocs data/eval/multimodal orchestrator/eval/annotation_export.py tests/test_annotation_export.py
git commit -m "feat(rag): add reviewed qrels export workflow"
~~~

### Task 3: Build evaluation runner and reports

**Files:**
- Create: orchestrator/eval/runner.py
- Create: scripts/run-rag-eval.ps1
- Create: tests/test_eval_runner.py
- Modify: orchestrator/eval/metrics.py

- [ ] Step 1: Write runner tests

The test uses a fake retriever and asserts six named runs are emitted: bm25, bge_m3, hybrid_rrf, hybrid_reranker, text_only_multimodal, and text_visual_colqwen2. It asserts disabled visual runs are marked disabled rather than passing.

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_eval_runner.py -q

Expected: FAIL because the runner is absent.

- [ ] Step 3: Implement report generation

scripts/run-rag-eval.ps1 requires an explicit corpus generation, index/alias names, qrels paths, and output path. It writes redacted JSON containing overall/source/language/query-type Recall@5, MRR@10, nDCG@10, latency, worst queries, empty results, wrong hits, reranker execution, bbox hit rate, GPU peak, index bytes/page, and failed pages. It refuses to label a disabled visual path as successful.

- [ ] Step 4: Verify offline and commit

Run: python -m pytest tests/test_eval_runner.py -q. Do not invoke network, model download, or production alias changes in this test.

~~~powershell
git add orchestrator/eval/runner.py scripts/run-rag-eval.ps1 tests/test_eval_runner.py orchestrator/eval/metrics.py
git commit -m "feat(rag): add reproducible retrieval evaluation runner"
~~~

### Task 4: Add consistency audit, real Agent E2E, and atomic cutover

**Files:**
- Create: orchestrator/eval/consistency.py
- Create: scripts/cutover-rag-alias.ps1
- Create: tests/test_cutover_guards.py
- Modify: scripts/rag-agent-e2e.ps1
- Modify: internal/rag/client_integration_test.go
- Modify: docs/superpowers/specs/2026-07-30-rag-technical-corpus-design.md

- [ ] Step 1: Write failing cutover guard tests

~~~python
def test_cutover_refuses_orphan_source_or_bad_metrics():
    snapshot = {"active_documents": 10000, "orphan_documents": 1, "recall_at_5": 0.81}
    with pytest.raises(CutoverRefused):
        validate_cutover(snapshot)
~~~

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_cutover_guards.py -q

Expected: FAIL because consistency and threshold validation are absent.

- [ ] Step 3: Implement preflight and inverse alias operation

The guard checks 10,000-20,000 active chunks, zero orphan MySQL/ES provenance, Recall@5 >= 0.80, MRR@10 >= 0.75, nDCG@10 >= 0.75, Chinese-to-English Recall@5 >= 0.75, P95 <= 5 seconds, no embedding/reranker dimension failures, and a passing real Agent E2E. The script snapshots MySQL, MinIO, pipeline, ES count/mapping/alias, refuses any failed gate, and performs one atomic UpdateAliases call. The rollback command executes the exact inverse action and records both responses.

- [ ] Step 4: Run the guard test and explicit E2E

Run: python -m pytest tests/test_cutover_guards.py -q. Then, only with CODE_AGENT_RUN_RAG_E2E=1 and the approved local DeepSeek key in the process environment, run scripts/rag-agent-e2e.ps1 against Docker. Assert top-k text, source URL, section path, document/page/element provenance, and wrong-user isolation.

- [ ] Step 5: Commit

~~~powershell
git add orchestrator/eval/consistency.py scripts/cutover-rag-alias.ps1 tests/test_cutover_guards.py scripts/rag-agent-e2e.ps1 internal/rag/client_integration_test.go docs/superpowers/specs/2026-07-30-rag-technical-corpus-design.md
git commit -m "feat(rag): gate and atomically cut over knowledge alias"
~~~

## Rollback Boundary

Do not delete old knowledge_base or any historical 9 ES documents. If a post-cutover E2E fails, immediately run the recorded inverse UpdateAliases request, keep v2 for analysis, and mark the generation FAILED rather than ACTIVE.
