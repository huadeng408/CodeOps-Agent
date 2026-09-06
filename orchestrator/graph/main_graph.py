from __future__ import annotations

import sqlite3
import uuid
from dataclasses import asdict, dataclass, field, fields
from pathlib import Path
from typing import Any

from langgraph.checkpoint.sqlite import SqliteSaver
from langgraph.graph import END, START, StateGraph
from langgraph.graph.state import CompiledStateGraph

from .nodes import GraphState, execute_node, respond_node, route_node, verify_node


@dataclass(slots=True)
class MainGraph:
    """Compiled LangGraph workflow with a compatibility ``run`` facade."""

    name: str = "main"
    nodes: tuple[str, ...] = field(
        default=("route", "execute", "verify", "respond"),
    )
    checkpoint_path: str | Path | None = None
    interrupt_after: tuple[str, ...] = ()
    _connection: sqlite3.Connection | None = field(default=None, init=False, repr=False)
    _checkpointer: SqliteSaver | None = field(default=None, init=False, repr=False)
    _compiled: CompiledStateGraph = field(init=False, repr=False)

    def __post_init__(self) -> None:
        workflow = StateGraph(GraphState)
        workflow.add_node("route", route_node)
        workflow.add_node("execute", execute_node)
        workflow.add_node("verify", verify_node)
        workflow.add_node("respond", respond_node)
        workflow.add_edge(START, "route")
        workflow.add_conditional_edges(
            "route",
            lambda state: state.next_node,
            {"execute": "execute", "respond": "respond"},
        )
        workflow.add_edge("execute", "verify")
        workflow.add_edge("verify", "respond")
        workflow.add_edge("respond", END)

        if self.checkpoint_path is not None:
            path = Path(self.checkpoint_path)
            path.parent.mkdir(parents=True, exist_ok=True)
            self._connection = sqlite3.connect(path, check_same_thread=False)
            self._checkpointer = SqliteSaver(self._connection)
            self._checkpointer.setup()

        self._compiled = workflow.compile(
            checkpointer=self._checkpointer,
            interrupt_after=list(self.interrupt_after) or None,
            name=self.name,
        )

    @property
    def compiled(self) -> CompiledStateGraph:
        """Return the LangGraph compiled workflow for advanced integrations."""

        return self._compiled

    def run(self, state: GraphState | None = None, *, thread_id: str | None = None) -> GraphState:
        initial = state or GraphState()
        config: dict[str, Any] | None = None
        if self._checkpointer is not None:
            config = {"configurable": {"thread_id": thread_id or f"run-{uuid.uuid4().hex}"}}
        result = self._compiled.invoke(asdict(initial), config=config)
        return _coerce_state(result)

    def get_checkpoint(self, thread_id: str) -> GraphState | None:
        """Read the latest state for *thread_id* without executing nodes."""

        if self._checkpointer is None:
            raise RuntimeError("graph checkpointing is not configured")
        snapshot = self._compiled.get_state({"configurable": {"thread_id": thread_id}})
        if not snapshot or not snapshot.values:
            return None
        return _coerce_state(snapshot.values)

    def write_checkpoint(self, state: GraphState, *, thread_id: str) -> None:
        """Persist a typed state snapshot without executing graph nodes.

        ConversationRunner has a richer model/tool lifecycle than the small
        built-in graph.  This adapter lets that lifecycle share the same
        durable LangGraph checkpoint store while keeping checkpoint writes
        side-effect free.
        """

        if self._checkpointer is None:
            raise RuntimeError("graph checkpointing is not configured")
        if not thread_id.strip():
            raise ValueError("thread_id is required for graph checkpoints")
        self._compiled.update_state(
            {"configurable": {"thread_id": thread_id}},
            asdict(state),
        )

    def close(self) -> None:
        if self._connection is not None:
            self._connection.close()
            self._connection = None


def _coerce_state(value: GraphState | dict[str, Any]) -> GraphState:
    if isinstance(value, GraphState):
        return value
    allowed = {item.name for item in fields(GraphState)}
    return GraphState(**{key: item for key, item in value.items() if key in allowed})


def build_graph(
    *,
    checkpoint_path: str | Path | None = None,
    interrupt_after: tuple[str, ...] = (),
) -> MainGraph:
    return MainGraph(checkpoint_path=checkpoint_path, interrupt_after=interrupt_after)
