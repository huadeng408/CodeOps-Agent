# 2026-08-14 Page-Qrels Release Gate

## Scope

This increment adds the missing executable transition between an auditable
human-reviewed page-Qrels draft and a scoreable release artifact. It does not
create any review decision, contamination result, split, prediction, visual
alias, or benchmark score.

## Implemented

`orchestrator.eval.multimodal_page_qrels.release_human_reviewed_page_qrels()`
is the only supported release transition. It refuses to write an output unless
all of these inputs bind to the exact source `qrels.jsonl` SHA-256:

1. The source is rebuilt in a temporary directory by rerunning the existing
   signed human-review materializer against the supplied candidates, decisions,
   query links, license allowlist, and explicit MinerU OCR receipts. The
   rebuilt Qrels bytes must exactly match the proposed source. A fabricated
   manifest or recalculated Qrels hash is insufficient.
2. There are at least 120 unique, non-empty query IDs.
3. A frozen `test` split receipt is signed by the configured trusted release
   workflow key and declares `split_frozen: true`.
4. A signed contamination receipt binds an existing raw report, declares
   `CLEAN`, and includes all four completed layers:
   `exact`, `containment`, `minhash`, and `embedding`.
5. A signed independent scorer receipt identifies the scorer and binds an
   existing prediction artifact hash.

On success it copies the immutable Qrels bytes into a new output directory and
writes `HUMAN_REVIEWED_PAGE_QRELS_RELEASED`; the release manifest contains the
source and all three receipt hashes. Existing inputs and aliases are never
modified.

## Verification

The tests were written first. Before implementation they failed because no
release API existed. After the minimal implementation:

```text
PYTEST_DISABLE_PLUGIN_AUTOLOAD=1 C:\Python312\python.exe -m pytest \
  tests\eval\test_multimodal_page_qrels.py -q

32 passed in 4.14s
```

The regression proves a 119-query draft raises
`PAGE_QRELS_MINIMUM_QUERY_COUNT`; rewritten Qrels with a recalculated manifest
digest fail rebuild comparison; tampered signed contamination receipts fail
signature validation; and a valid 120-query source with all required signed
receipts produces the release manifest and preserves every receipt digest.

## Current Boundary

The real DUDE artifact remains a 20-query
`HUMAN_REVIEWED_PAGE_QRELS_NOT_RELEASED` draft. It cannot be promoted through
this gate. The required 120-query split freeze, clean full contamination scan,
and independent scorer receipt have not been supplied, so no scoreable
multimodal Qrels or visual-alias claim is made by this change.
