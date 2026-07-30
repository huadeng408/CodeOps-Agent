# Multimodal Visual Pilot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立独立的页面视觉索引和受限 ColQwen2 重排适配器，先完成可关闭的 500-2,000 页 bake-off，再决定是否创建视觉生产 alias。

**Architecture:** 页面截图向量和视觉资产引用存储在独立物理 index，模型通过 adapter 接口注入。文本 v2 不依赖视觉链；GPU、许可证、显存或效果门槛失败时返回 disabled，并继续服务文本检索。

**Tech Stack:** Python adapter service, Elasticsearch visual mapping, MinIO page assets, optional Transformers/ColPali engine, explicit Docker/GPU integration tests。

---

### Task 1: Define visual index contract and disabled behavior

**Files:**
- Create: orchestrator/visual/__init__.py
- Create: orchestrator/visual/models.py
- Create: pkg/es/visual_index.go
- Create: tests/test_visual_contract.py
- Create: pkg/es/visual_index_test.go

- [ ] Step 1: Write failing contract tests

~~~python
def test_visual_adapter_is_explicitly_disabled_without_model():
    adapter = VisualAdapter.disabled("no GPU or model revision")
    result = adapter.search("table query", top_k=5)
    assert result.status == "disabled"
    assert result.items == []
    assert "no GPU" in result.reason
~~~

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_visual_contract.py -q and go test ./pkg/es -run Visual -count=1.

Expected: FAIL because the adapter and visual mapping do not exist.

- [ ] Step 3: Implement separate mapping and status

Visual pages use index name knowledge_page_visual_pilot_<model>_<revision>, a model-specific dense-vector dimension, page_id/document_id/source refs, asset_ref, bbox_refs, and model_revision. The mapping must reject a request that attempts to put page_visual_vector into the text v2 mapping. Add status values disabled, pilot, ready, and failed; disabled is a valid non-success state and is included in every report.

- [ ] Step 4: Verify and commit

Run: python -m pytest tests/test_visual_contract.py -q and go test ./pkg/es -run Visual -count=1.

~~~powershell
git add orchestrator/visual pkg/es/visual_index.go pkg/es/visual_index_test.go tests/test_visual_contract.py
git commit -m "feat(rag): add isolated visual index contract"
~~~

### Task 2: Add page rendering, asset references, and candidate adapters

**Files:**
- Create: orchestrator/visual/render.py
- Create: orchestrator/visual/encoders.py
- Create: orchestrator/visual/candidates.yaml
- Create: tests/test_visual_rendering.py
- Modify: docker-compose.yml
- Modify: orchestrator/rag/requirements.txt

- [ ] Step 1: Write rendering and adapter tests

The fixture renderer must turn a PDF page into a deterministic PNG hash, attach page_id and bbox coordinates, and preserve MinIO object keys. A candidate with missing checkpoint or license record returns failed before GPU initialization.

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_visual_rendering.py -q

Expected: FAIL because page rendering and candidate validation are absent.

- [ ] Step 3: Implement adapters without locking production

The candidate manifest stores candidate_id, model_id, revision, checkpoint URI, code license, weight license, expected dimensions, GPU memory estimate, and status. The renderer consumes MinerU page outputs; it never reparses the source PDF with Tika, Docling, Unstructured, or Marker. Add an optional visual service profile to Docker Compose but keep it disabled by default.

- [ ] Step 4: Verify and commit

Run: python -m pytest tests/test_visual_rendering.py -q and git diff --check. No model download occurs in ordinary tests.

~~~powershell
git add orchestrator/visual/render.py orchestrator/visual/encoders.py orchestrator/visual/candidates.yaml tests/test_visual_rendering.py docker-compose.yml orchestrator/rag/requirements.txt
git commit -m "feat(rag): add page rendering and visual candidate adapters"
~~~

### Task 3: Run the bounded bake-off and ColQwen2 rerank

**Files:**
- Create: orchestrator/visual/bakeoff.py
- Create: orchestrator/visual/colqwen_reranker.py
- Create: scripts/run-visual-bakeoff.ps1
- Create: tests/test_visual_bakeoff.py

- [ ] Step 1: Write offline report tests

~~~python
def test_bakeoff_report_marks_disabled_and_has_required_metrics():
    report = build_report([], status="disabled", reason="GPU unavailable")
    assert report["status"] == "disabled"
    assert report["bbox_hit_rate"] is None
    assert report["failed_pages"] == []
~~~

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_visual_bakeoff.py -q

Expected: FAIL because the report schema is absent.

- [ ] Step 3: Implement bounded pilot

scripts/run-visual-bakeoff.ps1 accepts an explicit input manifest containing 500-2,000 pages and at least 120 multimodal qrels. It records Recall@5, nDCG@5, bbox hit rate, P95 latency, peak GPU memory, index bytes/page, failed pages, model/revision, and license evidence. ColQwen2 receives only top 20-50 page candidates and returns disabled when GPU/model checks fail; it never writes patch vectors to the full ES index.

- [ ] Step 4: Run only as explicit integration

Run: $env:CODE_AGENT_RUN_VISUAL_PILOT=1; .\scripts\run-visual-bakeoff.ps1 -Manifest data\visual-pilot\manifest.json

Expected: a redacted JSON report. Production alias creation is refused unless the report meets the retrieval-evaluation plan thresholds.

- [ ] Step 5: Commit

~~~powershell
git add orchestrator/visual/bakeoff.py orchestrator/visual/colqwen_reranker.py scripts/run-visual-bakeoff.ps1 tests/test_visual_bakeoff.py
git commit -m "feat(rag): add bounded visual bake-off and ColQwen2 rerank"
~~~

## Rollback Boundary

Delete no source assets or old indices. If the pilot fails, retain the pilot report and physical index for analysis, keep visual status failed/disabled, and leave knowledge_page_visual_current absent.
