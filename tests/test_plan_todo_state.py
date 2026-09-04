from __future__ import annotations

import pytest

from codeagent import orchestrator_pb2
from orchestrator.graph.main_graph import build_graph
from orchestrator.llm.client import ChatResponse, ToolCall, Usage
from orchestrator.memory.manager import MemoryManager
from orchestrator.runtime.conversation import ConversationRunner
from orchestrator.runtime.tools import ToolRegistry
from orchestrator.server import OrchestratorService
from orchestrator.skills.manager import SkillManager
from orchestrator.todo.manager import TodoManager
from orchestrator.todo.state import (
    PlanTodoSnapshot,
    PlanTodoStateMachine,
    StateConflictError,
    StateValidationError,
)


def test_state_machine_restores_versioned_snapshot_without_mutating_it() -> None:
    source = {
        "schema_version": 1,
        "revision": 7,
        "plan": {
            "steps": ["inspect", "patch"],
            "current_index": 1,
            "mode": "plan",
        },
        "todos": [
            {"content": "inspect", "active_form": "inspecting", "status": "completed"},
            {"content": "patch", "active_form": "patching", "status": "in_progress"},
        ],
    }

    machine = PlanTodoStateMachine.from_wire(source)
    snapshot = machine.snapshot()

    assert snapshot.revision == 7
    assert snapshot.plan.steps == ("inspect", "patch")
    assert snapshot.plan.current_index == 1
    assert snapshot.plan.mode == "plan"
    assert snapshot.todos[1].active_form == "patching"
    assert source["plan"]["steps"] == ["inspect", "patch"]


def test_server_decodes_wire_plan_todo_snapshot_without_losing_revision() -> None:
    wire = orchestrator_pb2.PlanTodoSnapshot(
        schema_version=1,
        revision=12,
        plan=orchestrator_pb2.PlanUpdate(
            steps=["inspect", "patch"], current_index=1, mode="plan"
        ),
        todos=[orchestrator_pb2.TodoItem(content="patch", status="in_progress")],
    )

    decoded = OrchestratorService._snapshot_from_proto(wire)

    assert decoded["revision"] == 12
    assert decoded["plan"] == {
        "steps": ["inspect", "patch"],
        "current_index": 1,
        "mode": "plan",
    }
    assert decoded["todos"] == [
        {"content": "patch", "active_form": "", "status": "in_progress"}
    ]


def test_state_machine_replaces_plan_with_monotonic_revision_and_rejects_stale_writes() -> None:
    machine = PlanTodoStateMachine.from_wire(
        {
            "schema_version": 1,
            "revision": 4,
            "plan": {"steps": ["inspect"], "current_index": 0, "mode": "plan"},
            "todos": [],
        }
    )

    updated = machine.replace_plan(
        ["inspect", "patch"], current_index=1, mode="plan", expected_revision=4
    )

    assert updated.revision == 5
    assert updated.plan.current_index == 1
    with pytest.raises(StateConflictError):
        machine.replace_plan(["other"], expected_revision=4)


def test_state_machine_validates_todos_and_requires_explicit_parallel_policy() -> None:
    machine = PlanTodoStateMachine()

    with pytest.raises(StateValidationError, match="steps must be a list"):
        machine.replace_plan("not-a-list")

    with pytest.raises(StateValidationError, match="duplicate"):
        machine.replace_todos(
            [
                {"content": "same", "status": "pending"},
                {"content": "same", "status": "pending"},
            ]
        )
    with pytest.raises(StateValidationError, match="in_progress"):
        machine.replace_todos(
            [
                {"content": "one", "status": "in_progress"},
                {"content": "two", "status": "in_progress"},
            ]
        )

    updated = machine.replace_todos(
        [
            {"content": "one", "status": "in_progress"},
            {"content": "two", "status": "in_progress"},
        ],
        allow_parallel=True,
    )
    assert updated.revision == 1
    assert [item.status for item in updated.todos] == ["in_progress", "in_progress"]


class _StateTodoLLM:
    model = "state-test"

    def __init__(self) -> None:
        self.requests = []

    async def chat(self, request):
        self.requests.append(request)
        if len(request.messages) == 0:
            raise AssertionError("system context must be present")
        return ChatResponse(
            tool_calls=[
                ToolCall(
                    id="todo-1",
                    name="TodoWrite",
                    arguments={"todos": [{"content": "patch", "status": "in_progress"}]},
                    arguments_json='{"todos":[{"content":"patch","status":"in_progress"}]}',
                )
            ],
            usage=Usage(input_tokens=2, output_tokens=1),
        ) if not any(message.role == "tool" for message in request.messages) else ChatResponse(
            text="state restored",
            usage=Usage(input_tokens=2, output_tokens=1),
        )


def test_runner_restores_snapshot_and_emits_next_revision(tmp_path) -> None:
    llm = _StateTodoLLM()
    runner = ConversationRunner(
        graph=build_graph(),
        llm=llm,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
    )

    responses = list(
        runner.run(
            "continue the task",
            iter(()),
            session_id="state-session",
            plan_todo_snapshot={
                "schema_version": 1,
                "revision": 9,
                "plan": {"steps": ["inspect", "patch"], "current_index": 1, "mode": "plan"},
                "todos": [{"content": "inspect", "status": "completed"}],
            },
        )
    )

    updates = [response.todo_update for response in responses if response.HasField("todo_update")]
    assert len(updates) == 1
    assert updates[0].revision == 10
    assert updates[0].todos[0].content == "patch"
    assert any(
        message.role == "system" and "state revision: 9" in str(message.content)
        for message in llm.requests[0].messages
    )
