# O3 Trace Hardening Progress (2026-08-14)

## Scope

This update hardens the explicit O3 production-receipt path. It does not claim
an O1-O3 closure or a release benchmark result.

## Implemented

- `eval.run_o3` now performs read-only execution preflight for the Go RAG
  server, Phoenix, reranker, Elasticsearch health, read alias, physical target,
  nonempty corpus, and pinned corpus generation.
- The O3 preflight fails closed unless `knowledge_base_current` resolves to the
  single expected physical index `knowledge_base_v2_bge_m3`.
- Go server startup independently resolves that alias and gives the trace span
  its runtime-resolved physical target rather than merely recording the config
  assertion.
- Strict O3 exposes only `SearchKnowledge` to the model. Other built-in and
  MCP tools are not advertised, while generic harness behavior remains intact.

## Live Read-Only Evidence

- Elasticsearch cluster reported `yellow` (acceptable for this single-node
  development cluster).
- `knowledge_base_current` resolved only to `knowledge_base_v2_bge_m3`.
- The physical index contained 24,885 documents and its only observed
  `corpus_generation` was `techdocs-2026-07-30-v1`.
- Reranker `/health` reported `ready=true`; Phoenix `/healthz` returned HTTP
  200.

## Verification

- Focused Python O3/headless/harness/tool suite: `33 passed`.
- Focused Go suite (`cmd/server`, handler, service, telemetry): passed.
- `git diff --check`: no whitespace errors.

## Honest Boundary

No real model request and no canonical O3 receipt was produced in this update.
The current desktop command policy rejected attempts to inject
`ORCHESTRATOR_SHARED_SECRET` into the Go server process environment. That token
is required by the protected internal `SearchKnowledge` endpoint. Do not bypass
this by persisting a token in tracked config, weakening internal authentication,
or creating synthetic spans. Once a permitted secret-injection mechanism is
available, start Go with process-scoped `ORCHESTRATOR_SHARED_SECRET` and OTLP
settings, then run exactly one `python -m eval.run_o3 --execute` at relay
concurrency one.
