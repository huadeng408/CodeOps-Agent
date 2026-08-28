# Multimodal RAG Continuation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the existing MinerU page artifacts and modality-specific chunk policies into auditable, runnable multimodal RAG lanes on the local machine.

**Architecture:** Keep text/table/formula/audio evidence in the existing text-index contract and keep page/image vectors in a separate visual index. PDF input is always MinerU explicit OCR; visual pages consume only MinerU-rendered images and never call Tika. Every quality result is a receipt with model/data hashes and an explicit status boundary.

**Tech Stack:** Python 3.12, Pydantic, existing `EvidenceUnit`, MinerU OCR, CLIP pilot baseline, Faster-Whisper, Elasticsearch when Docker is available, pytest.

**Spec:** `docs/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md` and `docs/RESEARCH-2026-08-14-MULTIMODAL-RAG-MODALITIES.md`.

## Global Constraints

- PDF routes only through MinerU with explicit OCR; Tika is forbidden for PDF.
- Visual and text indexes remain physically separate; no visual alias is switched by this plan.
- New model/cache bytes go under `D:\\vscode\\localcode\\.runtime` or another D-drive path.
- BeeAPI/OpenAI concurrency remains at most 10; local visual/audio runs use batch one unless measured otherwise.
- API keys, transcripts, source contents and model answers never enter logs or receipts.
- Production-quality claims require human-reviewed qrels; fixture/public pilot scores remain scoped receipts.

---

### Task 1: Page visual EvidenceUnit and index projection

**Files:**
- Modify: `orchestrator/rag/visual/artifacts.py`
- Test: `tests/test_visual_artifacts.py`

**Interfaces:**
- `page_artifact_to_evidence(artifact: Mapping[str, Any], *, parser_name: str = "mineru", parser_version: str = "") -> EvidenceUnit`
- `page_artifact_to_index_document(artifact: Mapping[str, Any], *, visual_vector: list[float] | None = None) -> dict[str, Any]`

- [ ] **Step 1: Write failing tests** proving a page artifact with a valid source hash and asset hash becomes a visual `EvidenceUnit` with `page_id`, `asset_ref`, and stable child/parent IDs; prove the index projection rejects missing provenance and never emits text-index fields.
- [ ] **Step 2: Run `C:\Python312\python.exe -m pytest tests/test_visual_artifacts.py -q` and observe the expected missing-function failure.
- [ ] **Step 3: Implement the smallest conversion helpers using existing `EvidenceUnit`/`EvidenceCoordinates` and preserve the artifact's page and asset hashes.
- [ ] **Step 4: Re-run the focused tests and the existing evidence tests.

### Task 2: Real public visual pilot receipt

**Files:**
- Modify only if needed: `eval/scripts/run_docvqa_clip_pilot.py`
- Create: `eval_results/multimodal-rag/current-head-20260828-docvqa-clip/`
- Test: existing `tests/eval/test_docvqa_clip_pilot.py`

- [ ] **Step 1:** Run a bounded public DocVQA subset with the pinned CLIP revision, `limit=8`, `device=cpu` if GPU is occupied, and D-drive Hugging Face cache.
- [ ] **Step 2:** Record qrels, ranked pages, model/data revisions, runtime, hashes and explicit `PUBLIC_PILOT_ONLY` status; do not create or switch a production visual alias.
- [ ] **Step 3:** If network/model download fails, retry with command-scoped Clash proxy and retain the failure receipt rather than fabricating vectors.
- [ ] **Step 4:** Run the visual pilot tests and verify receipt checksums from a fresh process.

### Task 3: Audio quality gate without fabricated WER

**Files:**
- Modify: `orchestrator/rag/audio.py`
- Create: `scripts/rag/verify_audio_rag.py`
- Test: `tests/test_audio_rag.py`

- [ ] **Step 1:** Add tests for reference-transcript alignment requirements and for a receipt that is `QUALITY_BLOCKED` when no trusted reference transcript is provided.
- [ ] **Step 2:** Implement deterministic metadata-only WER calculation when a human/licensed reference is supplied; never infer WER from the ASR output itself.
- [ ] **Step 3:** Run the real cached Faster-Whisper fixture, persist timestamped EvidenceUnits and a receipt, and report WER as unavailable unless the reference is independently supplied.

### Task 4: Documentation and verification

- [ ] Append exact results, boundaries and blockers to the design map, `docs/PROGRESS-2026-08-27.md`, and the Obsidian mirror.
- [ ] Run focused Python tests, affected Go tests, `git diff --check`, and inspect `git status` before staging only task-owned files.
- [ ] Keep Docker/ES readback marked `BLOCKED` if Docker Desktop is still unavailable; do not delete volumes or caches.
