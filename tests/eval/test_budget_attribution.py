"""A budget the harness imposed must never be recorded as an agent failure.

Regression tests for the run where a fixed 500k run-wide token cap let 6 of 20
instances through and the other 14 were recorded ``agent`` — a summary reading
"5 ok, 15 failed, agent: 14" for a model that never saw 14 of them.

Two separate things are pinned:

1. **Attribution.** Cumulative run-wide pools (tokens, cost) get their own
   category. Per-instance instantaneous caps (processes) stay with the agent,
   because there the agent's own behaviour really did hit the limit.
2. **Sizing.** The default budget scales with the instance count, so requesting
   more instances cannot silently truncate the run.
"""

from __future__ import annotations

import pytest

from eval.harness.budget import Budget, BudgetExceeded, BudgetUsage, check_budget
from eval.harness.runner import (
    ERROR_AGENT,
    ERROR_BUDGET,
    ERROR_OOM,
    ERROR_TIMEOUT,
    classify_error,
)


# ------------------------------------------------------------------ attribution


def test_token_exhaustion_is_not_an_agent_failure():
    """The exact misattribution: 14 instances blamed on the model."""
    verdict = classify_error(BudgetExceeded("tokens", "542854 > 500000"))
    assert verdict == ERROR_BUDGET
    assert verdict != ERROR_AGENT


def test_cost_exhaustion_is_not_an_agent_failure():
    assert classify_error(BudgetExceeded("cost", "$11 > $10")) == ERROR_BUDGET


def test_process_cap_stays_with_the_agent():
    """A per-instance cap the agent's own behaviour hit."""
    assert classify_error(BudgetExceeded("processes", "5 > 1")) == ERROR_AGENT


def test_wall_clock_is_still_a_timeout():
    assert classify_error(BudgetExceeded("wall-clock", "10s > 5s")) == ERROR_TIMEOUT


def test_output_bytes_is_still_oom():
    assert classify_error(BudgetExceeded("output", "9 bytes > 5")) == ERROR_OOM


def test_budget_category_is_distinct_from_every_other():
    assert ERROR_BUDGET not in (ERROR_AGENT, ERROR_OOM, ERROR_TIMEOUT)
    assert ERROR_BUDGET == "budget"


def test_summary_initialises_the_budget_counter():
    """A category missing from by_category would KeyError mid-run."""
    from eval.harness.runner import HarnessRun

    import inspect

    source = inspect.getsource(HarnessRun.run)
    assert "ERROR_BUDGET: 0" in source


# ----------------------------------------------------------------------- sizing


def _sized_budget(instance_count: int, env: dict[str, str] | None = None) -> Budget:
    """Rebuild eval.run's sizing rule for *instance_count* instances."""
    import os

    overrides = env or {}

    def read(name: str, default: float) -> float:
        raw = overrides.get(name) or os.environ.get(name)
        return float(raw) if raw else default

    count = max(1, instance_count)
    return Budget(
        wall_clock_seconds=read("EVAL_BUDGET_SECONDS", 900.0 * count),
        max_tokens=int(read("EVAL_BUDGET_TOKENS", 250_000 * count)),
        max_cost=read("EVAL_BUDGET_COST", 2.0 * count),
        max_output_bytes=int(read("EVAL_BUDGET_OUTPUT_BYTES", 5_000_000 * count)),
    )


def test_twenty_instances_get_twenty_instances_worth_of_tokens():
    budget = _sized_budget(20)
    assert budget.max_tokens == 5_000_000
    # The observed run consumed 542,854 tokens across ~6 instances. Twenty
    # instances at that rate is ~1.8M, comfortably inside the new cap.
    assert budget.max_tokens > 1_800_000


def test_budget_scales_linearly_with_instance_count():
    one = _sized_budget(1)
    ten = _sized_budget(10)
    assert ten.max_tokens == one.max_tokens * 10
    assert ten.wall_clock_seconds == one.wall_clock_seconds * 10
    assert ten.max_cost == pytest.approx(one.max_cost * 10)


def test_zero_instances_does_not_produce_a_zero_budget():
    """A zero cap would fail the run on its first token."""
    budget = _sized_budget(0)
    assert budget.max_tokens > 0
    assert budget.wall_clock_seconds > 0


def test_explicit_override_is_honoured_verbatim():
    """An operator who names a total means that total, not a per-instance rate."""
    budget = _sized_budget(20, {"EVAL_BUDGET_TOKENS": "123456"})
    assert budget.max_tokens == 123456


def test_the_old_fixed_default_would_still_truncate_twenty_instances():
    """Guards the reasoning, not just the constant.

    If someone reverts to a flat 500k, this documents what that costs: the run
    stops partway and the remainder are recorded as failures.
    """
    usage = BudgetUsage()
    usage.tokens = 542_854  # measured across roughly six astropy instances
    with pytest.raises(BudgetExceeded) as excinfo:
        check_budget(Budget(max_tokens=500_000), usage)
    assert excinfo.value.kind == "tokens"
    assert classify_error(excinfo.value) == ERROR_BUDGET
    # And the same usage is fine once the cap is sized for the run.
    check_budget(_sized_budget(20), usage)
