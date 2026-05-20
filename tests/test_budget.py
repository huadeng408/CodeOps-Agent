from __future__ import annotations

from orchestrator.context import BudgetStatus, TokenBudget


def test_token_budget_reports_warning_and_exceeded() -> None:
    budget = TokenBudget(max_tokens=100, max_cost=1.0)

    budget.consume(90, 0.1)
    assert budget.check() == BudgetStatus.WARNING
    assert budget.remaining_tokens() == 10

    budget.consume(5, 1.0)
    assert budget.check() == BudgetStatus.EXCEEDED
    assert "Token budget exceeded" in budget.on_exceeded()


def test_token_budget_clamps_remaining_values() -> None:
    budget = TokenBudget(max_tokens=10, max_cost=0.5)

    budget.consume(15, 1.0)

    assert budget.remaining_tokens() == 0
    assert budget.remaining_cost() == 0.0
