from __future__ import annotations

import sqlite3
import json
import os
import subprocess
import sys
import time
from pathlib import Path

import pytest

from codeagent import orchestrator_pb2
from orchestrator.identity import ActorIdentity
from orchestrator.llm.client import ChatResponse, ToolCall
from orchestrator.graph.nodes import GraphState
from orchestrator.graph.main_graph import MainGraph
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


class CountingNoToolLLM(NoToolLLM):
    def __init__(self) -> None:
        self.requests = 0

    async def chat(self, request):
        self.requests += 1
        return await super().chat(request)


class ResumeRecordingLLM(NoToolLLM):
    def __init__(self) -> None:
        self.requests = []

    async def chat(self, request):
        self.requests.append(request)
        return ChatResponse(text="resumed after tool")


@pytest.mark.parametrize("history", [
    [{"role": "user", "content": f"fact-{index}"} for index in range(45)],
    [{"role": "user", "content": "x" * 5000 + " exact-tail-fact"}],
    [{"role": "user", "content": f"fact-{index}:" + "x" * 3000} for index in range(15)],
])
def test_durable_runner_preserves_surface_before_context_budgeting(tmp_path, history):
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    llm = ResumeRecordingLLM()
    runner = ConversationRunner(
        graph=app.graph, llm=llm, tool_registry=app.tools,
        todo_manager=app.todos, memory_manager=app.memory, skills=app.skills,
        project_root=app.project_root, working_dir=app.working_dir,
        token_budget=app.token_budget, layered_context=app.layered_context,
        context_window=256_000,
    )
    try:
        results = list(runner.run(
            "Recall the original facts", iter(()), session_id="durable-history",
            history=history, run_id="run:durable-history", resume=True,
            surface_sha256="surface:durable-history",
        ))
        assert results[-1].done.success
        assert llm.requests
        supplied = [message.content for message in llm.requests[0].messages if message.role == "user"]
        assert supplied == [item["content"] for item in history] + ["Recall the original facts"]
    finally:
        app.close()


class ToolThenFailLLM:
    model = "fake"

    def __init__(self) -> None:
        self.requests = []

    async def chat(self, request):
        self.requests.append(request)
        if len(self.requests) == 1:
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        id="read-first-run",
                        name="Read",
                        arguments={"path": "README.md"},
                        arguments_json='{"path":"README.md"}',
                    )
                ]
            )
        raise RuntimeError("simulated process interruption")


class RecordingGraph:
    def __init__(self, delegate) -> None:
        self.delegate = delegate
        self.states = []
        self.name = getattr(delegate, "name", "recording-graph")

    def get_checkpoint(self, session_id):
        return self.delegate.get_checkpoint(session_id)

    def write_checkpoint(self, state, *, thread_id):
        self.states.append(state)
        return self.delegate.write_checkpoint(state, thread_id=thread_id)


class FailingCheckpointGraph:
    name = "failing-checkpoint"

    def get_checkpoint(self, session_id):
        return None

    def write_checkpoint(self, state, *, thread_id):
        raise OSError("checkpoint storage unavailable")


class FailingCheckpointGraphAfter(FailingCheckpointGraph):
    def __init__(self, fail_after: int) -> None:
        self.writes = 0
        self.fail_after = fail_after

    def write_checkpoint(self, state, *, thread_id):
        self.writes += 1
        if self.writes > self.fail_after:
            raise OSError("checkpoint storage unavailable")


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


def test_missing_tool_call_ids_are_stable_for_durable_retries() -> None:
    first = ConversationRunner._stable_tool_call_id(
        ToolCall(name="Write", arguments={"path": "out.txt", "content": "x"}),
        session_id="session-1",
        turn=2,
        index=0,
    )
    second = ConversationRunner._stable_tool_call_id(
        ToolCall(name="Write", arguments={"content": "x", "path": "out.txt"}),
        session_id="session-1",
        turn=2,
        index=0,
    )
    different_turn = ConversationRunner._stable_tool_call_id(
        ToolCall(name="Write", arguments={"path": "out.txt", "content": "x"}),
        session_id="session-1",
        turn=3,
        index=0,
    )

    assert first == second
    assert first.startswith("generated-tool-")
    assert first != different_turn


def test_duplicate_provider_tool_call_ids_are_rejected() -> None:
    calls = [
        ToolCall(name="Read", id="duplicate", arguments={"path": "a.txt"}),
        ToolCall(name="Read", id="duplicate", arguments={"path": "b.txt"}),
    ]

    with pytest.raises(ValueError, match="duplicate tool call id"):
        ConversationRunner._assign_tool_call_ids(calls, session_id="session-1", turn=1)


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


def test_normal_conversation_writes_typed_graph_checkpoint(tmp_path: Path) -> None:
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

    responses = list(
        runner.run(
            "hello",
            iter(()),
            session_id="graph-session",
            run_id="run:graph-session",
            resume=False,
            surface_sha256="surface:graph-session",
        )
    )

    assert responses[-1].done.success is True
    checkpoint = app.graph.get_checkpoint("graph-session")
    assert checkpoint is not None
    assert checkpoint.metadata["session_id"] == "graph-session"
    assert checkpoint.metadata["phase"] == "model_after"
    assert checkpoint.metadata["surface_sha256"] == "surface:graph-session"
    assert checkpoint.done is True
    app.close()


def test_first_run_persists_real_tool_after_surface_identity(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    graph = RecordingGraph(app.graph)
    runner = ConversationRunner(
        graph=graph,
        llm=ToolThenFailLLM(),
        tool_registry=app.tools,
        todo_manager=app.todos,
        memory_manager=app.memory,
        skills=app.skills,
        project_root=app.project_root,
        working_dir=app.working_dir,
        token_budget=app.token_budget,
        layered_context=app.layered_context,
    )

    with pytest.raises(RuntimeError, match="simulated process interruption"):
        list(
            runner.run(
                "read README",
                iter(
                    [
                        orchestrator_pb2.HarnessMessage(
                            tool_result=orchestrator_pb2.ToolResult(
                                tool_name="Read",
                                tool_call_id="read-first-run",
                                output="README contents",
                            )
                        )
                    ]
                ),
                session_id="tool-after-first-run",
                run_id="run:tool-after-first-run",
                surface_sha256="surface:tool-after-first-run",
            )
        )

    tool_after = [state for state in graph.states if state.metadata.get("phase") == "tool_after"]
    assert tool_after
    assert tool_after[-1].metadata["surface_sha256"] == "surface:tool-after-first-run"
    assert tool_after[-1].metadata["tool_call_id"] == "read-first-run"
    app.close()


def test_normal_conversation_resumes_pending_checkpoint_turn(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    app.graph.write_checkpoint(
        GraphState(
            metadata={
                "session_id": "resume-session",
                "phase": "tool_after",
                "turn": 2,
            },
            tool_rounds=2,
            done=False,
            next_node="route",
        ),
        thread_id="resume-session",
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

    responses = list(runner.run("continue", iter(()), session_id="resume-session"))

    assert responses[-1].done.success is True
    assert runner._loop_last_turn == 3
    resumed = [
        event
        for event in app.context_store.events("resume-session")
        if event.payload.get("status") == "checkpoint_resumed"
    ]
    assert len(resumed) == 1
    assert resumed[0].payload["resume_turn"] == 3
    assert "continue" not in repr(resumed[0].payload)
    app.close()


def test_model_after_checkpoint_replays_tool_request_without_calling_model(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    llm = ResumeRecordingLLM()
    run_id = "run:model-after-replay"
    session_id = "model-after-replay"
    app.graph.write_checkpoint(
        GraphState(
            metadata={
                "session_id": session_id,
                "run_id": run_id,
                "phase": "model_after",
                "turn": 1,
                "history_sha256": ConversationRunner._digest_value([]),
                "surface_sha256": "surface:model-after-replay",
            },
            tool_requests=[
                {
                    "id": "read-1",
                    "name": "Read",
                    "arguments_json": '{"path":"README.md"}',
                }
            ],
            done=False,
            next_node="route",
        ),
        thread_id=session_id,
    )
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
    responses = list(
        runner.run(
            "",
            iter(
                [
                    orchestrator_pb2.HarnessMessage(
                        tool_result=orchestrator_pb2.ToolResult(
                            tool_name="Read",
                            tool_call_id="read-1",
                            output="README contents",
                        )
                    )
                ]
            ),
            session_id=session_id,
            run_id=run_id,
            resume=True,
            surface_sha256="surface:model-after-replay",
        )
    )

    assert responses[-1].done.success is True
    assert [item.tool_request.tool_call_id for item in responses if item.HasField("tool_request")] == [
        "read-1"
    ]
    assert len(llm.requests) == 1
    assert all(
        not (message.role == "user" and not str(message.content).strip())
        for message in llm.requests[0].messages
    )
    app.close()


def test_tool_after_resume_uses_harness_history_without_empty_user_turn(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    llm = ResumeRecordingLLM()
    session_id = "tool-after-history"
    run_id = "run:tool-after-history"
    app.graph.write_checkpoint(
        GraphState(
            metadata={
                "session_id": session_id,
                "run_id": run_id,
                "phase": "tool_after",
                "turn": 1,
                # The Harness Surface has legitimately grown after the
                # checkpoint with the assistant call and its tool result.
                "history_sha256": ConversationRunner._digest_value([]),
                "surface_sha256": "surface:tool-after-history",
            },
            done=False,
            next_node="route",
        ),
        thread_id=session_id,
    )
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

    responses = list(
        runner.run(
            "",
            iter(()),
            session_id=session_id,
            run_id=run_id,
            resume=True,
            surface_sha256="surface:tool-after-history",
            history=[
                {
                    "role": "assistant",
                    "content": "",
                    "tool_calls": [
                        {
                            "id": "read-2",
                            "name": "Read",
                            "arguments_json": '{"path":"README.md"}',
                        }
                    ],
                },
                {
                    "role": "tool",
                    "name": "Read",
                    "tool_call_id": "read-2",
                    "content": "README contents",
                },
            ],
        )
    )

    assert responses[-1].done.success is True
    messages = llm.requests[0].messages
    assert any(message.role == "assistant" and message.tool_calls for message in messages)
    assert any(message.role == "tool" and message.tool_call_id == "read-2" for message in messages)
    assert all(
        not (message.role == "user" and not str(message.content).strip())
        for message in messages
    )
    app.close()


def test_resume_rejects_changed_checkpoint_surface_identity(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    app.graph.write_checkpoint(
        GraphState(
            metadata={
                "session_id": "surface-mismatch",
                "run_id": "run:surface-mismatch",
                "phase": "tool_after",
                "turn": 1,
                "surface_sha256": "surface:original",
            },
            done=False,
            next_node="route",
        ),
        thread_id="surface-mismatch",
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

    with pytest.raises(ValueError, match="checkpoint surface sha256 does not match request"):
        runner.load_checkpoint(
            "surface-mismatch",
            run_id="run:surface-mismatch",
            resume=True,
            surface_sha256="surface:changed",
        )
    app.close()


def test_resume_rejects_checkpoint_without_surface_identity(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    app.graph.write_checkpoint(
        GraphState(
            metadata={
                "session_id": "surface-missing",
                "run_id": "run:surface-missing",
                "phase": "tool_after",
                "turn": 1,
            },
            done=False,
            next_node="route",
        ),
        thread_id="surface-missing",
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

    with pytest.raises(ValueError, match="checkpoint surface sha256 is missing"):
        runner.load_checkpoint(
            "surface-missing",
            run_id="run:surface-missing",
            resume=True,
            surface_sha256="surface:expected",
        )
    app.close()


@pytest.mark.parametrize(
    ("run_id", "surface_sha256", "message"),
    [
        ("", "surface:required", "continuation run id is required"),
        ("run:required", "", "continuation surface sha256 is required"),
    ],
)
def test_runner_resume_requires_both_identities(
    tmp_path: Path, run_id: str, surface_sha256: str, message: str
) -> None:
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

    with pytest.raises(ValueError, match=message):
        list(
            runner.run(
                "continue",
                iter(()),
                session_id="resume-identities",
                run_id=run_id,
                resume=True,
                surface_sha256=surface_sha256,
            )
        )
    app.close()


def test_explicit_run_id_rejects_unfinished_checkpoint_from_another_run(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    app.graph.write_checkpoint(
        GraphState(
            metadata={
                "session_id": "run-isolation",
                "run_id": "run:old",
                "phase": "tool_after",
                "turn": 1,
                "surface_sha256": "surface:old",
            },
            done=False,
            next_node="route",
        ),
        thread_id="run-isolation",
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

    with pytest.raises(ValueError, match="checkpoint run id does not match request"):
        runner.load_checkpoint(
            "run-isolation",
            run_id="run:new",
            resume=True,
            surface_sha256="surface:old",
        )
    with pytest.raises(ValueError, match="new turn must have a new surface"):
        runner.load_checkpoint("run-isolation", run_id="run:new", resume=True,
                               surface_sha256="surface:old", new_turn=True)
    with pytest.raises(ValueError, match="independent durable run"):
        runner.load_checkpoint("run-isolation", run_id="run:new", resume=True,
                               surface_sha256="surface:new", new_turn=True,
                               retry_of_run_id="run:old")
    assert runner.load_checkpoint("run-isolation", run_id="run:new", resume=True,
                                  surface_sha256="surface:new", new_turn=True) is None
    app.close()


def test_new_user_turn_can_follow_an_unfinished_foreign_checkpoint(tmp_path):
    import grpc
    from codeagent import orchestrator_pb2_grpc
    from concurrent import futures
    from orchestrator.server import OrchestratorService

    app = OrchestratorServer(ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path)))
    app.llm = ResumeRecordingLLM()
    app.fast_llm = None
    app.provider_clients = {"default": app.llm}
    app.graph.write_checkpoint(GraphState(metadata={
        "session_id": "new-user-turn", "run_id": "run:old", "phase": "model_before",
        "turn": 1, "surface_sha256": "surface:old",
    }, done=False, next_node="route"), thread_id="new-user-turn")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=2))
    orchestrator_pb2_grpc.add_OrchestratorServicer_to_server(OrchestratorService(app), server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
            responses = list(orchestrator_pb2_grpc.OrchestratorStub(channel).Converse(iter([
                orchestrator_pb2.HarnessMessage(user_input=orchestrator_pb2.UserInput(
                    text="A new instruction after the failed turn", session_id="new-user-turn",
                    actor=orchestrator_pb2.ActorContext(schema_version=1, actor_id="user:7", subject="test", tenant_id="test", roles=["USER"], session_id="new-user-turn"),
                )),
            ]), metadata=[("x-code-agent-run-id", "run:new"), ("x-code-agent-resume", "true"),
                          ("x-code-agent-surface-sha256", "surface:new"), ("x-code-agent-new-turn", "true")]))
        assert responses[-1].done.success
        assert app.llm.requests
    finally:
        server.stop(grace=0).wait()
        app.close()


def test_resume_allows_explicit_retry_of_failed_checkpoint(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    app.graph.write_checkpoint(
        GraphState(
            metadata={
                "session_id": "run-retry",
                "run_id": "run:failed",
                "phase": "model_before",
                "turn": 1,
                "surface_sha256": "surface:retry",
            },
            done=False,
            next_node="route",
        ),
        thread_id="run-retry",
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

    checkpoint = runner.load_checkpoint(
        "run-retry",
        run_id="run:new",
        resume=True,
        surface_sha256="surface:retry",
        retry_of_run_id="run:failed",
    )
    assert checkpoint is not None
    assert checkpoint.phase == "model_before"

    # A later failed attempt may have persisted a checkpoint under its own
    # run identity. The original failed run remains the stable lineage root,
    # so a subsequent retry must still be able to use that root predecessor.
    app.graph.write_checkpoint(
        GraphState(
            metadata={
                "session_id": "run-retry",
                "run_id": "run:middle",
                "retry_root_run_id": "run:failed",
                "phase": "model_before",
                "turn": 1,
                "surface_sha256": "surface:retry",
            },
            done=False,
            next_node="route",
        ),
        thread_id="run-retry",
    )
    rooted_retry = runner.load_checkpoint(
        "run-retry",
        run_id="run:latest",
        resume=True,
        surface_sha256="surface:retry",
        retry_of_run_id="run:failed",
        retry_of_run_ids=("run:middle", "run:failed"),
    )
    assert rooted_retry is not None
    assert rooted_retry.state.metadata["run_id"] == "run:middle"
    with pytest.raises(ValueError, match="checkpoint run id does not match request"):
        runner.load_checkpoint(
            "run-retry",
            run_id="run:new",
            resume=True,
            surface_sha256="surface:retry",
            retry_of_run_ids=("run:other",),
        )
    app.close()


def test_resume_allows_retry_of_completed_model_after_checkpoint(tmp_path: Path) -> None:
    app = OrchestratorServer(ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path)))
    app.graph.write_checkpoint(
        GraphState(
            metadata={"session_id": "run-done-retry", "run_id": "run:failed",
                     "phase": "model_after", "turn": 1, "surface_sha256": "surface:done"},
            response="durable provider result", done=True, next_node="done",
        ), thread_id="run-done-retry",
    )
    runner = ConversationRunner(
        graph=app.graph, llm=NoToolLLM(), tool_registry=app.tools,
        todo_manager=app.todos, memory_manager=app.memory, skills=app.skills,
        project_root=app.project_root, working_dir=app.working_dir,
        token_budget=app.token_budget, layered_context=app.layered_context,
    )
    checkpoint = runner.load_checkpoint(
        "run-done-retry", run_id="run:new", resume=True,
        surface_sha256="surface:done", retry_of_run_id="run:failed",
    )
    assert checkpoint is not None
    assert checkpoint.state.done is True
    app.close()


@pytest.mark.parametrize("retry_run_id", ["run:original", "run:retry"])
def test_completed_response_replays_after_reopen_without_model_call(tmp_path: Path, retry_run_id: str) -> None:
    config = ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    app = OrchestratorServer(config)
    try:
        runner = ConversationRunner(
            graph=app.graph, llm=NoToolLLM(), tool_registry=app.tools,
            todo_manager=app.todos, memory_manager=app.memory, skills=app.skills,
            project_root=app.project_root, working_dir=app.working_dir,
            token_budget=app.token_budget, layered_context=app.layered_context,
        )
        original = list(runner.run(
            "hello", iter(()), session_id="replay-completed", run_id="run:original",
            surface_sha256="surface:original",
        ))
        assert original[-1].done.success
        expected = "".join(item.text.text for item in original if item.HasField("text"))
        assert expected == "done"
    finally:
        app.close()

    app = OrchestratorServer(config)
    try:
        llm = CountingNoToolLLM()
        runner = ConversationRunner(
            graph=app.graph, llm=llm, tool_registry=app.tools,
            todo_manager=app.todos, memory_manager=app.memory, skills=app.skills,
            project_root=app.project_root, working_dir=app.working_dir,
            token_budget=app.token_budget, layered_context=app.layered_context,
        )
        replayed = list(runner.run(
            "hello", iter(()), session_id="replay-completed", run_id=retry_run_id,
            resume=True, surface_sha256="surface:original", retry_of_run_id="run:original",
        ))
        assert replayed[-1].done.success
        assert llm.requests == 0
        assert "".join(item.text.text for item in replayed if item.HasField("text")) == expected
        checkpoint = app.graph.get_checkpoint("replay-completed")
        assert checkpoint.metadata["retry_root_run_id"] == "run:original"
        assert checkpoint.metadata["run_id"] == "run:original"
    finally:
        app.close()


@pytest.mark.parametrize("done", [False, True])
def test_model_after_checkpoint_without_replay_payload_fails_closed(tmp_path: Path, done: bool) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    app.graph.write_checkpoint(
        GraphState(
            metadata={
                "session_id": "unreplayable-model-after",
                "run_id": "run:unreplayable",
                "phase": "model_after",
                "turn": 1,
                "history_sha256": ConversationRunner._digest_value([]),
                "surface_sha256": "surface:unreplayable",
            },
            done=done,
            next_node="route",
        ),
        thread_id="unreplayable-model-after",
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

    with pytest.raises(ValueError, match="model_after checkpoint is not replayable"):
        list(
            runner.run(
                "",
                iter(()),
                session_id="unreplayable-model-after",
                run_id="run:unreplayable",
                resume=True,
                surface_sha256="surface:unreplayable",
            )
        )
    app.close()


def test_normal_conversation_rejects_checkpoint_from_different_history(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    app.graph.write_checkpoint(
        GraphState(
            metadata={
                "session_id": "mismatched-session",
                "phase": "model_before",
                "turn": 1,
                "history_sha256": ConversationRunner._digest_value(
                    [{"role": "user", "content": "original"}]
                ),
            },
            done=False,
            next_node="route",
        ),
        thread_id="mismatched-session",
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

    with pytest.raises(ValueError, match="checkpoint history does not match request"):
        list(
            runner.run(
                "continue",
                iter(()),
                session_id="mismatched-session",
                history=[{"role": "user", "content": "different"}],
            )
        )
    app.close()


def test_normal_conversation_fails_closed_when_checkpoint_cannot_be_read(tmp_path: Path, monkeypatch) -> None:
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

    def unreadable_checkpoint(_graph, _session_id: str):
        raise sqlite3.DatabaseError("checkpoint database is corrupt")

    monkeypatch.setattr(MainGraph, "get_checkpoint", unreadable_checkpoint)
    with pytest.raises(sqlite3.DatabaseError, match="corrupt"):
        list(runner.run("continue", iter(()), session_id="corrupt-checkpoint"))
    app.close()


def test_completed_response_replays_across_python_processes(tmp_path: Path) -> None:
    script = Path(__file__).parent / "e2e" / "conversation_checkpoint_process.py"
    environment = os.environ.copy()
    environment["PYTHONPATH"] = str(Path(__file__).resolve().parents[1])
    command = [sys.executable, str(script), "--root", str(tmp_path),
               "--marker", str(tmp_path / "unused.marker"), "--completed-result"]
    original = subprocess.run(command, env=environment, capture_output=True, text=True, timeout=60)
    assert original.returncode == 0, original.stderr
    retried = subprocess.run(command + ["--resume"], env=environment, capture_output=True, text=True, timeout=60)
    assert retried.returncode == 0, retried.stderr
    assert json.loads(original.stdout.splitlines()[-1]) == {
        "done": True, "text": "resumed", "model_calls": 1, "retry_root": "run:original",
    }
    assert json.loads(retried.stdout.splitlines()[-1]) == {
        "done": True, "text": "resumed", "model_calls": 0, "retry_root": "run:original",
    }


def test_normal_conversation_checkpoint_survives_python_process_restart(tmp_path: Path) -> None:
    script = Path(__file__).parent / "e2e" / "conversation_checkpoint_process.py"
    environment = os.environ.copy()
    repository_root = Path(__file__).resolve().parents[1]
    environment["PYTHONPATH"] = str(repository_root)
    marker = tmp_path / "checkpoint.marker"

    first = subprocess.Popen(
        [
            sys.executable,
            str(script),
            "--root",
            str(tmp_path / "project"),
            "--marker",
            str(marker),
        ],
        cwd=repository_root,
        env=environment,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        deadline = time.monotonic() + 15
        while not marker.exists() and first.poll() is None and time.monotonic() < deadline:
            time.sleep(0.05)
        assert marker.exists(), first.stderr.read() if first.poll() is not None else ""
    finally:
        if first.poll() is None:
            first.kill()
        first.wait(timeout=10)

    resumed = subprocess.run(
        [
            sys.executable,
            str(script),
            "--root",
            str(tmp_path / "project"),
            "--marker",
            str(marker),
            "--resume",
        ],
        cwd=repository_root,
        env=environment,
        capture_output=True,
        text=True,
        timeout=30,
        check=True,
    )

    assert resumed.stdout.strip().splitlines()[-1] == (
        '{"checkpoint_phase": "model_after", "done": true, "turn": 3}'
    )


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
    llm = CountingNoToolLLM()
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
    app.context_store.close()

    responses = list(runner.run("continue", iter(()), session_id="session-1"))

    assert responses[-1].done.success is False
    assert responses[-1].done.message == "context persistence unavailable"
    assert llm.requests == 0


def test_checkpoint_write_failure_fails_closed_before_next_model_call(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    llm = CountingNoToolLLM()
    runner = ConversationRunner(
        graph=FailingCheckpointGraph(),
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

    responses = list(runner.run("continue", iter(()), session_id="session-1"))

    assert responses[-1].done.success is False
    assert responses[-1].done.message == "checkpoint persistence unavailable"
    assert llm.requests == 0
    app.context_store.close()


def test_batch_checkpoint_failure_stops_processing_later_results(tmp_path: Path) -> None:
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    runner = ConversationRunner(
        graph=FailingCheckpointGraphAfter(fail_after=1),
        llm=None,
        tool_registry=app.tools,
        todo_manager=app.todos,
        memory_manager=app.memory,
        skills=app.skills,
        project_root=app.project_root,
        working_dir=app.working_dir,
        token_budget=app.token_budget,
        layered_context=app.layered_context,
    )
    calls = [
        ToolCall(id="read-1", name="Read", arguments={"path": "a.txt"}),
        ToolCall(id="glob-1", name="Glob", arguments={"pattern": "*.py"}),
    ]
    messages = list(
        runner._handle_tool_batch(
            calls,
            iter(
                [
                    orchestrator_pb2.HarnessMessage(
                        tool_result=orchestrator_pb2.ToolResult(
                            tool_name="Read", tool_call_id="read-1", output="a"
                        )
                    ),
                    orchestrator_pb2.HarnessMessage(
                        tool_result=orchestrator_pb2.ToolResult(
                            tool_name="Glob", tool_call_id="glob-1", output="b"
                        )
                    ),
                ]
            ),
            [],
            {},
            1,
            0,
            0,
            0.0,
            0,
            session_id="session-1",
        )
    )

    assert messages[-1].done.success is False
    assert messages[-1].done.message == "checkpoint persistence unavailable"
    persisted_results = [
        event.payload.get("tool_call_id")
        for event in app.context_store.events("session-1")
        if event.kind == "execution_result" and event.payload.get("tool_call_id")
    ]
    assert persisted_results == ["read-1", "glob-1"]
    assert len([message for message in messages if message.HasField("tool_request_batch")]) == 1
    assert len(messages) == 2
    app.context_store.close()
