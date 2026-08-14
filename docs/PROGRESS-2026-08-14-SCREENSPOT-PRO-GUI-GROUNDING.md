# 2026-08-14 ScreenSpot-Pro GUI Grounding Micro-Receipt

## Scope

- Branch at start: `main`; baseline commit: `cca77e75`.
- This record adds a separately classified GUI-grounding receipt. It does not
  add PDF qrels, corpus data, an index, model score, review label, or service
  configuration.

## Verified source and result

- The pinned source identity recorded by the scorer is
  `likaixin/ScreenSpot-Pro@210e78d3844251110bff86c95835ebd37a6930fa` with MIT
  licence metadata.
- Python UTF-8 parsing read 55 annotations from `vscode_macos.json` and found
  `vscode_macos_0`. The annotation declares image size `[2560, 1664]` and bbox
  `[473, 183, 503, 219]`; PNG header inspection matched `[2560, 1664]`.
- The local PNG SHA-256 is
  `e2aba1cb9e3b31e3500178d0cebda30615e93fd74d1a3c3b353cc1dd5230cd1d`.
- `image_center_baseline` predicted `[1280.0, 832.0]`, outside the target
  box, so the one-sample rate is `0.0`. This is deliberately not a model score
  and must not be compared with RAG metrics.
- The report and receipt under `.tmp/screenspot-pro-210e78d/micro-receipt/`
  use SHA-256 inputs and output checksums. The report fixes
  `document_retrieval`, `pdf_rag_gate`, `answer_quality`, and `human_review`
  to `NOT_APPLICABLE`.
- The scorer rejects every sample not present in its fixed manifest, changed
  annotation or image bytes, a changed declared image path, and `NaN` or
  infinity bbox coordinates before it can write a report.

## Verification

```powershell
C:\Python312\python.exe -m pytest tests\eval\test_screenspot_grounding.py -q
# 6 passed in 0.14s

C:\Python312\python.exe -m py_compile eval\benchmarks\screenspot_grounding.py tests\eval\test_screenspot_grounding.py
# exit 0
```

## WebSRC correction

The inspected `WebSRC_v1.0_test.zip` is 130,295,695 bytes with SHA-256
`e85088f49499f77ce34825fa747b941d7643cdeefedadc902884ccf1c06b7501`.
Its `dataset.csv` has `question,id`, not an upstream question-to-element/bbox
join. No WebSRC qrels or converter were created; the earlier structural-lane
plan is superseded.

## Remaining gap and rollback

The project still lacks a licence-clear, document-native,
question/page/element/bbox source with real human review and a frozen clean
holdout. Removing this lane only means reverting its scorer, tests and records;
it does not affect Docker, MySQL, Elasticsearch, MinIO, aliases, qrels, or API
keys.
