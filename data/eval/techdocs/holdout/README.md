# TechDocs Net-New Holdout Intake

This directory contains the holdout **contract**, not a holdout dataset. The
180 checked-in TechDocs queries are permanently development data. They must
not be copied, renamed, paraphrased, or resampled into this workflow.

## Required Human-Controlled Inputs

Create the following three files outside the repository, after the questions
have been authored and labeled without being shown to an evaluation agent.

`questions.jsonl` has one object per question:

```json
{"query_id":"holdout-001","query":"...","source_id":"go","language":"en","query_type":"concept"}
```

`qrels.jsonl` has one or more labels per question. Every question id must have
at least one label and no label may name an unknown question id:

```json
{"query_id":"holdout-001","document_id":"go@<pinned-commit>:doc/go_spec.html","section_path":["Types"],"relevance":1,"source_id":"go","language":"en","query_type":"concept"}
```

`attestation.json` is a truthful human statement. It is a semantic claim, not
a machine proof, and is preserved by hash:

```json
{"attestation_type":"HUMAN_NET_NEW","author_role":"independent_dataset_steward","created_utc":"2026-08-14T00:00:00Z","statement":"These questions were authored outside the agent-visible dev corpus and have not been used for tuning or scoring."}
```

## Seal And Run

Run the PowerShell wrapper with an output path that is also outside the
repository:

```powershell
./scripts/seal-holdout.ps1 -Questions D:\holdout\questions.jsonl -Qrels D:\holdout\qrels.jsonl -Attestation D:\holdout\attestation.json -Output D:\holdout\sealed-20260814
```

All three inputs and the sealed output are enforced to live outside the
repository. It writes only `evaluation-questions.jsonl` and
`holdout-manifest.json` to the sealed output. Qrels are deliberately not
copied there. The question view is an allowlisted projection, not a raw input
copy, and its hash is checked before scoring.

Only `load_verified_holdout_qrels(manifest, questions, qrels, attestation)`
may hand labels to a privileged holdout scorer; it re-runs the complete seal
verification immediately before returning labels. Generic/dev scorers that
accept an arbitrary qrels path are not holdout scoring paths and must never be
reported as such.

The manifest says `SEALED_NOT_SCORED` and
`HUMAN_ATTESTED_NOT_MACHINE_PROVABLE`. It does not certify model performance
or convert a semantic unseen claim into machine-verifiable evidence.
