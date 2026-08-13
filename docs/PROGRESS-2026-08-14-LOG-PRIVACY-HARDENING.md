# 2026-08-14 Production Log Privacy Hardening

## Scope

The real O3 multi-instance receipt exposed a production observability defect:
the Go HTTP access logger retained complete request and response bodies. A
`knowledge-search` request can carry a user query, while its response can carry
retrieved document text. Those values must not be copied into stdout/application
logs.

This change is a logging and error-boundary hardening only. It does not alter
retrieval queries, model inputs, MinerU/OCR routing, RAG results, scoring, or
trace topology.

## Root Cause

- `internal/middleware.RequestLogger` read, buffered, and emitted every Gin
  request and response body. Key-based JSON redaction could not safely cover
  free-text fields such as `query`, `textContent`, `bboxRefs`, or future
  document payload shapes.
- Retrieval and orchestrator failure diagnostics in handlers/services wrote raw
  queries.
- Some downstream HTTP clients placed non-200 response bodies into returned
  errors. Callers may log those errors, allowing an upstream to echo prompt or
  document content into application logs.

## IMPLEMENTED

- Access logging is metadata-only: status, latency, client IP, method, path,
  request byte count, and response byte count. The middleware does not consume,
  cache, parse, redact, or emit request/response contents.
- Retrieval diagnostics now use the shared `genai.HashQuery` value as
  `query_hash`/`queryHash`; raw query and phrase fields are removed from log
  records in search, memory, chat, evidence expansion, orchestrator support,
  and HTTP handlers.
- Stream, memory, ingestion, reranker, and Elasticsearch error boundaries now
  retain status plus `body_bytes`, not remote response text.
- The real request payloads remain unchanged. PDF routing remains MinerU plus
  explicit OCR; Tika remains restricted to non-PDF Office formats.

## VERIFIED

TDD red evidence was recorded before the implementation:

1. `TestRequestLoggerDoesNotPersistKnowledgeSearchBodies` previously observed
   the private request query and private retrieved document body in the access
   log.
2. `TestRetrievalMetricsLogsOnlyQueryHash` previously observed query,
   normalized query, and raw phrase fields in retrieval metrics.
3. Non-200 client regression tests previously observed private remote failure
   bodies in stream, memory, ingestion, and reranker errors.

Fresh green verification at the current worktree:

```text
go test ./internal/middleware ./internal/service ./internal/handler \
  ./pkg/orchestrator ./internal/telemetry/genai ./internal/rag -count=1

go test ./pkg/orchestrator ./pkg/reranker \
  -run 'Test(StreamResponse|MemoryClient|IngestionClient|Rerank)DoesNotExposeNonOKResponseBody' \
  -count=1

go test ./... -count=1
```

All commands exited 0. The full Go suite completed in 41.9 seconds. `git diff
--check` has no whitespace errors.

## Boundary And Rollback

This is not a statement that all possible application data is safe to log.
Low-level Elasticsearch package errors outside the request/retrieval path still
need a separately scoped audit because they can carry remote body text. The
same applies to tool-level fetch/parser diagnostics whose response content is
part of their explicit operation contract.

Rollback is limited to this change's Go files and tests. It does not mutate
Docker containers, database rows, Elasticsearch indexes/aliases, MinIO objects,
qrels, evaluation artifacts, prompts, model configuration, or secrets.

## Four-Workstream Snapshot

- Harness: 81% `[########--]`. The three-source current O3 receipt is verified;
  release-scale official benchmark samples and long-run recovery statistics are
  still absent.
- Multimodal RAG: 64% `[######----]`. MinerU plus explicit OCR and text RAG are
  evidenced; visual index, 120 multimodal qrels, and ViDoRe/bbox bake-off are
  not complete.
- Evaluation set: 60% `[######----]`. Recoverable relay failures were retried;
  valid Sol disagreements still require source-evidence adjudication and real
  human review.
- Observability: 86% `[#########-]`. The cross-source O3 Phoenix receipt and
  log content minimization are verified; long-horizon metrics/alerts and a
  broader low-level error-body audit remain.

Highest-impact next work: visual index plus locked multimodal qrels, then
ViDoRe/bbox bake-off and release-grade independent evaluation. The least certain
area is visual retrieval quality because no real visual encoder/index receipt
exists. The largest omission is still independent human review and an unseen,
contamination-controlled release holdout.
