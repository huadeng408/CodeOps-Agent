# 2026-08-14 Automatic Mainline Closure

## Scope And Boundary

This record closes the automatic work that could be executed on the current
`main` head without inventing human review, changing Docker Desktop's global
proxy, accepting gated data terms, or manufacturing an unseen holdout. It is
not a release claim.

## O3 Current-HEAD Receipt

- `VERIFIED`: `scripts/run-o3-receipt.ps1` produced
  `eval_results/o3/current-head-20260814-045502/trace-o3-66455d08` at
  `e1122b823a2cf81626c1862d7c5524700985f24c`.
- The real relay model was `gpt-5.6-sol` at concurrency `1`; the manifest
  correctly records `MODEL_IDENTITY_UNVERIFIED`, so this remains a
  non-release development smoke.
- Three public TechDocs instances (`go`, `docker`, `kubernetes`) completed.
  Their raw scorer outputs report `retrieved_relevant_document`; first
  relevant ranks were 1, 2, and 1.
- Phoenix API readback returned one trace with 33 ended spans. The shared v2
  O3 contract passed with `eval.run`, `eval.instance`, agent, tool, chat,
  retrieval, embedding, rerank, and official scorer kinds; no error spans or
  topology problems were reported.
- Recursive checksum verification returned `[]`, and the temporary Go server
  was stopped; port `8081` was not left listening.

## tau2-bench Official Source And Smoke

- `VERIFIED`: the official upstream source was isolated outside the repository
  at tag `v1.0.1`, commit
  `fc0055dc4e0a316c3f83133267fbd6faaa770992`, with MIT license. `uv sync`,
  `tau2 --help`, and `tau2 check-data` succeeded. The latter requires UTF-8
  process output on Windows because the upstream check emits a Unicode mark.
- `VERIFIED`: the bundled `data/` tree hash is
  `79a43301c4208e19f09d6ccacfcd8ba11e8511258dd6a404a4c6538b8b45fa23`.
  `tau2` is distinct from the old editable `tau_bench 0.1.0` checkout; legacy
  tau-bench scores remain development history only.
- `VERIFIED`: a real official `mock` run used `openai/gpt-5.6-sol`, seed 42,
  one task, one trial, and concurrency 1. The provider prefix was required by
  upstream LiteLLM. The run completed, but its official reward was `0.0`: the
  agent called `create_task` and the official environment recorded
  `action_match=false` / `db_match=false`. This is `OFFICIAL_FAILURE`, not a
  passing smoke.
- `VERIFIED`: the new isolated `eval.benchmarks.tau2official` boundary copies
  the raw official result into a canonical receipt and verifies its hash. The
  receipt at `eval_results/tau2official/current-head-20260814-050229` has raw
  result SHA-256
  `109e1331e7d9f61cb298ed68447757439ec83a600decc5882b9c8eace2f84799` and
  checksum verification `[]`.
- `IMPLEMENTED`: `Tau2OfficialRunner` never mutates the old adapter, requires
  a pinned source/data fingerprint, requires a LiteLLM provider prefix, caps
  concurrency at 10, and accepts only the public `mock` smoke domain. It is
  infrastructure for a future official pass, not evidence of one.

## Multimodal Bbox Eligibility

- `VERIFIED`: `likaixin/ScreenSpot-Pro` revision
  `210e78d3844251110bff86c95835ebd37a6930fa` is public, ungated, and MIT. Its
  screenshot/instruction/bbox/image-size schema is suitable for a separate
  GUI grounding lane. See `PROGRESS-2026-08-14-SCREENSPOT-PRO-BBOX-ELIGIBILITY.md`.
- `NOT_APPLICABLE`: ScreenSpot-Pro is not DocVQA, ViDoRe, document retrieval,
  multi-page evidence QA, or a visual-alias gate input. Its metrics must stay
  separate.
- `LICENSE_BLOCKED`: VisualMRC remains unavailable under its gated/internal
  evaluation terms. No terms were accepted and no bytes were downloaded.

## Remaining Non-Automatic Or External Gates

1. `BLOCKED`: Terminal-Bench needs Docker Desktop daemon proxy correction from
   `http.docker.internal:3128` to a reachable host proxy and a Docker Desktop
   restart. This is a global machine setting; it was not changed automatically.
2. `BLOCKED`: a tau2 official pass needs genuine agent behavior improvement;
   the 0-score raw receipt must remain in the evidence set.
3. `BLOCKED`: a document-native public question/page/evidence/bbox benchmark
   is still absent. GUI grounding cannot fill that gap.
4. `BLOCKED`: the qrel holdout is empty and the remaining semantic disputes
   require human decisions. GPT-5.6 Sol may only yield `AI_REVIEWED` or
   `DISPUTED`, never `HUMAN_REVIEWED`.

## Verification

```text
C:\Python312\python.exe -m pytest tests\eval\test_tau2_official_runner.py -q -rA
4 passed

C:\Python312\python.exe -m pytest tests\eval\test_benchmark_runners.py tests\eval\test_run_contract.py -q
35 passed, 1 upstream deprecation warning
```

## Four Workstreams

- Self-built Harness: 86% `[#########-]`. Current-HEAD O3 real receipt is
  complete; Terminal-Bench and a passing tau2 official smoke remain.
- Multimodal RAG: 80% `[########--]`. MinerU/OCR and visual retrieval pilots
  exist; a document-native bbox benchmark and visual alias gate remain.
- Evaluation set: 70% `[#######---]`. Pinned sources and real scorer receipts
  improved; human qrel review and a net-new holdout remain.
- Observability: 90% `[#########-]`. Current-HEAD Phoenix O3 parent chain is
  verified; long-running metrics and alerting remain.

The largest uncertainty is whether a corrected Docker Desktop daemon proxy
will reliably pull Terminal-Bench's uncached Ubuntu base image. The largest
likely omission is still a license-unambiguous document-native bbox dataset;
ScreenSpot-Pro does not answer that requirement.
