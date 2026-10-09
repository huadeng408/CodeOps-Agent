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

## Remaining integration

Engineering status: **IMPLEMENTED** for the Ledger module; **BLOCKED** for
Issue #3's complete runtime acceptance. The foreground, retry, reflection and
Worker callers have not yet migrated through this admission module. The
existing Python per-session `TokenBudget` is not this batch and does not
enforce a process-wide 100M-token limit. Local-core execution remains blocked.

Next, connect those callers through the versioned Go/Python protocol, validate
provider-specific cache/reasoning usage, and expose the actual preflight and
batch projection through the approved right sidebar. Keep the old callers
until migration and recovery have genuine runtime evidence. The module's
tests cannot close #3 or substitute for browser or paid-model acceptance.

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
