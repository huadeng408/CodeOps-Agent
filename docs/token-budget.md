# Verification token batch

The authorized batch limit is **100,000,000 input plus output tokens**. This
replaces the earlier CNY100 limit. The cap is an allowance, not a target to
consume. Prices and currency conversion remain independent accounting data;
an unknown price must not be recorded as zero.

## Current implementation

`internal/admission.Budget` records the batch, reservations, settlements,
unknown outcomes and task completion as append-only events in the existing
Session Ledger. It creates no separate billing database or mutable balance
file. All Harness processes participating in this batch must open the same
approved canonical Ledger; separate databases are not coordinated by this
module.

- The batch identity and cumulative allowance survive process restart.
- Only one task can hold the batch slot, including across different owners.
  At most ten unsettled calls can hold relay slots within that task.
- Each upstream attempt needs a new call ID and a confirmed token upper bound.
  A duplicate reservation refuses another dispatch. A repeated identical
  settlement does not count consumption again.
- Missing usage preserves the complete reservation and blocks further calls.
  Confirmed usage can reconcile a missing response. Reported usage above its
  reservation records a bound violation and stays blocked; a smaller later
  settlement cannot silently erase that observation.
- A task can release its slot only after all its calls settle. Completion
  preserves the used allowance; a delayed auxiliary call cannot reopen it.
- The authenticated owner must match when settling or completing a task.
  Ledger replay checks both the hash chain and admission transitions.

The Go caller remains responsible for authenticating the owner, issuing task
and attempt identities, confirming the provider's upper bound, and obtaining
attributable usage. Input totals must include cache tokens once; output totals
must include reasoning tokens once. This module does not infer either total
from missing provider fields and does not calculate money from a model name.

## Model invocation migration

Engineering status: **IMPLEMENTED** for the Ledger module and opt-in invocation
migration; **BLOCKED** for Issue #3's complete product acceptance. In
`CODE_AGENT_MODEL_ADMISSION=required` mode the Go CLI and full-stack continuation
client issue transient, actor-bound gRPC capabilities. Python uses its existing
OpenAI/Anthropic payload adapters, while Go owns credentials, HTTP dispatch,
reservation and settlement. Capabilities do not enter model Context or checkpoints.

Foreground, compaction, reflection and the supplied Workflow worker client can
use this boundary. Independent child scopes are recovered from verified
parent/child Ledger links, including auxiliary calls without an AgentTask field.
The admitted runner bypasses the old per-session token/money cap; its
SessionMeta, CLI and browser report unknown price explicitly.

Configure the approved provider in the Go process environment together with
`CODE_AGENT_MODEL_INPUT_LIMIT` and `CODE_AGENT_MODEL_OUTPUT_LIMIT`. Absent or
invalid credentials, endpoint, model or bounds refuse dispatch. Python in
required mode skips dotenv/provider-file loading and direct client construction;
the Go subprocess launcher strips model credentials from its environment.

The gateway admits **one unresolved upstream call** across the canonical
Ledger, within the permitted aggregate range of 1–10. This preserves a fence
after a process crash or failed unknown-outcome append. It overrides output
limits, refuses provider-hosted tools and redirects, and normalizes OpenAI
prompt/completion totals or Anthropic input + cache creation/read totals once.
OpenAI uses `max_completion_tokens`, which includes reasoning output under its
[API contract](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create).
Compatible relays must support that contract; there is no silent parameter downgrade.

429, transport failures and responses without attributable usage retain the
whole reservation and return a distinct, nonretryable blocked classification.
There is currently no automatic paid retry after such an unknown outcome.
Retries with confirmed accounting and their recovery matrix remain incomplete.
The admitted transport currently returns a single final chunk; metered SSE
streaming is a remaining product migration item.

The existing authenticated capabilities endpoint now exposes a read-only batch
projection and specific model/sandbox prerequisites in the approved right
sidebar. A read creates no batch facts. The local-core profile still refuses
execution. Legacy direct callers remain available outside required mode;
their calls are not claimed to be part of this metered batch.

CLI `/clear` completes a settled root task before creating the next Session;
live scopes, open runs and unfinished agents refuse completion. Ending a
process leaves the task available for explicit resume, while `/budget` reports
its Session ID. The cumulative allowance never resets on task completion.

Remaining: default product enablement, controlled secret injection/approved
sandbox, task-slot completion across HTTP/product lifecycles, fully metered
retry/reconciliation, production CLI/HTTP model recovery and complete trace
backend readback. Keep #3 open until those criteria have fresh evidence.

## Reproducible checks

```text
go test ./internal/admission -count=1 -v
```

These tests use synthetic amounts and real SQLite, including distinct Go
processes contending on the same Ledger. They make **zero model requests**.
They cover restart, CAS contention, owner boundaries, one task, ten relay
slots, duplicate dispatch, unknown usage, violated bounds, task completion and
contradictory facts. The subprocess helper is skipped in the parent process
and executed by its parent tests.

## Provider preflight boundary

The approved profile passed a read-only, authenticated BeeAPI model-catalog
check on 2026-10-09; the configured model was present. The catalog supplied no
context or output-limit metadata. The profile omits an explicit output limit;
the existing Anthropic adapter has a default and can raise it for thinking.
Those configuration values do not by themselves prove the relay's complete
billing contract. No paid call was made during this preflight.

The upstream model page publishes a context window, while the relay supports
multiple provider groups. Bind the selected relay/adapter contract before
turning that upstream value into a dispatch allowance, and validate actual
usage after a bounded call. References:
[upstream model](https://docs.x.ai/developers/models/grok-4.6),
[BeeAPI setup](https://beeapi.dev/docs/01-quick-start/quickstart).

On 2026-10-10 a bounded real-provider probe made five calls through the Go
gateway: foreground, reflection proposals, compaction, a Workflow worker and
a fresh Python process. The first run settled 15,130 tokens and an independent
Go process recovered the same batch and consumption. It used an explicit
500,000 input ceiling derived from the upstream model page and 2,048 output
tokens. This is a configured contract assumption plus observed usage for those
calls; it is not proof of all relay groups or untested cache/reasoning variants.
All repeated paid probes use the same canonical verification Ledger.
The receipt remains BLOCKED for full acceptance because trace backend readback
and production code-task/browser model flows are outside this probe.
