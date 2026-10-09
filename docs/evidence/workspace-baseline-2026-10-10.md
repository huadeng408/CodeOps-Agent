# Working-copy baseline slice — 2026-10-10

Source: `612daf5417051666d1427533489157ef649f9fff`.
Issue [#4](https://github.com/huadeng408/CodeOps-Agent/issues/4): baseline
inspection **IMPLEMENTED**; complete Task Workspace acceptance **BLOCKED**.
The product command is `/worktree baseline`. Its scope and conservative
limits are documented in [workspace-baseline.md](../workspace-baseline.md).
It prepares no checkout and writes no independent Session or checkpoint state.

## Source and public-boundary checks

`go test ./... -count=1 -json`: exit **0**, 1,289 passed test actions,
54 skipped, 0 failed, 42 packages. Sources remained unchanged during the run.
Manifest: `output/playwright/gateway-final-go-1b3fc566-f14b-4b90-a8be-5662ddb73981/receipt.json`.
SHA-256: `47605d158a8d425f37e711189d3d09cd45bdcbf0a87253e2fa5f13aa37e51ed5`.

That full run preceded the final `core.excludesFile` override and its new
regression. After that small fix, the complete affected `internal/worktree`
package, public CLI test and `go vet ./...` were rerun, all exit **0**.
The affected package has one skipped pre-existing symlink test because this
Windows process lacks symlink privilege; actual source and metadata junction
denials ran and passed. Python/frontend/protobuf were unchanged in this slice;
their prior results are in [gateway evidence](token-gateway-2026-10-10.md),
not presented as new executions here.

Public tests establish dirty/new/empty/staged and unstaged deletion states,
original index preservation, stable and changed checksums, cancellation,
missing/non-repository refusal, Windows unsafe-root refusal, junctions,
unapproved Git metadata, includes and nested aliases, common credential
locations, generated-directory aggregation, source/output bounds, and the
external-ignore override. Source regression uses real Git and isolated
fixtures and makes zero paid model calls.

Standards and specification reviews found issues in metadata authorization,
unbounded stdout, credential locations and excluded-file counting. All were
fixed with public regressions and re-reviewed; both axes report **0 remaining
findings** for this slice. These are AI reviews. Exact staged paths and an
index-only secret scan passed; five files scanned, zero matching files, no
matched values printed. `git diff --cached --check` exited 0.

## Built production CLI, two processes

Build: `go build -o <run>/agent.exe ./cmd/agent`, exit **0**. Each process
receives `/worktree baseline` followed by `/exit` on stdin. The isolated
fixture repository has staged plus unstaged content, a new file, an empty
file, a deletion and a credential-path fixture. Local fixture configuration
disables model autostart and sandbox probing; no model or tool task runs.

Run `run-2e56fd32-e6d2-43df-8228-9be66cf9b95d`: **7/7 checks, exit 0**,
two OS processes (39316 and 50584), each exit 0. The checks cover distinct
production processes, command output, no file-content echo, stable checksum
after process restart, unchanged index bytes, unchanged source/deletion and
unchanged Git status/HEAD. The receipt binds the source SHA above and the
four changed source/test file hashes, unchanged during execution.

Receipt: `.runtime/e2e/workspace-baseline-runs/run-2e56fd32-e6d2-43df-8228-9be66cf9b95d/receipt.json`.
SHA-256: `8f4a5432e544b7a035379706832298683a33e561066daf04996cabf5d549a406`.
Trace is unknown, so the full acceptance receipt remains `BLOCKED`.
This is a real built CLI against a fixture repository, not one of the two
public coding tasks and not a browser preparation test.

Earlier CLI probes remain: `run-c5e6aa7b-024e-45b6-a033-d3de5cb7d804` and
`run-70c8b295-9d7f-4dbf-8f15-04e7b6e6693c`, each 7/7, source-hash bound to
its uncommitted slice; they do not replace the final committed-source run.

## Preserved failures and next criteria

The first public CLI regression failed because `/worktree baseline` did not
exist. Unsafe-root, unapproved metadata, generated-count, common credential,
include/alias and external-ignore regressions each failed before their fixes.
The first stdout-bound regression also found a real `bytes.Buffer` embedding
bug: promoted `ReadFrom` bypassed the custom `Write` limit. A private buffer
field fixed it, and the same public oversized-inventory test then passed.
No failed check was relabeled as runtime success or removed from the records.

The three UI previews remain sample-data artifacts, outside `frontend/`, at
`output/playwright/workspace-prototype-5e7f625f-475a-4784-a893-e383872507e2/`.
The user has not yet selected A/B/C. Prior approval of the capability sidebar
does not select this new workspace interface.

Still required: isolated current-copy seeding; host-approved storage outside
the user's source tree; content/policy validation before copying; explicit
managed metadata relations; retained failure/lease and Ledger CAS outcomes;
real process recovery and authenticated product prepare/view endpoints;
selected frontend, browser preparation and complete trace. #4 stays open.
No claim of atomic filesystem capture, completed isolation, safe proposal
application or the milestone's real coding tasks is made by this slice.
