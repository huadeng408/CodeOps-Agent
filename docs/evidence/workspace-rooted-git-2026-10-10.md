# Rooted Git preparation evidence — 2026-10-10

Engineering state: `IMPLEMENTED`; full Issue #4 acceptance: `BLOCKED`.
This slice replaces native Git reads/registration in baseline inspection and
task preparation. The approved A layout and original user changes are preserved.
Prior [preparation evidence](workspace-preparation-2026-10-10.md) and all failures
remain historical evidence; they are not inherited as current verification.

## Implementation and reuse

The Git reader sees a bounded captured inventory through an `os.Root` read-only
filesystem. Paths inserted later do not enter that namespace; writes, absolute
paths, unsafe aliases and chroot escapes are rejected. Windows metadata/root
pins remain in place. Go creates the existing detached locked worktree relation
and HEAD index with rooted exclusive writes under the Ledger preparation intent.
Legacy native manager callers remain unchanged.

The reader reuses go-git **v5.19.3**, go-billy **v5.9.2** (Apache-2.0) and the
already selected gcfg parser (BSD). NOTICE retains their applicable terms.
The [pinned go-git release](https://github.com/go-git/go-git/releases/tag/v5.19.3)
requires Go 1.26+ and includes delta/scanner security fixes. Dependency versions
are authoritative in go.mod/go.sum; CI reads its Go version from go.mod.

Objects use a bounded loose header/body and go-git Scanner/PatchDelta codecs,
not the unbounded Storage/objfile allocation path. Per-object/delta result limits,
an aggregate decode reservation, checksum verification, cycle and depth refusal
precede object decoding. Version 4 index reconstruction is also preflighted.
Trees are decoded as needed; missing children cannot produce a partial baseline.

Ignore matching uses Go's RE2 implementation with Git byte wildcard semantics,
immutable parent scopes and aggregate rule limits. Repository config/worktree
config, bare vs explicit-empty bools, nested rules, negation and escapes have
native Git comparisons. Git LFS wildmatch was evaluated and removed because its
recursive matcher has unbounded backtracking. No new Session facts or Python
side effects were introduced. Exact compatibility limits are in
[workspace-baseline.md](../workspace-baseline.md).

## Source checks at 0b7b3eae (before Linux CI fixes)

The initial fixed point is `f240e7def34882116c28b013bcb0fe937885db9d`.
Tested source: **`0b7b3eae05600ab91eae6f3fe769891aedb4ddbf`**. Pre-commit runs
bind exact tracked/unignored source hashes. The post-commit audit checks every
831/460 source entry against the unchanged working bytes and that commit's blobs;
Git CRLF conversion is normalized only for the committed-blob comparison. Original
run receipts are not rewritten. Binding receipt:
`output/playwright/workspace-rooted-review/source-binding.json`, SHA-256
`f36d20812cd2d733f7143670a7648b824ad4ec9398b559c76570e2aadea09118`.
The subsequent evidence-only commit changes no tested source, dependency or asset.

- Targeted Go checks cover packed/no-native Git inspection and preparation,
  native worktree/index reopening, missing trees, snapshot insertion/replacement,
  loose/delta bounds, cycles, expanded v4 indexes, ignore computation/UTF-8/config.
  Commands and exit 0 outputs are retained in the session and review artifacts.
- Related full worktree/session/handler/serverconfig/localidentity/safety suites
  pass, exit 0, before the final inventory-count compatibility fix. That last
  fix also passes the existing public CLI baseline test, exit 0.
- Final `go vet ./...`, `go mod verify` and `git diff --check`: exit 0.
- Final `go test ./... -count=1 -json`: **1,350 passed / 54 skipped / 0 failed
  test actions**, 43 passing packages, exit 0. All **831** Go/Python/protobuf
  and dependency-file hashes were unchanged during the run. Run:
  `workspace-rooted-go-c68b2d8a-fe09-49a0-8ff9-06464e4a200f`, receipt at
  `output/playwright/<run>/receipt.json`. Receipt SHA-256:
  `d5205697e7da5f5ad712e920d5fe16ae903863cda6ac7160d493a9274bca87d8`.
  Log SHA-256: `9ad3af565f361f0b79a7612b94122647707ea23f2b38e347a651e56601f1e4f4`.
  This is source regression, not a real coding-task or release receipt.

Actual browser command:

```text
CODE_AGENT_RUN_LOCAL_CORE_E2E=1 CODE_AGENT_E2E_WORKSPACE=1 CODE_AGENT_BROWSER_HEADLESS=1 node tests/e2e/local_core_browser.mjs
```

Final run `local-core-87e92d9e-fbd8-4584-890b-03bd702a16ee`: **13/13, exit 0**.
The server child processes have an empty PATH, so preparation does not depend
on native Git. The built server/browser exercise approval/auth, current dirty/
new/empty/deleted state, unchanged original HEAD/status/index/content, CAS,
retained workspaces, restart and modified-copy refusal. The input is a controlled
Git fixture; it is not a real model coding task. Model calls: 0; Trace backend:
unknown. The receipt binds binary/assets/screenshots, two process outcomes,
all checks and unchanged source/assets. Runtime files stay ignored.

Receipt: `output/playwright/local-core-87e92d9e-fbd8-4584-890b-03bd702a16ee/receipt.json`.
Receipt SHA-256: `dbe187a0ce01ad3364e78efc6c80a1b970edf79e00c1c0676b07fbcfe64923ff`.
Go PIDs: **26564, 31796**; **460** source hashes. Source binding is recorded above.
The previous run `local-core-b60d55f6-30c7-4c56-9b19-483d75f40023` also passed
13/13, exit 0, but preceded the CLI count fix and is not final-source evidence.

## Failures and two-axis review

Failed cases remain; successful subsets do not replace their denominators:

- Initial no-native public tests: 2/2 failed before this change (exit 1).
- First native registration interop check failed on Windows backslash pointer
  formatting; Go now writes the standard forward-slash relation (exit 0).
- Missing nested tree regression first failed because TreeWalker could hide
  the error; explicit bounded traversal now refuses it (exit 0).
- Three native ignore regressions failed before matcher/config fixes; the
  expanded public and independent matrix now pass.
- Intermediate UTF-8 literal, case-fold and empty-bool comparisons failed;
  original-byte mapping and gcfg's blank-value flag fixed those mismatches.
- First complete Go run: **1,347 passed / 54 skipped / 1 failed test action,
  42 passing packages, exit 1**, source hashes unchanged. Run
  `workspace-rooted-go-a10aa679-8d95-4bd7-9b53-2ccd0e444a14`, receipt under
  `output/playwright/<run>/receipt.json`, SHA-256
  `ffe5a2eb02b615c7d6254abffd6584daa4de2483024c56837a2a500a2c86cc0b`.
  The public CLI expected four files and
  one exclusion, but the new inventory counted already-ignored generated dirs.
  Filtering untracked Git-ignored paths before exclusion accounting fixes the
  shared reader; the existing CLI regression then exits 0. No fixture was deleted.
- Intermediate Go run `workspace-rooted-go-251ef77f-7c1e-4dff-b4ac-78768920f09a`
  passed 1,348 / skipped 54, exit 0; browser
  `local-core-abd27ee3-afdb-4709-8273-2727c6bcf1d6` passed 13/13, exit 0.
  Both precede the final positive-character-range correction. Final source
  removes `/` from all parsed wildcard ranges using
  `regexp/syntax`. Native separator/non-separator regressions pass. The complete
  Go and browser runs above follow this final source change.

**Standards:** no remaining definite findings in the implemented slice.
Object/index pre-codec limits, RE2 computation bounds and negated-class separator
handling addressed all three confirmed P1 findings. Final ordering adjustment
preserves tracked exclusions and content/traversal boundaries.

**Spec:** no remaining definite findings in the implemented slice. Earlier independent
native comparisons plus public UTF-8/no-native worktree tests: **53/53, exit 0**;
three post-ordering ignore/credential checks: **3/3, exit 0**. Final-source range,
negation, POSIX and UTF-8 check: **5/5, exit 0**. Source review is
`AI_REVIEWED`, not `HUMAN_REVIEWED`. Review logs remain ignored at
`output/playwright/workspace-rooted-review/`:

| Artifact | SHA-256 |
| --- | --- |
| `spec-final.log` | `9b4fec897fe85af74ff7652115429afa3887f148942fea2920622202cf259869` |
| `failure-tool-excerpts.log` | `5bf0a8bfa9d184e265c2df16c89a5e9573ca002f64d7b871de6e8be82843e6fd` |

The failure log is a tool-output excerpt, not a complete runtime receipt.
The external review overlay and original logs are retained outside the repo.

## Linux CI failure and correction

[Run 38040174277](https://github.com/huadeng408/CodeOps-Agent/actions/runs/38040174277)
tested `84d5f31bec27eefe806f1361f47242a20007b879`: Python and Frontend succeeded;
Go failed with two failing test cases, exit 1, and vet was skipped. The useful
source checks stay enabled; no workflow gate was removed.

The ignore parser inserted `/` into a negated class before parsing it. This
changed `[!-z]` (literal leading hyphen and `z`) into a different range. Windows'
default `core.ignorecase=true` masked the mismatch. Slash exclusion now happens
only on the parsed AST, preserving Git's original class syntax. Native comparisons
explicitly set both case modes and also cover the positive range `[ -z]`.

The recovery fixture assumed exactly two failed lifecycle calls, but a background
terminal observer can also retry. The fixture now keeps failure active until the
old runner is closed and drained. Recovery must still add exactly one callback
and one durable acknowledgement; repeated recovery must not call it again.
Production retry, authorization and Session Ledger logic are unchanged.

Fresh correction checks, all exit 0:

- `go test ./internal/session -run TestIndependentAgentRecoveryRetriesWorkspaceLifecycleUntilAcknowledged -count=100`:
  100 successful repetitions.
- `go test ./internal/worktree ./internal/session -count=1`: both packages pass.
- `go vet ./...`, `gofmt -l` for the three modified Go files and
  `git diff --check`: clean.
- `go test ./... -count=1 -json`: **1,352 passed / 54 skipped / 0 failed test
  actions**, 43 passing packages. All 831 source/dependency hashes stayed unchanged.
  Run `workspace-rooted-go-5d6b5167-42bb-469e-8037-f991025595f4`; receipt at
  `output/playwright/<run>/receipt.json`, SHA-256
  `f18c92d3af9f7c9608d4f50b23cd62cb19b2b9331481eae070141a48de7da676`;
  log SHA-256 `766ec95267a2dc40a61a2c1d269f3660c1fee6dbfde93ca42fa9404451f77b1c`.
- Browser command above: **13/13**, run
  `local-core-08df07be-dfb4-4d24-aab1-8e310a275690`; receipt at
  `output/playwright/<run>/receipt.json`, SHA-256
  `ad2ef7e5b4d3b039df0cd6f9c1701a0f1d37be9f59f92762ec056012fd0966a4`.
  Go PIDs 27804/32716; 460 source hashes, unchanged assets, empty server PATH,
  fixture input, model calls 0, Trace unknown. Original workspace/index remain
  unchanged. The restarted browser screenshot was also inspected.

These runs recorded the parent HEAD `84d5f31b` plus exact modified-source hashes;
committed-source binding is recorded after the correction commit. Both review
axes found no remaining definite issue in the correction (`AI_REVIEWED`).
The original failed CI remains at `output/playwright/github-ci-38040174277/`:
`go-failure.log` SHA-256 `282de5ad26fc2dac9c41b59d100872beea11ad13c1a8837e33d78c8350cc4414`,
`run.json` SHA-256 `72acfcc428f205aa195d74532d983a3ed87b7bf751ad714a1beac5ba9397b299`.
Fresh Linux CI is recorded independently by GitHub against the pushed SHA.

## Acceptance boundary

The named native namespace read has been removed from this preparation path;
the bounded reader cannot discover optional metadata added after capture.
These checks do not prove the entire production recovery/race matrix or grant
execution. Production tool admission, real coding tasks, ten-turn conversations,
complete Go/Python Trace and the milestone remain `BLOCKED`. SHA-256 repositories
and product preparation on non-Windows remain explicit refusals. The Python and
frontend suites were not rerun locally in this backend-only slice; Linux CI is
recorded separately after pushing. Paid model calls this slice: **0**; the shared
token batch is unchanged, not reset.
