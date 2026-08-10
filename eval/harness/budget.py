"""Eval harness budget (plan Task 8.1).

Wall-clock / token / cost / output-size limits per instance. A budget is
immutable once a run starts; exceeding any limit classifies the run
accordingly (timeout, infra, budget).
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field


@dataclass(frozen=True)
class Budget:
    wall_clock_seconds: float = 600.0
    max_tokens: int = 200_000
    max_cost: float = 2.0
    max_output_bytes: int = 1_000_000
    max_processes: int = 4


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
