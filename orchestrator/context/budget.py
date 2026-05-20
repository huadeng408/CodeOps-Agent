from __future__ import annotations

from dataclasses import dataclass
from enum import Enum


class BudgetStatus(str, Enum):
    OK = "ok"
    WARNING = "warning"
    EXCEEDED = "exceeded"


@dataclass(slots=True)
class TokenBudget:
    max_tokens: int = 1_000_000
    max_cost: float = 5.0
    used_tokens: int = 0
    used_cost: float = 0.0

    def check(self) -> BudgetStatus:
        if self.used_tokens >= self.max_tokens:
            return BudgetStatus.EXCEEDED
        if self.used_cost >= self.max_cost:
            return BudgetStatus.EXCEEDED
        if self.used_tokens >= self.max_tokens * 0.9:
            return BudgetStatus.WARNING
        return BudgetStatus.OK

    def consume(self, tokens: int, cost: float = 0.0) -> None:
        self.used_tokens += max(tokens, 0)
        self.used_cost += max(cost, 0.0)

    def remaining_tokens(self) -> int:
        return max(self.max_tokens - self.used_tokens, 0)

    def remaining_cost(self) -> float:
        return max(self.max_cost - self.used_cost, 0.0)

    def on_exceeded(self) -> str:
        return (
            "Token budget exceeded. Session paused. "
            "Increase the budget or start a new session to continue."
        )
