from __future__ import annotations

import pytest

from codeagent import orchestrator_pb2
from orchestrator.graph.main_graph import build_graph
from orchestrator.llm.client import ChatResponse, ToolCall, Usage
from orchestrator.memory.manager import MemoryManager
from orchestrator.runtime.conversation import ConversationRunner
from orchestrator.runtime.agent_loop import AgentLoopPluginRegistry, LoopEvent
from orchestrator.runtime.tools import ToolRegistry
from orchestrator.server import OrchestratorServer, OrchestratorService, ServerConfig
from orchestrator.skills.manager import SkillManager
from orchestrator.todo.manager import TodoManager


def test_registry_preserves_order_and_rejects_duplicate_names():
    registry = AgentLoopPluginRegistry()
    registry.register("first", lambda event: {"first": 1})
    registry.register("second", lambda event: {"second": 2})
    with pytest.raises(ValueError):
        registry.register("first", lambda event: {})
    event = LoopEvent(schema_version="1", session_id="s", turn=2, phase="loop_start")
    result = registry.emit(event)
    assert result.metadata == {"first": 1, "second": 2}
    assert result.errors == []


def test_plugin_errors_are_isolated_and_structured():
    registry = AgentLoopPluginRegistry()
    registry.register("bad", lambda event: (_ for _ in ()).throw(RuntimeError("private marker")))
    registry.register("good", lambda event: {"ok": True})
    result = registry.emit(LoopEvent("1", "s", 1, "model_before"))
    assert result.metadata == {"ok": True}
    assert result.errors[0]["plugin"] == "<redacted-plugin>"
    assert "private marker" not in str(result.errors)


def test_plugin_names_cannot_leak_from_failure_records():
    registry = AgentLoopPluginRegistry()
    registry.register("credential-shaped-plugin", lambda event: (_ for _ in ()).throw(RuntimeError("failed")))
    result = registry.emit(LoopEvent("1", "s", 1, "model_before"))
    assert result.errors[0]["plugin"] == "<redacted-plugin>"
    assert "credential-shaped-plugin" not in str(result.errors)


def test_production_grpc_runner_uses_server_plugin_registry(tmp_path):
    app = OrchestratorServer(
        ServerConfig(project_root=str(tmp_path), memory_dir=str(tmp_path / "memory"))
    )
    try:
        marker = object()
        app.loop_plugins.register("server-plugin", lambda event: {"marker": marker})
        runner = OrchestratorService(app)._new_runner()
        assert runner.loop_plugins is app.loop_plugins
        result = runner.loop_plugins.emit(LoopEvent("1", "s", 0, "loop_start"))
        assert result.metadata["marker"] is marker
    finally:
        app.close()


def test_event_contains_stable_schema_and_phase():
    event = LoopEvent("1", "session-x", 3, "tool_after", {"tool": "Echo"})
    assert event.schema_version == "1"
    assert event.phase == "tool_after"
    assert event.session_id == "session-x"
    assert event.turn == 3


def test_event_metadata_is_immutable():
    event = LoopEvent("1", "session-x", 1, "model_before", {"count": 1})
    with pytest.raises(TypeError):
        event.metadata["count"] = 2


class _PluginLoopLLM:
    model = "local-plugin-loop"

    def __init__(self) -> None:
        self.calls = 0

    async def chat(self, request):
        self.calls += 1
        if self.calls == 1:
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        id="read-1",
                        name="Read",
                        arguments={"path": "README.md"},
                        arguments_json='{"path":"README.md"}',
                    )
                ],
                usage=Usage(input_tokens=2, output_tokens=1),
            )
        return ChatResponse(text="PLUGIN_LOOP_E2E_OK", usage=Usage(input_tokens=2, output_tokens=1))


def test_runner_dispatches_ordered_lifecycle_and_contains_plugin_failure(tmp_path):
    llm = _PluginLoopLLM()
    registry = AgentLoopPluginRegistry()
    observed: list[tuple[str, int]] = []

    def recorder(event: LoopEvent):
        observed.append((event.phase, event.turn))
        return {"phase_seen": event.phase}

    registry.register("recorder", recorder)
    registry.register("failing", lambda event: (_ for _ in ()).throw(RuntimeError("private marker")))
    runner = ConversationRunner(
        graph=build_graph(),
        llm=llm,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        loop_plugins=registry,
    )
    tool_results = iter(
        [
            orchestrator_pb2.HarnessMessage(
                tool_result=orchestrator_pb2.ToolResult(
                    tool_name="Read",
                    tool_call_id="read-1",
                    output="README content",
                )
            )
        ]
    )

    responses = list(runner.run("inspect README", tool_results, session_id="plugin-session"))

    assert responses[-1].done.success is True
    assert [phase for phase, _turn in observed] == [
        "loop_start",
        "model_before",
        "model_after",
        "tool_before",
        "tool_after",
        "model_before",
        "model_after",
        "loop_end",
    ]
    assert runner.loop_plugin_errors == (
        {
            "plugin": "<redacted-plugin>",
            "error_type": "RuntimeError",
            "code": "plugin_callback_failed",
        },
    ) * len(observed)
    assert all("private marker" not in str(error) for error in runner.loop_plugin_errors)
    assert llm.calls == 2


def test_runner_emits_provider_identity_on_model_after(tmp_path):
    class IdentityLLM(_PluginLoopLLM):
        async def chat(self, request):
            self.calls += 1
            return ChatResponse(
                text="IDENTITY_OK",
                usage=Usage(input_tokens=2, output_tokens=1),
                model_identity={
                    "requested_model": "relay-model",
                    "reported_model": "relay-model-build",
                    "response_id": "response-123",
                    "system_fingerprint": "fp-123",
                    "identity_verified": True,
                },
            )

    observed = []
    registry = AgentLoopPluginRegistry()
    registry.register("recorder", lambda event: observed.append(event))
    runner = ConversationRunner(
        graph=build_graph(),
        llm=IdentityLLM(),
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        loop_plugins=registry,
    )

    responses = list(runner.run("identity", iter([]), session_id="identity-session"))

    assert responses[-1].done.success is True
    model_after = next(event for event in observed if event.phase == "model_after")
    assert model_after.metadata["model_identity"] == {
        "requested_model": "relay-model",
        "reported_model": "relay-model-build",
        "response_id": "response-123",
        "system_fingerprint": "fp-123",
        "identity_verified": True,
    }
