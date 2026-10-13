# Repository cleanup audit — 2026-10-09

Status: **IMPLEMENTED** (engineering cleanup). Product runtime and release
acceptance remain **BLOCKED**. Audit baseline:
`1c82efd982842187c0b3ec8333cf817514f2dc91`.

## Scope and counts

The inventory covers 1,041 tracked paths: 807 source/test paths, 43 document
paths, 5 tracked archive paths and 186 other configuration/data paths. This is
a path inventory and scoped caller/reference audit, not proof that every
symbol is useful or every execution path is correct.

| Confirmed finding | Count | Action |
| --- | ---: | --- |
| Local experiment scripts without active source/test/configuration callers | 16 files / 1,594 lines | Moved to a local historical archive |
| Superseded local alignment task cards/browser plan | 6 files | Archived; new planning follows formal spec #1 |
| Old interview redirect pages at the docs root | 2 files | Archived; canonical interview receipts retained |
| Overgrown current Goal containing dated runtime logs | 1 file / 626 original lines | Full byte-preserving snapshot; current contract and gaps retained |
| Stale design navigation/state/workplan/decision/workstream | 5 files | Original snapshots retained; active navigation clarified |
| Permanently skipped manual release job | 1 job | Manual-only audit now executes the existing fail-closed gate |
| Exact source hash group consisting of empty package markers | 3 files | Retained as Python package boundaries |
| Internal/pkg Go packages with no incoming repository import | 0 of 52 inventoried packages | No production package deleted on speculation |

Thirty archived artifacts have matching before/after SHA-256: 24 individual
moves and 6 snapshots. The original Goal, navigation and task history remain
recoverable. No recursive delete, database cleanup, container/volume removal,
benchmark-data deletion or run-tree pruning was performed.

## Findings and completed changes

1. **Historical process conditions looked like current prerequisites.** The
   Goal mixed old missing-provider conditions, dated successful runs and
   incomplete receipts with active acceptance. Its original text is retained;
   the live Goal now separates milestone design, current evidence gaps and
   persistent project thresholds. Runtime preparation must check current
   credentials and sandbox conditions instead of inheriting an old absence.

2. **Local design state claimed independent authority.** Its freshness pin
   remained 2026-08-31 and its source-of-truth field pointed to itself. The
   corrected map/state are navigation and planning projections of the active
   Goal. The structural design-map checker remains useful but does not prove
   runtime freshness or release eligibility. The superseded baseline DAG is
   preserved in its original snapshot.

3. **Ignored experimental runners invited accidental reuse.** Active tracked
   source, tests and configuration, plus local design/planning references, were
   checked before moving the sixteen scripts. A similarly named prediction
   directory in the retained runner is an artifact reference, not a call to
   the removed script. The historically referenced `run_swebench_honest_10.py`
   remains; all active product, benchmark adapters and official scorer paths
   remain. Archived source is reference material, not an execution entry.

4. **Old task cards competed with the approved product scope.** Their five
   implementation cards and failed browser acceptance plan are preserved
   verbatim. Current drafting starts from [spec #1](https://github.com/huadeng408/CodeOps-Agent/issues/1).
   Prior failures and acceptance limits are not rewritten into new successes.

5. **The hosted release workflow was a no-op.** Its unconditional false job
   condition is removed. It still runs only on deliberate manual dispatch;
   ordinary push/PR CI is unchanged. It uses the already selected pinned
   Actions and Ubuntu runner, prepares Go modules before offline evaluation
   tests, and executes the same release gate. Missing valid runtime/scorer
   receipts fail with `BLOCKED`; no secret provisioning or receipt generation
   is implied. Hosted execution is not claimed by this cleanup.

## Retained contracts and gates

- Go authorization, owner/session binding, Ledger append-only/CAS, sandbox,
  workspace/symlink/reparse validation, MCP trust and unknown-effect handling.
- Ordinary Go/Python/frontend CI and all seven release evidence lanes.
- Full 200-round browser/compaction/reconnect and production CLI/HTTP recovery.
- 8 Workers, 200 long tasks, 30 real faults, at least 98.5% recovery; 40 Skills,
  1,000 cases, at least 948 correct; at least 60% input-token reduction with no
  outcome regression; official SWE at least 18/20 against the pinned 8/20 baseline.
- Active RAG cutover, operator instructions, provider/trace guidance, Memory
  contracts and both reuse inventories, which still have distinct consumers.
- Existing snapshots, approved data, raw failures, public benchmark inputs,
  compatibility adapters and package markers with actual contract roles.

## Reproducibility and restoration

Local audit artifacts live under `.scratch/repository-cleanup/`: inventory,
caller/reference scan results, Go-package reference map, archive manifest and
verification receipt. They remain ignored engineering artifacts. The archive
manifest lists relative source/destination paths, actions, byte/line counts and
SHA-256; restoration checks those hashes and refuses to overwrite new work.
The [archive index](archive/INDEX.md) names the historical groups.

The tracked original Goal is independently recoverable from
[baseline source](https://github.com/huadeng408/CodeOps-Agent/blob/1c82efd982842187c0b3ec8333cf817514f2dc91/docs/GOAL.md).
Its archived SHA-256 is
`59f15d7bae3c8b1aa821914f12c3d20b9beed7218e62004684da63806bbb846c`.
Local-only archives are not required files of a fresh public clone and are not
silently promoted into published release evidence.

## Validation boundary

Verify archive hashes and removed-path/caller consistency, current threshold
parity, local navigation structure, affected workflow/evidence regressions and
diff hygiene. Record commands, exit codes and full test counts in the local
verification receipt. These are engineering checks, not provider-backed E2E.

No symbol-level dead-code elimination, paid-model run, browser product E2E,
official scorer, hosted manual release run or full Go/frontend suite is claimed.
Production code was not changed. A dynamically invoked local experiment outside
the audited callers may need its original path restored from the archive.
