# Verification token batch

The authorized batch limit is **100,000,000 input plus output tokens**. This
replaces the earlier CNY100 limit. The provider enforces this approved hard
limit; local reservations are audit estimates, not a second spending gate.
Prices and currency conversion remain independent accounting data;
an unknown price must not be recorded as zero. The current provider-quota
continuation and stopped-process recovery checks, including retained failures,
are recorded in [2026-10-13 evidence](evidence/model-proposal-budget-2026-10-13.md).

## Current implementation

`internal/admission.Budget` records the batch, reservations, settlements,
unknown outcomes and task completion as append-only events in the existing
Session Ledger. It creates no separate billing database or mutable balance
file. All Harness processes participating in this batch must open the same
approved canonical Ledger; separate databases are not coordinated by this
module.

- The batch identity and cumulative allowance survive process restart.
- Only one task can hold the batch slot, including across different owners.
  At most ten active local calls can hold relay slots within that task.
- Each upstream attempt needs a new call ID and a confirmed token upper bound.
  A duplicate reservation refuses another dispatch. A repeated identical
  settlement does not count consumption again.
- Missing usage preserves the original reservation and unknown status without
  blocking independent calls after the local attempt ends. Confirmed usage can
  later settle that call. A bound violation remains recorded and cannot be erased
  by a smaller settlement; accounting uncertainty is not converted into zero.
- A stopped task can release its slot while retaining unknown usage. Active
  calls and process scopes still prevent completion. A delayed auxiliary call
  cannot reopen a finished task; later settlement does not alter task identity.
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

The gateway admits **one active local upstream attempt** across the canonical
Ledger, within the permitted aggregate range of 1–10. An unmarked reservation
still fences dispatch after a process crash; a terminal unknown accounting fact
does not occupy a relay slot. It overrides output
limits, refuses provider-hosted tools and redirects, and normalizes OpenAI
prompt/completion totals or Anthropic input + cache creation/read totals once.
OpenAI uses `max_completion_tokens`, which includes reasoning output under its
[API contract](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create).
Compatible relays must support that contract; there is no silent parameter downgrade.

Transport failures and responses without attributable usage retain the original
reservation and return a distinct, nonretryable error for that attempt. An
unknown 429/5xx is never assumed to cost zero and is not automatically retried.
The next independent request does not require operator settlement or approval;
the provider's authorized hard quota remains the spending limit.
Duplicate accounting fields, including escaped names that decode to the same
field, follow this unknown path; unrelated metadata retains its existing handling.
[Ambiguous usage checks](evidence/model-ambiguous-usage-2026-10-13.md).
When a response has complete bounded usage, Go durably settles that attempt
before any retry. Only such accounted 429/5xx responses permit up to three
attempts, with one/two-second backoff and a valid Retry-After of at most ten
seconds. An excessive or malformed delay stops retries rather than retrying
earlier than requested. Repeated Retry-After field lines are rejected by their count
before parsing, including identical values or fragments that could form a date.
An explicitly empty field also stops retries; an absent field uses default backoff,
and a single valid HTTP-date retains its normal handling.
[Repeated field checks](evidence/model-retry-after-fields-2026-10-13.md).
[Field-count correction](evidence/model-retry-after-arity-2026-10-13.md).
Authentication and other HTTP errors are not retried.

Each retry gets a new Go call ID and a same-Ledger `retry_of` link. Replay and
CAS require a settled transient parent, unchanged owner/task/bound/purpose,
one successor and at most three attempts. Every attempt rechecks the active
capability and task prerequisites before its own reservation and dispatch.
The protocol counters include all settled attempts; Python does not retry the
provider independently. An invalid HTTP status is rejected before settlement
and preserves the reservation without poisoning the Ledger.

Canceling backoff adds no undispatched reservation. Canceling an in-flight
attempt still preserves its unknown outcome. Python retains its existing
65-second invocation deadline, so slow retries may exhaust that deadline;
the timeout does not authorize another attempt or clear pending usage.
These are transport/accounting contracts. Real relay error responses do not
necessarily supply attributable usage; missing usage still requires separate
reconciliation. [Accounted retry checks](evidence/model-accounted-retry-2026-10-12.md).
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

CLI `/clear` completes a stopped root task before creating the next Session;
live scopes, open runs and unfinished agents refuse completion. Ending a
process leaves the task available for explicit resume, while `/budget` reports
its Session ID. The cumulative allowance never resets on task completion.

Authenticated `POST /api/v1/sessions/:id/task/finish` exposes the same Go
completion boundary in the selected sidebar. The owner must match the root;
foreign/deleted Sessions refuse access. Repeated completion, including after
HTTP restart, preserves the batch and does not release a later task. Explicitly
ending an unused root records a zero-token completion and prevents rebinding.
Unknown model accounting can remain after task completion; its original facts
and reservations stay available. Missing tool outcomes and unconfirmed tool/Hook stops in ordinary
or legacy CLI histories refuse completion without rewriting those outcomes.

Model scope bindings and releases are lifecycle facts in the same admission
Ledger. A scope ID is a capability digest; the capability itself remains
transient. Scope facts can precede the first admitted call without creating a
token batch or reserving funds. Completion checks scopes in its sequence CAS,
so another Harness process holding a binding still prevents completion.
Normal Gateway shutdown stops handlers before releasing its bindings. A crash
or failed release append retains the fence. New binding facts also record a
hashed local host identity, platform, PID and canonical OS creation identity.
The existing explicit completion action can recover a retained scope only
after Go confirms its exact original holder stopped. Recovery appends the
original scope identities through the same Ledger CAS; a conflict requires
fresh proof. It never kills a process or changes token accounting.

An alive holder, legacy fact without process metadata, different host or
unavailable OS proof continues to refuse completion. Linux additionally
requires a confirmed proc PID view and matching PID/user namespace identity;
unsupported platforms refuse new bindings. A failed release append while its
holder is still alive remains fenced. New Gateway reservations retain their
scope's process provenance. Confirmed holder exit can mark an interrupted call
terminal-unknown, preserving its original reservation without settling tokens.
Legacy reservations without attributable process proof stay fenced; holder
exit never implies zero consumption or invents a provider usage response.
[Completion checks](evidence/model-task-finish-2026-10-12.md) and
[confirmed holder recovery checks](evidence/model-scope-recovery-2026-10-12.md)
describe their separate scopes.

Issue #3 still needs its complete attempt denominator for real-provider retries,
reflection/child-call accounting, process restart and draft recovery. Retained
unknown usage remains an accounting gap that can be settled later.
Full coding tasks, product fault recovery, reflection writeback, cross-session
recall and complete Trace readback are accepted under their own later tickets,
not implicit dependencies of #3. Current scope evidence is linked from the Goal.

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

## 运行证据

当前计量契约按本文及 [AGENTS.md](../AGENTS.md) 执行，具体调用与完整尝试分母见
[GOAL.md 的证据链接](GOAL.md#证据链接)。2026-10-09／10 的 catalog 与五次独立探针
保留在 [历史原文](archive/policy-cleanup-2026-10-14/docs--token-budget.md#provider-preflight-boundary)，
其旧用量和 Trace 缺口只描述当次范围。
