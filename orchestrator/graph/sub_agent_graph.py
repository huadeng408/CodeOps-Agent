"""Durable plan/delegate/collect/verify graph for independent agents."""

from __future__ import annotations

import sqlite3
import uuid
from dataclasses import asdict, dataclass, field, fields
from pathlib import Path
from typing import Any, Callable, Mapping

from langgraph.checkpoint.sqlite import SqliteSaver
from langgraph.graph import END, START, StateGraph
from langgraph.graph.state import CompiledStateGraph


@dataclass(slots=True)
class SubAgentState:
    goal: str = ""
    tasks: list[dict[str, Any]] = field(default_factory=list)
    delegated: dict[str, dict[str, Any]] = field(default_factory=dict)
    results: dict[str, dict[str, Any]] = field(default_factory=dict)
    status: str = "pending"
    errors: list[str] = field(default_factory=list)
    metadata: dict[str, Any] = field(default_factory=dict)


Delegate = Callable[[Mapping[str, Any]], Mapping[str, Any]]
Collect = Callable[[Mapping[str, Any]], Mapping[str, Any]]
Verify = Callable[[Mapping[str, Any]], bool]


def _plan(state: SubAgentState) -> SubAgentState:
    if not state.tasks and state.goal.strip():
        state.tasks = [{"id": "task-1", "objective": state.goal.strip()}]
    state.metadata["planned"] = True
    return state


def _delegate(state: SubAgentState, delegate: Delegate | None) -> SubAgentState:
    if delegate is None:
        state.errors.append("delegate adapter is not configured")
        state.status = "failed"
        return state
    for raw_task in state.tasks:
        task = dict(raw_task)
        task_id = str(task.get("id", "")).strip()
        if not task_id or task_id in state.delegated:
            continue
        try:
            result = dict(delegate(task))
        except Exception as exc:  # noqa: BLE001 - record only a safe type
            state.errors.append(type(exc).__name__)
            state.status = "failed"
            continue
        result.setdefault("task_id", task_id)
        state.delegated[task_id] = result
    state.metadata["delegated"] = len(state.delegated)
    return state


def _collect(state: SubAgentState, collect: Collect | None) -> SubAgentState:
    if state.status == "failed":
        return state
    if collect is None:
        state.results = dict(state.delegated)
    else:
        for task_id, delegated in state.delegated.items():
            if task_id in state.results:
                continue
            try:
                state.results[task_id] = dict(collect(delegated))
            except Exception as exc:  # noqa: BLE001 - record only a safe type
                state.errors.append(type(exc).__name__)
    state.metadata["collected"] = len(state.results)
    return state


def _verify(state: SubAgentState, verify: Verify | None) -> SubAgentState:
    if state.errors:
        state.status = "failed"
        return state
    expected = {str(task.get("id", "")).strip() for task in state.tasks}
    complete = bool(expected and expected == set(state.results))
    if verify is not None:
        complete = bool(complete and all(verify(result) for result in state.results.values()))
    else:
        complete = bool(
            complete
            and all(
                str(result.get("status", "completed")).lower()
                in {"completed", "complete", "succeeded", "success"}
                for result in state.results.values()
            )
        )
    state.status = "completed" if complete else "failed"
    state.metadata["verified"] = complete
    return state


@dataclass(slots=True)
class SubAgentGraph:
    """Compiled LangGraph workflow with a compatibility ``run`` facade."""

    name: str = "sub-agent"
    checkpoint_path: str | Path | None = None
    interrupt_after: tuple[str, ...] = ()
    delegate: Delegate | None = None
    collect: Collect | None = None
    verify: Verify | None = None
    nodes: tuple[str, ...] = field(
        default=("plan", "delegate", "collect", "verify"), init=False
    )
    _connection: sqlite3.Connection | None = field(default=None, init=False, repr=False)
    _checkpointer: SqliteSaver | None = field(default=None, init=False, repr=False)
    _compiled: CompiledStateGraph = field(init=False, repr=False)

    def __post_init__(self) -> None:
        workflow = StateGraph(SubAgentState)
        workflow.add_node("plan", _plan)
        workflow.add_node("delegate", lambda state: _delegate(state, self.delegate))
        workflow.add_node("collect", lambda state: _collect(state, self.collect))
        workflow.add_node("verify", lambda state: _verify(state, self.verify))
        workflow.add_edge(START, "plan")
        workflow.add_edge("plan", "delegate")
        workflow.add_edge("delegate", "collect")
        workflow.add_edge("collect", "verify")
        workflow.add_edge("verify", END)
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
        return self._compiled

    def run(
        self, state: SubAgentState | None = None, *, thread_id: str | None = None
    ) -> SubAgentState:
        initial = state or SubAgentState()
        config: dict[str, Any] | None = None
        if self._checkpointer is not None:
            config = {"configurable": {"thread_id": thread_id or f"run-{uuid.uuid4().hex}"}}
        result = self._compiled.invoke(asdict(initial), config=config)
        return _coerce_state(result)

    def get_checkpoint(self, thread_id: str) -> SubAgentState | None:
        if self._checkpointer is None:
            raise RuntimeError("graph checkpointing is not configured")
        snapshot = self._compiled.get_state({"configurable": {"thread_id": thread_id}})
        if not snapshot or not snapshot.values:
            return None
        return _coerce_state(snapshot.values)

    def close(self) -> None:
        if self._connection is not None:
            self._connection.close()
            self._connection = None


def _coerce_state(value: SubAgentState | Mapping[str, Any]) -> SubAgentState:
    if isinstance(value, SubAgentState):
        return value
    allowed = {item.name for item in fields(SubAgentState)}
    return SubAgentState(**{key: item for key, item in value.items() if key in allowed})


def build_sub_agent_graph(
    *,
    checkpoint_path: str | Path | None = None,
    interrupt_after: tuple[str, ...] = (),
    delegate: Delegate | None = None,
    collect: Collect | None = None,
    verify: Verify | None = None,
) -> SubAgentGraph:
    return SubAgentGraph(
        checkpoint_path=checkpoint_path,
        interrupt_after=interrupt_after,
        delegate=delegate,
        collect=collect,
        verify=verify,
    )


__all__ = ["SubAgentGraph", "SubAgentState", "build_sub_agent_graph"]
