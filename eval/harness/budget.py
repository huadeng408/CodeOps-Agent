"""Eval harness budget (plan Task 8.1).

Wall-clock / token / cost / output-size limits per instance. A budget is
immutable once a run starts; exceeding any limit classifies the run
accordingly (timeout, infra, budget).
"""

from __future__ import annotations

import os
import time
from collections.abc import Mapping
from dataclasses import dataclass, field
import math

DEFAULT_INSTANCE_WALL_CLOCK_SECONDS = 900.0
MAX_DEFAULT_SCORER_RESERVE_SECONDS = 240.0
DEFAULT_SCORER_RESERVE_FRACTION = 0.25


@dataclass(frozen=True)
class Budget:
    wall_clock_seconds: float = 600.0
    max_tokens: int = 200_000
    max_cost: float = 2.0
    max_output_bytes: int = 1_000_000
    max_processes: int = 4
    instance_wall_clock_seconds: float | None = None
    scorer_reserve_seconds: float = 0.0


@dataclass
class BudgetUsage:
    started_at: float = field(default_factory=time.perf_counter)
    wall_clock_seconds: float = 0.0
    tokens: int = 0
    cost: float = 0.0
    output_bytes: int = 0
    active_processes: int = 0

    def record_tokens(self, n: int) -> None:
        self.tokens += n

    def record_cost(self, cost: float) -> None:
        self.cost += cost

    def record_output(self, n: int) -> None:
        self.output_bytes += n

    def record_process_start(self) -> None:
        """Advisory: increment the tracked count of concurrent child processes."""
        self.active_processes += 1

    def record_process_end(self) -> None:
        """Decrement the tracked child-process count (floored at zero)."""
        self.active_processes = max(0, self.active_processes - 1)

    def wall_clock(self) -> float:
        return time.perf_counter() - self.started_at


class BudgetExceeded(Exception):
    """Raised when a budget limit is exceeded; carries the limit kind."""

    def __init__(self, kind: str, detail: str) -> None:
        super().__init__(f"budget {kind} exceeded: {detail}")
        self.kind = kind


def check_budget(budget: Budget, usage: BudgetUsage) -> None:
    """Raise BudgetExceeded when any limit is hit (fail loud, never silent)."""
    if usage.wall_clock() > budget.wall_clock_seconds:
        raise BudgetExceeded("wall-clock", f"{usage.wall_clock():.1f}s > {budget.wall_clock_seconds}s")
    if usage.active_processes > budget.max_processes:
        raise BudgetExceeded(
            "processes",
            f"{usage.active_processes} concurrent processes > {budget.max_processes}",
        )
    if usage.tokens > budget.max_tokens:
        raise BudgetExceeded("tokens", f"{usage.tokens} > {budget.max_tokens}")
    if usage.cost > budget.max_cost:
        raise BudgetExceeded("cost", f"${usage.cost:.2f} > ${budget.max_cost}")
    if usage.output_bytes > budget.max_output_bytes:
        raise BudgetExceeded("output", f"{usage.output_bytes} bytes > {budget.max_output_bytes}")


def build_run_budget(
    instance_count: int,
    *,
    environ: Mapping[str, str] | None = None,
) -> Budget:
    """Build the official-run budget from one consistent environment source."""
    env = os.environ if environ is None else environ
    count = max(1, int(instance_count))

    def read(name: str, default: float) -> float:
        raw = env.get(name)
        if not raw:
            return default
        try:
            value = float(raw)
        except (TypeError, ValueError) as exc:
            raise ValueError(f"invalid budget {name}: {raw!r}") from exc
        if not math.isfinite(value):
            raise ValueError(f"invalid budget {name}: must be finite")
        return value

    def read_count(name: str, default: int) -> int:
        value = read(name, float(default))
        if value < 0 or not value.is_integer():
            raise ValueError(f"invalid budget {name}: must be a non-negative integer")
        return int(value)

    total_wall_clock = read(
        "EVAL_BUDGET_SECONDS",
        DEFAULT_INSTANCE_WALL_CLOCK_SECONDS * count,
    )
    instance_wall_clock = read(
        "EVAL_INSTANCE_BUDGET_SECONDS",
        total_wall_clock / count,
    )
    # An explicit per-instance value cannot extend the run-level deadline. Keep
    # one effective wall-clock budget for phase allocation and enforcement.
    instance_wall_clock = min(total_wall_clock, instance_wall_clock)
    default_scorer_reserve = min(
        MAX_DEFAULT_SCORER_RESERVE_SECONDS,
        instance_wall_clock * DEFAULT_SCORER_RESERVE_FRACTION,
    )
    scorer_reserve = read(
        "EVAL_SCORER_RESERVE_SECONDS",
        default_scorer_reserve,
    )
    if total_wall_clock <= 0 or instance_wall_clock <= 0:
        raise ValueError("wall-clock budgets must be positive")
    if scorer_reserve < 0 or scorer_reserve >= instance_wall_clock:
        raise ValueError(
            "scorer reserve must be non-negative and smaller than the "
            "effective per-instance wall-clock budget"
        )

    max_cost = read("EVAL_BUDGET_COST", 2.0 * count)
    if max_cost < 0:
        raise ValueError("invalid budget EVAL_BUDGET_COST: must be non-negative")

    return Budget(
        wall_clock_seconds=total_wall_clock,
        instance_wall_clock_seconds=instance_wall_clock,
        scorer_reserve_seconds=scorer_reserve,
        max_tokens=read_count("EVAL_BUDGET_TOKENS", 250_000 * count),
        max_cost=max_cost,
        max_output_bytes=read_count("EVAL_BUDGET_OUTPUT_BYTES", 5_000_000 * count),
    )
