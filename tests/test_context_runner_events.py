from __future__ import annotations

import sqlite3
from pathlib import Path

import pytest

from codeagent import orchestrator_pb2
from orchestrator.identity import ActorIdentity
from orchestrator.llm.client import ChatResponse, ToolCall
from orchestrator.runtime.conversation import ConversationRunner
from orchestrator.server import OrchestratorServer, ServerConfig, create_grpc_server


class EventRecordingLLM:
    model = "fake"

    def __init__(self) -> None:
        self.requests = []

    async def chat(self, request):
        self.requests.append(request)
        if len(self.requests) == 1:
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        id="plan-1",
                        name="PlanWrite",
                        arguments={"steps": ["write file"], "current_index": 0},
                        arguments_json='{"steps":["write file"],"current_index":0}',
                    )
                ]
            )
        if len(self.requests) == 2:
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        id="write-1",
                        name="Write",
                        arguments={
                            "path": "src/out.txt",
                            "content": "LEAK_SENTINEL_PATCH",
                        },
                        arguments_json=(
                            '{"path":"src/out.txt","content":"LEAK_SENTINEL_PATCH"}'
                        ),
                    )
                ]
            )
        return ChatResponse(text="LEAK_SENTINEL_RESPONSE")


class NoToolLLM:
    model = "fake"

    async def chat(self, request):
        return ChatResponse(text="done")


def test_runner_persists_authorized_actor_event(tmp_path: Path) -> None:
    app = OrchestratorServer(ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path)))
    runner = ConversationRunner(
        graph=app.graph,
        llm=NoToolLLM(),
        tool_registry=app.tools,
        todo_manager=app.todos,
        memory_manager=app.memory,
        skills=app.skills,
        project_root=app.project_root,
        working_dir=app.working_dir,
        token_budget=app.token_budget,
        layered_context=app.layered_context,
    )
    actor = ActorIdentity(
        schema_version=1,
        actor_id="user:42",
        subject="alice",
        tenant_id="org:7",
        roles=("USER",),
        session_id="session-actor",
    )

    list(runner.run("hello", iter(()), session_id="session-actor", actor=actor))

    events = app.context_store.events("session-actor")
    authorized = [event for event in events if event.kind == "actor/authorized"]
    assert len(authorized) == 1
    assert authorized[0].payload["actor_id"] == "user:42"
    assert authorized[0].payload["tenant_id"] == "org:7"
    app.context_store.close()


def test_conversation_runner_persists_plan_tool_diff_and_final_events(tmp_path: Path) -> None:
    app = OrchestratorServer(ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path)))
    llm = EventRecordingLLM()
    runner = ConversationRunner(
        graph=app.graph,
        llm=llm,
        tool_registry=app.tools,
        todo_manager=app.todos,
        memory_manager=app.memory,
        skills=app.skills,
        project_root=app.project_root,
        working_dir=app.working_dir,
        token_budget=app.token_budget,
        layered_context=app.layered_context,
    )
    messages = iter(
        [
            orchestrator_pb2.HarnessMessage(
                tool_result=orchestrator_pb2.ToolResult(
                    tool_name="Write",
                    output="LEAK_SENTINEL_RESULT",
                    tool_call_id="write-1",
                )
            )
        ]
    )

    responses = list(runner.run("write a file", messages, session_id="session-1"))

    assert responses[-1].done.success is True
    events = app.context_store.events("session-1")
    assert [event.kind for event in events] == [
        "execution_result",
        "tool_call",
        "plan",
        "tool_call",
        "execution_result",
        "file_diff",
        "reflection",
        "execution_result",
    ]
    assert events[2].payload["steps"] == ["write file"]
    assert events[3].payload["tool_name"] == "Write"
    assert events[5].payload["path"] == "src/out.txt"
    assert events[-1].payload["status"] == "completed"
    persisted = repr(events)
    assert "LEAK_SENTINEL_PATCH" not in persisted
    assert "LEAK_SENTINEL_RESULT" not in persisted
    assert "LEAK_SENTINEL_RESPONSE" not in persisted
    assert app.layered_context.search_memory("conversation outcome")
    restored = runner._initial_messages("conversation outcome", 1, session_id="session-1")
    assert "Long-term memory:" in "\n".join(str(message.content) for message in restored)
    app.context_store.close()


def test_managed_grpc_server_closes_context_store_on_stop(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    server = create_grpc_server(app)
    server.add_insecure_port("127.0.0.1:0")
    server.start()

    server.stop(grace=0)

    with pytest.raises(sqlite3.ProgrammingError, match="closed database"):
        app.context_store.events("session-1")


def test_context_persistence_failure_fails_closed_without_crashing_runner(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    runner = ConversationRunner(
        graph=app.graph,
        llm=NoToolLLM(),
        tool_registry=app.tools,
        todo_manager=app.todos,
        memory_manager=app.memory,
        skills=app.skills,
        project_root=app.project_root,
        working_dir=app.working_dir,
        token_budget=app.token_budget,
        layered_context=app.layered_context,
    )
    app.context_store.close()

    responses = list(runner.run("continue", iter(()), session_id="session-1"))

    assert responses[-1].done.success is False
    assert responses[-1].done.message == "context persistence unavailable"
