# Relay fast-model compatibility - 2026-08-14

## Root cause and fix

- The orchestrator defaulted `MODEL_FAST` to `gpt-4o-mini`, while the configured BeeAPI relay did not expose that model. A prior proposed `deepseek-v4-flash` value was also disproven by the live `/v1/models` response: the relay exposes `gpt-5.4`, `gpt-5.5`, `gpt-5.6-sol`, `gpt-5.6-terra`, and compact variants only.
- **IMPLEMENTED**: default fast model is now `gpt-5.5-openai-compact` in both Python and Go defaults. Existing explicit `MODEL_FAST` overrides and empty-value disable behavior remain unchanged.
- **IMPLEMENTED**: provider routing keeps relay compact models on the OpenAI-compatible client path.
- **VERIFIED**: TDD regression first failed against the old default, then passed after the minimal fix. Python provider tests, Go config tests, CLI/orchestrator tests all pass.
- **VERIFIED**: real BeeAPI request with the BeeAPI-scoped key returned HTTP success and text `OK`; provider constructed `OpenAIClient(model=gpt-5.5-openai-compact, base_url=https://beeapi.ai/v1)`. The key existed only in the child process environment and was cleared after the probe.

## Evidence boundary

The first probe intentionally using the first file key returned real `401 invalid api key`; the corrected BeeAPI-labelled key returned success. A `deepseek-v4-flash` request returned real `404 no enabled channel`, so no DeepSeek default was retained. No qrels, indexes, databases, Docker volumes, or review labels changed.

## Four workstreams

- 自研 Harness 79% `[########--]`: current-HEAD O3 and first-turn relay compatibility verified; multi-instance replay and release gates remain.
- 多模态 RAG 64% `[######----]`: text/RAG and MinerU/OCR paths verified; visual index, bbox bake-off, and multimodal qrels remain.
- 评测集 58% `[######----]`: pinned techdocs smoke verified; multi-dataset release statistics, disputed adjudication, and human review remain.
- 可观测性 82% `[########--]`: Phoenix contract/readback verified; long-running metrics, alerts, and cross-instance aggregation remain.

Next priority is multi-instance O3 at aggregate concurrency <=10, followed by visual index/qrels and release evaluation. Remaining uncertainty is first-turn behavior with tools and long-running relay sessions, not basic fast-model HTTP compatibility. The largest omission is still the absence of release-level multimodal and hidden-holdout evidence.
