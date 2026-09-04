from __future__ import annotations

import pytest

from codeagent import orchestrator_pb2
from orchestrator.graph.main_graph import build_graph
from orchestrator.llm.client import ChatResponse
from orchestrator.llm.client import ToolCall, Usage
from orchestrator.memory.manager import MemoryManager
from orchestrator.runtime.conversation import ConversationRunner
from orchestrator.runtime.hooks import (
    CommandRegistry,
    HookEvent,
    HookRegistry,
    HookResult,
)
from orchestrator.runtime.tools import ToolRegistry
from orchestrator.skills.manager import SkillManager
from orchestrator.todo.manager import TodoManager
from orchestrator.server import OrchestratorServer, OrchestratorService, ServerConfig


def test_hooks_run_serially_and_collect_context_in_registration_order() -> None:
    registry = HookRegistry()
    observed: list[str] = []
    registry.register("first", "pre_step", lambda event: observed.append("first") or HookResult(context="one"))
    registry.register("tool-only", "pre_tool", lambda event: observed.append("tool") or None, matcher="Read")
    registry.register("second", "pre_step", lambda event: observed.append("second") or HookResult(context="two"))

    result = registry.dispatch(HookEvent("pre_step", "session", 1))

    assert observed == ["first", "second"]
    assert result.context == ("one", "two")
    assert result.blocked is False


def test_hook_matcher_and_blocking_stop_later_hooks() -> None:
    registry = HookRegistry()
    observed: list[str] = []
    registry.register("wrong-tool", "pre_tool", lambda event: observed.append("wrong"), matcher="Write")
    registry.register("block", "pre_tool", lambda event: HookResult(cancel=True, message="denied"), matcher="Read")
    registry.register("after", "pre_tool", lambda event: observed.append("after"), matcher="Read")

    result = registry.dispatch(HookEvent("pre_tool", "session", 1, tool_name="Read"))

    assert observed == []
    assert result.blocked is True
    assert result.messages == ("denied",)


def test_hook_callback_failure_is_sanitized_and_does_not_abort_dispatch() -> None:
    registry = HookRegistry()
    registry.register("credential-shaped-hook", "post_model", lambda event: (_ for _ in ()).throw(RuntimeError("private marker")))
    registry.register("good", "post_model", lambda event: HookResult(context="safe"))

    result = registry.dispatch(HookEvent("post_model", "session", 1))

    assert result.context == ("safe",)
    assert result.errors == ({"hook": "<redacted-hook>", "error_type": "RuntimeError", "code": "hook_callback_failed"},)
    assert "private marker" not in str(result.errors)


def test_hook_event_and_command_context_are_recursively_immutable() -> None:
    event = HookEvent(
        "pre_step",
        "session",
        1,
        payload={"nested": {"items": ["one"]}},
        metadata={"routes": ["default"]},
    )

    with pytest.raises(TypeError):
        event.payload["nested"]["extra"] = True
    assert event.payload["nested"]["items"] == ("one",)
    assert event.metadata["routes"] == ("default",)

    commands = CommandRegistry()

    def mutate_context(args, context):
        context["nested"]["value"] = "changed"

    commands.register("mutate", mutate_context)
    result = commands.dispatch("/mutate", {"nested": {"value": "original"}})
    assert result is not None
    assert result.success is False


def test_command_registry_dispatches_named_extension_with_arguments() -> None:
    registry = CommandRegistry()
    registry.register("greet", lambda args, context: f"hello {args} from {context['session_id']}")

    result = registry.dispatch("/greet world", {"session_id": "s1"})

    assert result is not None
    assert result.handled is True
    assert result.text == "hello world from s1"


def test_command_registry_rejects_duplicate_names() -> None:
    registry = CommandRegistry()
    registry.register("greet", lambda args, context: None)

    with pytest.raises(ValueError):
        registry.register("greet", lambda args, context: None)

    with pytest.raises(ValueError):
        registry.register("Invalid/Name", lambda args, context: None)
    assert registry.dispatch("/invalid/name") is None


def test_command_registry_contains_malformed_handler_results() -> None:
    registry = CommandRegistry()
    registry.register("broken", lambda args, context: object())

    result = registry.dispatch("/broken")

    assert result is not None
    assert result.success is False
    assert result.text == "command failed"


def test_command_and_hook_results_are_redacted_at_extension_boundary() -> None:
    commands = CommandRegistry()
    commands.register("show", lambda args, context: "api_key=not-a-real-secret")
    command = commands.dispatch("/show")
    assert command is not None
    assert "not-a-real-secret" not in command.text

    hooks = HookRegistry()
    hooks.register(
        "context",
        "pre_step",
        lambda event: HookResult(context="Bearer not-a-real-bearer", message="password=not-real"),
    )
    hook = hooks.dispatch(HookEvent("pre_step", "session", 1))
    assert "not-a-real-bearer" not in str(hook.context)
    assert "not-real" not in str(hook.messages)


class _NoCallLLM:
    model = "hooks-test"

    def __init__(self) -> None:
        self.calls = 0

    async def chat(self, request):
        self.calls += 1
        return ChatResponse(text="unexpected")


def test_runner_command_extension_handles_input_before_model_call(tmp_path) -> None:
    llm = _NoCallLLM()
    commands = CommandRegistry()
    commands.register("status", lambda args, context: "STATUS_OK")
    runner = ConversationRunner(
        graph=build_graph(),
        llm=llm,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        commands=commands,
    )

    responses = list(runner.run("/status", iter([]), session_id="command-session"))

    assert llm.calls == 0
    assert any(response.HasField("text") and response.text.text == "STATUS_OK" for response in responses)
    assert responses[-1].done.success is True


class _HookLifecycleLLM:
    model = "hooks-lifecycle"

    def __init__(self) -> None:
        self.requests = []

    async def chat(self, request):
        self.requests.append(request)
        if len(self.requests) == 1:
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
        return ChatResponse(text="HOOK_E2E_OK", usage=Usage(input_tokens=2, output_tokens=1))


def test_runner_dispatches_mutating_hook_lifecycle_without_breaking_tool_pairs(tmp_path) -> None:
    llm = _HookLifecycleLLM()
    hooks = HookRegistry()
    phases: list[str] = []

    def session_start(event):
        phases.append(event.phase)
        return HookResult(context="session-start-context")

    def pre_step(event):
        phases.append(event.phase)
        return HookResult(context=f"pre-step-{event.turn}")

    def post_model(event):
        phases.append(event.phase)
        return HookResult(context="post-model-context")

    def pre_tool(event):
        phases.append(f"{event.phase}:{event.tool_name}")

    def post_tool(event):
        phases.append(f"{event.phase}:{event.tool_name}")
        return HookResult(context="post-tool-context")

    def session_end(event):
        phases.append(event.phase)

    hooks.register("start", "session_start", session_start)
    hooks.register("pre-step", "pre_step", pre_step)
    hooks.register("post-model", "post_model", post_model)
    hooks.register("pre-tool", "pre_tool", pre_tool, matcher="Read")
    hooks.register("post-tool", "post_tool", post_tool, matcher="Read")
    hooks.register("stopping", "turn_stopping", lambda event: phases.append(event.phase))
    hooks.register("end", "session_end", session_end)
    runner = ConversationRunner(
        graph=build_graph(),
        llm=llm,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        hooks=hooks,
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

    responses = list(runner.run("inspect README", tool_results, session_id="hook-session"))

    assert responses[-1].done.success is True
    assert phases == [
        "session_start",
        "pre_step",
        "post_model",
        "pre_tool:Read",
        "post_tool:Read",
        "pre_step",
        "post_model",
        "turn_stopping",
        "session_end",
    ]
    assert any(
        message.role == "system" and "session-start-context" in str(message.content)
        for message in llm.requests[0].messages
    )
    assert any(
        message.role == "system" and "pre-step-1" in str(message.content)
        for message in llm.requests[0].messages
    )
    assert any(
        message.role == "system" and "post-tool-context" in str(message.content)
        for message in llm.requests[1].messages
    )


def test_runner_pre_tool_block_closes_tool_pair_without_calling_harness(tmp_path) -> None:
    llm = _HookLifecycleLLM()
    hooks = HookRegistry()
    hooks.register(
        "deny-read",
        "pre_tool",
        lambda event: HookResult(cancel=True, message="read denied"),
        matcher="Read",
    )
    runner = ConversationRunner(
        graph=build_graph(),
        llm=llm,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        hooks=hooks,
    )

    responses = list(runner.run("inspect README", iter(()), session_id="blocked-hook"))

    assert responses[-1].done.success is True
    assert not any(response.HasField("tool_request") for response in responses)
    messages = llm.requests[1].messages
    assistant_index = next(index for index, message in enumerate(messages) if message.role == "assistant")
    assert messages[assistant_index + 1].role == "tool"
    assert messages[assistant_index + 1].is_error is True
    assert "read denied" in str(messages[assistant_index + 1].content)


def test_post_model_cancel_is_observation_only_after_streamed_output(tmp_path) -> None:
    llm = _NoCallLLM()
    hooks = HookRegistry()
    hooks.register(
        "observer",
        "post_model",
        lambda event: HookResult(cancel=True, message="cannot retract output"),
    )
    runner = ConversationRunner(
        graph=build_graph(),
        llm=llm,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        hooks=hooks,
    )

    responses = list(runner.run("hello", iter(()), session_id="post-model-observer"))

    assert responses[-1].done.success is True
    assert any(
        response.HasField("text") and response.text.text == "unexpected"
        for response in responses
    )
    assert not any(
        response.HasField("text") and "cannot retract" in response.text.text
        for response in responses
    )


def test_runner_exposes_sanitized_hook_error_with_phase(tmp_path) -> None:
    hooks = HookRegistry()
    hooks.register(
        "failing",
        "session_start",
        lambda event: (_ for _ in ()).throw(RuntimeError("private failure")),
    )
    runner = ConversationRunner(
        graph=build_graph(),
        llm=_NoCallLLM(),
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        hooks=hooks,
    )

    responses = list(runner.run("hello", iter(()), session_id="hook-error"))

    assert responses[-1].done.success is True
    assert runner.hook_errors == (
        {
            "phase": "session_start",
            "hook": "<redacted-hook>",
            "error_type": "RuntimeError",
            "code": "hook_callback_failed",
        },
    )
    assert "private failure" not in str(runner.hook_errors)


def test_fallback_path_dispatches_step_and_tool_hooks(tmp_path) -> None:
    hooks = HookRegistry()
    phases: list[str] = []
    for phase in ("pre_step", "pre_tool", "post_tool"):
        hooks.register(
            phase,
            phase,
            lambda event, phase=phase: phases.append(f"{phase}:{event.tool_name}"),
        )
    runner = ConversationRunner(
        graph=build_graph(),
        llm=None,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        hooks=hooks,
    )
    tool_results = iter(
        [
            orchestrator_pb2.HarnessMessage(
                tool_result=orchestrator_pb2.ToolResult(
                    tool_name="Glob",
                    tool_call_id="fallback-glob",
                    output="README.md",
                )
            )
        ]
    )

    responses = list(runner.run("inspect", tool_results, session_id="fallback-hooks"))

    assert responses[-1].done.success is True
    assert phases == ["pre_step:", "pre_tool:Glob", "post_tool:Glob"]


def test_fallback_path_redacts_harness_tool_errors_before_returning_them(tmp_path) -> None:
    private_value = "fallback-private-value"
    runner = ConversationRunner(
        graph=build_graph(),
        llm=None,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
    )
    tool_results = iter(
        [
            orchestrator_pb2.HarnessMessage(
                tool_result=orchestrator_pb2.ToolResult(
                    tool_name="Glob",
                    tool_call_id="fallback-glob",
                    error=f"api_key={private_value}",
                )
            )
        ]
    )

    responses = list(runner.run("inspect", tool_results, session_id="fallback-redaction"))

    returned_text = "\n".join(
        response.text.text for response in responses if response.HasField("text")
    )
    assert private_value not in returned_text
    assert "api_key=<redacted>" in returned_text


class _TwoToolLLM:
    model = "hooks-two-tools"

    def __init__(self) -> None:
        self.requests = []

    async def chat(self, request):
        self.requests.append(request)
        return ChatResponse(
            tool_calls=[
                ToolCall(id="read-1", name="Read", arguments={"path": "README.md"}),
                ToolCall(id="grep-1", name="Grep", arguments={"pattern": "CodeOps"}),
            ],
            usage=Usage(input_tokens=2, output_tokens=1),
        )


def test_post_tool_block_skips_remaining_tools_and_preserves_all_pairs(tmp_path) -> None:
    llm = _TwoToolLLM()
    hooks = HookRegistry()
    hooks.register(
        "stop-after-read",
        "post_tool",
        lambda event: HookResult(cancel=True, message="stop tool step"),
        matcher="Read",
    )
    runner = ConversationRunner(
        graph=build_graph(),
        llm=llm,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        hooks=hooks,
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

    responses = list(runner.run("inspect repository", tool_results, session_id="post-tool-stop"))

    tool_requests = [response.tool_request for response in responses if response.HasField("tool_request")]
    assert [request.tool_name for request in tool_requests] == ["Read"]
    assert responses[-1].done.success is False
    assert len(llm.requests) == 1


class _CachedToolLLM:
    model = "hooks-cached-tool"

    def __init__(self) -> None:
        self.calls = 0

    async def chat(self, request):
        self.calls += 1
        if self.calls <= 2:
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        id=f"read-{self.calls}",
                        name="Read",
                        arguments={"path": "README.md"},
                    )
                ],
                usage=Usage(input_tokens=2, output_tokens=1),
            )
        return ChatResponse(text="CACHE_OK", usage=Usage(input_tokens=2, output_tokens=1))


def test_cached_tool_results_still_dispatch_pre_and_post_hooks(tmp_path) -> None:
    llm = _CachedToolLLM()
    hooks = HookRegistry()
    post_events: list[tuple[str, bool]] = []
    pre_tools: list[str] = []
    hooks.register(
        "pre-read",
        "pre_tool",
        lambda event: pre_tools.append(event.tool_name),
        matcher="Read",
    )
    hooks.register(
        "post-read",
        "post_tool",
        lambda event: post_events.append((event.tool_name, bool(event.payload["cached"]))),
        matcher="Read",
    )
    runner = ConversationRunner(
        graph=build_graph(),
        llm=llm,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        hooks=hooks,
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

    responses = list(runner.run("read twice", tool_results, session_id="cached-hooks"))

    assert responses[-1].done.success is True
    assert pre_tools == ["Read", "Read"]
    assert post_events == [("Read", False), ("Read", True)]
    assert sum(response.HasField("tool_request") for response in responses) == 1


class _WorkflowToolLLM:
    model = "hooks-workflow"

    async def chat(self, request):
        return ChatResponse(
            tool_calls=[
                ToolCall(
                    id="workflow-1",
                    name="RunWorkflow",
                    arguments={
                        "id": "workflow",
                        "workers": [
                            {"id": "worker", "title": "Worker", "objective": "Run"}
                        ],
                    },
                )
            ],
            usage=Usage(input_tokens=2, output_tokens=1),
        )


def test_workflow_runtime_failure_dispatches_post_tool_hook(monkeypatch, tmp_path) -> None:
    observed: list[tuple[str, bool]] = []
    hooks = HookRegistry()
    hooks.register(
        "workflow-failure",
        "post_tool",
        lambda event: observed.append((event.tool_name, bool(event.payload["is_error"])))
        or HookResult(cancel=True, message="stop after workflow failure"),
        matcher="RunWorkflow",
    )

    def fail_workflow(self, workflow):
        raise RuntimeError("workflow failed")

    monkeypatch.setattr(ConversationRunner, "_run_workflow", fail_workflow)
    runner = ConversationRunner(
        graph=build_graph(),
        llm=_WorkflowToolLLM(),
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        hooks=hooks,
    )

    responses = list(runner.run("run workflow", iter(()), session_id="workflow-hook"))

    assert observed == [("RunWorkflow", True)]
    assert responses[-1].done.success is False


def test_server_shares_hook_and_command_registries_with_each_runner(tmp_path) -> None:
    app = OrchestratorServer(
        ServerConfig(project_root=str(tmp_path), memory_dir=str(tmp_path / "memory"))
    )
    try:
        service = OrchestratorService(app)
        first = service._new_runner()
        second = service._new_runner()
        assert first.hooks is app.hooks
        assert second.hooks is app.hooks
        assert first.commands is app.commands
        assert second.commands is app.commands
    finally:
        app.close()
