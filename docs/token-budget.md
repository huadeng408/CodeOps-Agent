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

Engineering status: **IMPLEMENTED** for the Ledger module and CLI /
full-stack / local-core invocation boundary; **BLOCKED** for Issue #3's complete product
acceptance. The Go CLI and full-stack continuation client always issue transient,
actor-bound gRPC capabilities. Python uses its existing
OpenAI/Anthropic payload adapters, while Go owns credentials, HTTP dispatch,
reservation and settlement. Capabilities do not enter model Context or checkpoints.

Foreground, compaction, reflection and the supplied Workflow worker client can
use this boundary. Independent child scopes are recovered from verified
parent/child Ledger links, including auxiliary calls without an AgentTask field.
The admitted runner bypasses the old per-session token/money cap; its
SessionMeta, CLI and browser report unknown price explicitly.

Configure the approved provider in the Go process environment together with
`CODE_AGENT_MODEL_INPUT_LIMIT` and `CODE_AGENT_MODEL_OUTPUT_LIMIT`. Absent or
invalid credentials, endpoint, model or bounds refuse dispatch. Go always starts
its Python subprocess in required mode and strips provider credentials and the
provider-file entry point, even when the parent inherits a disabled admission
setting. Required mode skips dotenv/provider-file loading and direct client
construction. Harness-managed Converse requests also refuse a missing binding
when an external Python server was started outside required mode.

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
sidebar. A read creates no batch facts. Local-core attaches continuation only
with a configured provider, approved OpenSandbox prerequisites and a healthy
orchestrator at `CODE_AGENT_ORCHESTRATOR_ADDR` (default loopback port 50051).
Its Context and tools use the Go-prepared Task Workspace; Memory keeps the
original repository namespace. Each local-core gateway attempt checks the retained
workspace, tool outcomes and runtime before reserving tokens. Ledger-recorded
Go edits can continue; missing or unknown tool results require reconciliation.
Legacy standalone Python callers remain available outside
required mode; their calls are not claimed to be part of this metered batch.
Default-boundary checks: [2026-10-12 evidence](evidence/model-default-admission-2026-10-12.md).
Local-core checks: [workspace admission evidence](evidence/model-local-workspace-2026-10-12.md).

CLI `/clear` completes a settled root task before creating the next Session;
live scopes, open runs and unfinished agents refuse completion. Ending a
process leaves the task available for explicit resume, while `/budget` reports
its Session ID. The cumulative allowance never resets on task completion.

Authenticated `POST /api/v1/sessions/:id/task/finish` exposes the same Go
completion boundary in the selected sidebar. The owner must match the root;
foreign/deleted Sessions refuse access. Repeated completion, including after
HTTP restart, preserves the batch and does not release a later task. Explicitly
ending an unused root records a zero-token completion and prevents rebinding.
An exhausted but fully settled task can still be ended; its allowance stays
exhausted. Missing tool outcomes and unconfirmed tool/Hook stops in ordinary
or legacy CLI histories refuse completion without rewriting those outcomes.

Model scope bindings and releases are lifecycle facts in the same admission
Ledger. A scope ID is a capability digest; the capability itself remains
transient. Scope facts can precede the first admitted call without creating a
token batch or reserving funds. Completion checks scopes in its sequence CAS,
so another Harness process holding a binding still prevents completion.
Normal Gateway shutdown stops handlers before releasing its bindings. A crash
or failed release append retains the fence; explicit recovery of that fence
remains part of #3 reconciliation and is not covered by the successful HTTP
restart checks. [Scoped checks](evidence/model-task-finish-2026-10-12.md).

Remaining: real-provider local-core acceptance and controlled process startup,
task-slot recovery across process faults and complete product lifecycles, fully metered
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
