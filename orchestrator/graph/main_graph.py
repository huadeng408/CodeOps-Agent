from __future__ import annotations

from dataclasses import dataclass, field

from .nodes import GraphState, execute_node, respond_node, route_node, verify_node


@dataclass(slots=True)
class MainGraph:
    name: str = "main"
    nodes: tuple[str, ...] = field(
        default=("route", "execute", "verify", "respond"),
    )

    def run(self, state: GraphState | None = None) -> GraphState:
        state = state or GraphState()
        state = route_node(state)
        state = execute_node(state)
        state = verify_node(state)
        state = respond_node(state)
        return state


def build_graph() -> MainGraph:
    return MainGraph()
