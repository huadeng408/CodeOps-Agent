# Archive index

This tree is historical context, not an execution entry. New conversations read `AGENT.md` and `docs/GOAL.md`. Open a file below only when tracing a specific receipt.

Do not treat progress logs, HANDOFF notes, scorer JSON dumps or old design maps as current acceptance.

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

`GOAL.md`, `DESIGN-MAP.md`, `provider-configuration.md`, `release-gate.md`, `MANUAL-REVIEW-PORTAL.md`, `manual-review-portal.html`, `observability-phoenix-live-trace-runbook.md`, `adr/`, `design/`, `releases/`.
