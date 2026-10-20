# Archive index

This tree is historical context, not an execution entry. New conversations read `AGENTS.md` and `docs/GOAL.md`. Open a file below only when tracing a specific receipt.

Do not treat progress logs, HANDOFF notes, scorer JSON dumps or old design maps as current acceptance.

## Policy and navigation cleanup — 2026-10-20

Nine tracked originals are retained byte-for-byte in [policy-cleanup-2026-10-20/](policy-cleanup-2026-10-20/).
The [manifest](policy-cleanup-2026-10-20/manifest.json) records source paths, sizes and SHA-256 checksums.
They include the previous Goal, ticket navigation, two reuse reports and CSV, local-core and OpenSandbox
operator pages, RAG cutover runbook and manual-review instructions. Original statements are historical.
Current links remain at their ordinary entries; the long reuse CSV is available only in this archive.

Twelve additional machine-local originals were moved into the ignored `local-only/` subdirectory:
nine retired design pages, two old assessment/research copies, and the obsolete design checker bytecode.
The local manifest preserves every original identity and checksum. These private copies are not required
by a fresh clone. Restore only an explicitly chosen original to a non-conflicting path after checksum verification.

## Policy and navigation cleanup — 2026-10-14

Twelve originals are retained byte-for-byte in [policy-cleanup-2026-10-14/](policy-cleanup-2026-10-14/).
The [manifest](policy-cleanup-2026-10-14/manifest.json) records each original path, archive path, byte count and SHA-256.
This curated packet is tracked; it contains documents and retired navigation source, not runtime or model output.

| Originals | Historical scope |
| --- | --- |
| Design Map, `state.yaml`, `workplan.yaml`, checker and its test | Local draft navigation dependent on ignored scratch paths; retired checker source has a `.txt` suffix |
| Goal and task navigation | Before the acceptance-scope correction; old task status is a dated snapshot |
| Refactor plan and sandbox comparison | Original implementation starting point and candidate recommendation, before OpenSandbox was approved |
| Phoenix runbook | 2026-08-04 import and service conditions, without current execution authority |
| Token budget and 2026-10-09 cleanup audit | Old probe counts, blockers and cleanup process; current contracts remain at their active entries |

Current navigation is [DESIGN-MAP.md](../DESIGN-MAP.md); task states and dependencies come from GitHub Issues.
Restore an original only to an explicitly selected path after checking its manifest hash; preserve any newer content.
Four unused machine-local LFS hooks were separately retained under `.scratch/repository-qa-20261014/local-lfs-hooks/`.
Their local manifest permits restoration without changing other hooks or Git configuration.

## Cleanup snapshot — 2026-10-09

The audit is [repository-cleanup-2026-10-09.md](../repository-cleanup-2026-10-09.md).
Thirty artifacts were retained byte-for-byte locally: 24 individual moves and
6 snapshots, with matching before/after SHA-256. No run data or published
receipt was deleted. These additions remain gitignored and are local references,
not files a fresh clone is required to have.

| Local archive | Contents |
| --- | --- |
| `receipts/GOAL-log-2026-10-09.md` | Original 626-line Goal, including all old runs and failures |
| `design-map/cleanup-2026-10-09/` | Five original navigation/state/workplan/decision/workstream files |
| `plans/codeops-fast-alignment-2026-10-09/` | Five old task cards and one historical browser plan |
| `receipts/cleanup-2026-10-09/root-pointers/` | Two retired interview redirect pages; canonical interview receipts are unchanged |
| `run-artifacts/legacy-eval-scripts-2026-10-09/` | Sixteen unreferenced local experiment scripts (1,594 lines), kept as historical source |

The original tracked Goal is also available at [source 1c82efd](https://github.com/huadeng408/CodeOps-Agent/blob/1c82efd982842187c0b3ec8333cf817514f2dc91/docs/GOAL.md).
Its archived byte checksum is
`59f15d7bae3c8b1aa821914f12c3d20b9beed7218e62004684da63806bbb846c`.
The per-file action/checksum manifest is local at
`.scratch/repository-cleanup/archive-manifest.json`; open it only for restoring
this cleanup. Historical source is not an approved execution entry.

## Layout

| Path | What it holds | Default read? |
| --- | --- | --- |
| `receipts/` | Goal daily log snapshot, interview receipts, HANDOFF notes | no |
| `progress/` | Dated `PROGRESS-*.md` working logs | no |
| `plans/` | One-off plans plus archived `superpowers/` specs | no |
| `research/` | Interview research notes and product comparisons | no |
| `design-map/` | Frozen monolithic design map | no |
| `superseded/` | Recovered Claude Code / provider migration notes | no |
| `run-artifacts/` | Root-level scorer JSON and accidental scratch files | no |

Promoted, redacted release evidence stays under `data/eval/`. Live operator docs stay beside `docs/GOAL.md` (`provider-configuration.md`, `release-gate.md`, `MANUAL-REVIEW-PORTAL.md`, `releases/`).

## Receipts

| File | Summary |
| --- | --- |
| [receipts/GOAL-log-2026-10-11-before-instruction-consolidation.md](receipts/GOAL-log-2026-10-11-before-instruction-consolidation.md) | Exact live Goal snapshot before the six approved instruction/validation changes; historical test counts, blockers and implementation slices |
| [receipts/GOAL-log-2026-09.md](receipts/GOAL-log-2026-09.md) | Full Goal daily log through 2026-09-14, copied before the live file was slimmed |
| [receipts/interview-verified-2026-09-11.md](receipts/interview-verified-2026-09-11.md) | Interview conversation verification receipt |
| [receipts/interview-readiness-2026-09-10.md](receipts/interview-readiness-2026-09-10.md) | Interview readiness snapshot (provider route still blocked that day) |
| [receipts/HANDOFF-2026-08-12-O1-O3-TRACE-CLOSURE.md](receipts/HANDOFF-2026-08-12-O1-O3-TRACE-CLOSURE.md) | O1–O3 trace closure handoff |
| [receipts/HANDOFF-2026-08-02-MULTIMODAL-RAG-AGENT-PLATFORM.md](receipts/HANDOFF-2026-08-02-MULTIMODAL-RAG-AGENT-PLATFORM.md) | Multimodal RAG platform handoff |
| [receipts/HANDOFF-2026-08-02-REAL-CORPUS-IMPORT.md](receipts/HANDOFF-2026-08-02-REAL-CORPUS-IMPORT.md) | Real corpus import handoff |

## Progress logs

Dated working logs from 2026-07-30 through 2026-09-01, including the 2026-08-14 topic receipts. List: `docs/archive/progress/`. The 2026-08-31 snapshot is the stale design-map evidence pin, not current Goal state.

## Research and assessments

| File | Summary |
| --- | --- |
| [research/codex-claude-official-comparison.md](research/codex-claude-official-comparison.md) | Codex vs Claude Code official-behavior comparison |
| [research/production-agent-harness-assessment.md](research/production-agent-harness-assessment.md) | 2026-09-07 production Harness assessment |
| [research/RESEARCH-2026-08-14-MULTIMODAL-RAG-MODALITIES.md](research/RESEARCH-2026-08-14-MULTIMODAL-RAG-MODALITIES.md) | Four-modal RAG research notes |
| [research/interview-stories-2026.md](research/interview-stories-2026.md) | Interview story drafts |
| [research/interview-research-sources.md](research/interview-research-sources.md) | Interview source list |

## Design map and plans

- Frozen map: [design-map/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md](design-map/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md)
- Plans and specs: `plans/` and `plans/superpowers/`
- Archived OpenViking-inspired memory plan: `plans/openviking-memory-2026-10-06/` (historical; not the active CodeOps-Agent memory design)
- Recovered session notes: `superseded/CLAUDE-CODE-*.md`

## Run artifacts (local JSON)

Moved off the repository root so new conversations do not glob scorer dumps:

| Directory | Contents |
| --- | --- |
| `run-artifacts/swebench-scorer/` | `code-agent-*.swebench-*.json` official scorer summaries |
| `run-artifacts/native-scorer/` | native Windows scorer smoke JSON |
| `run-artifacts/deepseek-scorer/` | `deepseek-*.json` run summaries |
| `run-artifacts/humaneval/` | `eval_results_*.json*`, `eval_checkpoint_*.json` |
| `run-artifacts/playwright-scratch/` | Phoenix/playwright screenshots and snapshots |
| `run-artifacts/misc/` | Accidental eval-expression files (`el.parentElement.className`, `$taskDiag`, …) |

These remain gitignored. Canonical audited receipts stay in `data/eval/`. Regenerable trees `eval_results/`, `.runtime/`, `eval/swebench_work/` stay in those ignored directories.

## Leave at docs root

`GOAL.md`, `DESIGN-MAP.md`, `provider-configuration.md`, `release-gate.md`, `MANUAL-REVIEW-PORTAL.md`, `manual-review-portal.html`, `observability-phoenix-live-trace-runbook.md`, `adr/`, `releases/`.
The retired local `design/` tree is historical and no longer belongs at the active docs root.
