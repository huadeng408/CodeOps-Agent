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


def route_node(state: GraphState) -> GraphState:
    state.next_node = "execute" if state.tool_requests else "respond"
    return state


def execute_node(state: GraphState) -> GraphState:
    state.metadata.setdefault("executed", True)
    return state


def verify_node(state: GraphState) -> GraphState:
    state.metadata.setdefault("verified", True)
    return state


def respond_node(state: GraphState) -> GraphState:
    if not state.response:
        state.response = "orchestrator skeleton response"
    state.next_node = "done"
    return state
