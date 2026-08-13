# O3 current-HEAD receipt - 2026-08-14

## Status

- IMPLEMENTED: Go retrieval span is now `rag.retrieve orchestrator /knowledge-search`; handler telemetry regression test locks the contract.
- IMPLEMENTED: removed the hard-coded 5 second HTTP timeout from `pkg/reranker`. SearchService context now owns the configured rerank deadline.
- IMPLEMENTED: O3 runtime uses `rerank_topn=5` and `rerank_timeout_ms=30000`; reranker remains serialized with two threads.
- VERIFIED: `go test ./pkg/reranker ./internal/handler ./internal/service ./internal/telemetry/genai -count=1` passed.
- VERIFIED: real reranker warm probes returned HTTP 200.
- VERIFIED: real single-concurrency `gpt-5.6-sol` O3 receipt at `eval_results/o3/trace-o3-0a3a8b60` completed 1/1 with 0 failures.
- VERIFIED: Phoenix readback returned 10 spans; shared trace contract is `PASS`, with no missing kinds or problems.
- VERIFIED: official scorer `retrieved_relevant_document`, first relevant rank 1, and real rerank applied.
- VERIFIED: 9 receipt artifacts were checksummed; the short-lived Go server was cleaned up and port 8081 is free.

## Evidence boundary

The manifest marks this as `non_release_dev_smoke` and `MODEL_IDENTITY_UNVERIFIED` because the provider did not return an immutable model revision. This is not release-level multi-dataset evaluation or human review. Earlier failed receipts remain unchanged for audit.

## Four workstreams

- 自研 Harness 78%: real O3 execution, scorer, trace/artifact/checksum gates verified; multi-instance replay and release gates remain.
- 多模态 RAG 64%: text retrieval, embedding, rerank and MinerU/OCR routing verified; visual index, bbox bake-off and multimodal qrels remain.
- 评测集 58%: pinned techdocs smoke verified; multi-dataset statistics and independent review remain.
- 可观测性 82%: Phoenix readback and unified contract verified; long-running metrics, alerts and aggregation remain.

Next priority: multi-instance O3 with concurrency <=10, visual index and multimodal qrels, release evaluation and human review, then long-running observability. Biggest uncertainty is reranker p95 on long chunks, cold starts and low-memory hosts. Biggest omission is current-HEAD first-turn relay compatibility and long-running agent acceptance.
