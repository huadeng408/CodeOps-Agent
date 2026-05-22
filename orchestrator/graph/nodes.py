from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(slots=True)
class GraphState:
    messages: list[dict[str, Any]] = field(default_factory=list)
    plan: list[str] = field(default_factory=list)
    todos: list[dict[str, Any]] = field(default_factory=list)
    tool_requests: list[dict[str, Any]] = field(default_factory=list)
    metadata: dict[str, Any] = field(default_factory=dict)
    response: str = ""
    next_node: str = "route"
    tool_rounds: int = 0
    error_count: int = 0
    budget_status: str = "ok"
    recovery_hint: str = ""
    done: bool = False


def route_node(state: GraphState) -> GraphState:
    if state.budget_status == "exceeded":
        state.next_node = "respond"
    elif state.tool_requests:
        state.next_node = "execute"
    else:
        state.next_node = "respond"
    return state


def execute_node(state: GraphState) -> GraphState:
    if state.next_node != "execute":
        return state
    state.tool_rounds += 1
    state.metadata["executed"] = True
    state.metadata["tool_request_count"] = len(state.tool_requests)
    state.next_node = "verify"
    return state


def verify_node(state: GraphState) -> GraphState:
    state.metadata["verified"] = True
    if state.error_count >= 2:
        state.recovery_hint = "switch_strategy"
    elif state.error_count == 1:
        state.recovery_hint = "retry_with_context"
    state.next_node = "respond"
    return state


def respond_node(state: GraphState) -> GraphState:
    if state.budget_status == "exceeded" and not state.response:
        state.response = "Token budget exceeded."
    elif not state.response:
        state.response = "orchestrator response ready"
    state.done = True
    state.next_node = "done"
    return state
