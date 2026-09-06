from __future__ import annotations

import threading

import pytest

from codeagent import orchestrator_pb2
from orchestrator.context.compaction import CompactionRequest, LLMCompactionSummarizer
from orchestrator.context.compactor import Compactor
from orchestrator.graph.main_graph import build_graph
from orchestrator.llm.client import ChatMessage, ChatResponse, RequestInterrupted, StreamDelta, ToolCall
from orchestrator.llm.router import ModelInfo, ProviderRouter
from orchestrator.memory.manager import MemoryManager
from orchestrator.runtime.conversation import ConversationRunner
from orchestrator.runtime.tools import ToolRegistry
from orchestrator.skills.manager import SkillManager
from orchestrator.todo.manager import TodoManager


def test_compactor_preserves_goal_paths_errors_and_recent_messages() -> None:
    compactor = Compactor(max_chars=8_000, max_messages=2)
    messages = [
        {"role": "user", "content": "Implement Read ranges in internal/tools/read.go"},
        {"role": "assistant", "content": "Decision: keep deterministic compaction."},
        {"role": "tool", "content": "Error: failed reading tests/go/tools_test.go"},
        {"role": "assistant", "content": "Recent response mentions orchestrator/runtime/conversation.py"},
    ]

    summary = compactor.compact_history(messages, keep_recent=1)

    assert "[Compacted conversation history]" in summary
    assert "Implement Read ranges" in summary
    assert "internal/tools/read.go" in summary
    assert "tests/go/tools_test.go" in summary
    assert "failed reading" in summary
    assert "Recent response mentions orchestrator/runtime/conversation.py" in summary


def test_conversation_runner_compacts_before_chat(tmp_path) -> None:
    runner = ConversationRunner(
        graph=build_graph(),
        llm=None,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        compactor=Compactor(max_chars=200, max_messages=4),
    )
    messages = [ChatMessage(role="system", content="system prompt")]
    for idx in range(10):
        messages.append(
            ChatMessage(
                role="user" if idx == 0 else "assistant",
                content=f"message {idx} touching internal/tools/read.go with error {idx}",
            )
        )

    compacted = runner._compact_messages(messages)

    assert compacted[0].content == "system prompt"
    assert compacted[1].role == "system"
    assert "[Compacted conversation history]" in compacted[1].content
    assert "internal/tools/read.go" in compacted[1].content
    assert len(compacted) < len(messages)


def test_conversation_reuses_compacted_surface_on_follow_up_turn(tmp_path) -> None:
    class OneShotCompactor:
        def __init__(self):
            self.calls = 0
            self.compaction_retries = 0

        def should_compact(self, messages, **_kwargs):
            should = self.calls == 0
            self.calls += 1
            return should

        def select_compaction_range(self, messages, **_kwargs):
            return (1, 1)

        def estimate_tokens(self, messages):
            return sum(len(str(getattr(message, "content", message))) for message in messages)

        def compact_history(self, messages, **_kwargs):
            return "[Compacted conversation history]\nSURFACE_MARKER"

        def prune_tool_results(self, messages):
            return messages

    class ToolThenText:
        model = "fake"

        def __init__(self):
            self.requests = []

        async def chat(self, request):
            self.requests.append(request)
            if len(self.requests) == 1:
                return ChatResponse(
                    tool_calls=[ToolCall(id="read-1", name="Read", arguments={"path": "README.md"}, arguments_json='{"path":"README.md"}')]
                )
            return ChatResponse(text="done")

    llm = ToolThenText()
    runner = ConversationRunner(
        graph=build_graph(),
        llm=llm,
        tool_registry=ToolRegistry(str(tmp_path)),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        compactor=OneShotCompactor(),
    )
    runner.compactor.calls = 0
    responses = list(
        runner.run(
            "continue",
            iter([
                orchestrator_pb2.HarnessMessage(
                    tool_result=orchestrator_pb2.ToolResult(
                        tool_name="Read", tool_call_id="read-1", output="ok"
                    )
                )
            ]),
        )
    )

    assert responses[-1].done.success is True
    assert len(llm.requests) == 2
    assert "SURFACE_MARKER" in "\n".join(str(message.content) for message in llm.requests[1].messages)


def test_failed_mutating_tool_invalidates_read_cache() -> None:
    runner = ConversationRunner.__new__(ConversationRunner)
    cache = {
        "Read:{\"path\":\"README.md\"}": type("Cached", (), {"content": "old", "is_error": False})()
    }
    call = ToolCall(id="write-1", name="Write", arguments={"path": "README.md"})
    result = type("Result", (), {"error": "write failed", "exit_code": 1})()

    runner._invalidate_tool_cache_after(cache, call, result)

    assert cache == {}


def test_token_pressure_uses_context_window_and_provider_override() -> None:
    compactor = Compactor(
        context_window=100,
        pressure_ratio=0.8,
        retain_ratio=0.16,
        model_context_windows={"deepseek/reasoner": 200},
    )
    messages = [
        {"role": "user", "content": "x" * 160},
        {"role": "assistant", "content": "y" * 160},
        {"role": "user", "content": "z" * 160},
    ]

    assert compactor.estimate_tokens(messages) == 128
    assert compactor.should_compact(messages, model="deepseek/reasoner") is False
    assert compactor.should_compact(messages, model="other") is True


def test_exact_provider_model_policy_overrides_context_and_retention() -> None:
    compactor = Compactor(
        context_window=100,
        pressure_ratio=0.8,
        retain_ratio=0.16,
        model_context_windows={"reasoner": 200},
        model_policies={
            "deepseek/reasoner": {
                "context_window": 400,
                "pressure_ratio": 0.5,
                "retain_tokens": 40,
            }
        },
    )

    assert compactor.effective_context_window(
        model="reasoner", provider="deepseek"
    ) == 400
    assert compactor.pressure_threshold_tokens(
        model="reasoner", provider="deepseek"
    ) == 200
    assert compactor.effective_retain_tokens(
        model="reasoner", provider="deepseek"
    ) == 40


def test_model_policy_requires_mapping_values() -> None:
    with pytest.raises(TypeError, match="must be a mapping"):
        Compactor(model_policies={"deepseek/reasoner": "invalid"})


def test_runner_uses_active_route_context_window_for_pressure() -> None:
    class DummyClient:
        model = "reasoner"

        async def chat(self, request):  # pragma: no cover - route metadata only
            raise AssertionError("chat should not be called")

    client = DummyClient()
    router = ProviderRouter()
    router.register(
        "deepseek",
        client,
        models=[ModelInfo(provider="deepseek", id="reasoner", context_window=400)],
    )
    runner = ConversationRunner.__new__(ConversationRunner)
    runner.llm = client
    runner._active_route = router.prepare("deepseek", "reasoner")
    runner.provider_router = router
    runner.context_window = 100
    runner.compactor = Compactor(context_window=100, pressure_ratio=0.8)
    runner._pending_compaction_updates = []
    runner._context_persistence_error = ""

    messages = [
        ChatMessage(role="system", content="system"),
        ChatMessage(role="user", content="x" * 160),
        ChatMessage(role="assistant", content="y" * 160),
        ChatMessage(role="user", content="z" * 160),
    ]

    compacted = runner._compact_messages(messages)

    assert len(compacted) == len(messages)


def test_select_compaction_range_keeps_latest_sixteen_percent_and_tool_pair() -> None:
    compactor = Compactor(context_window=100, retain_ratio=0.16)
    call = ToolCall(id="call-1", name="Read", arguments={}, arguments_json="{}")
    messages = [
        {"role": "user", "content": "old-1" * 20},
        {"role": "assistant", "content": "", "tool_calls": [call]},
        {"role": "tool", "content": "old-result", "tool_call_id": "call-1"},
        {"role": "assistant", "content": "r"},
    ]

    selection = compactor.select_compaction_range(messages)

    assert selection is not None
    start, end = selection
    assert start == 0
    assert end == 0


def test_select_compaction_range_keeps_tool_call_anchor_when_tail_starts_at_result() -> None:
    compactor = Compactor(context_window=1_000)
    call = ToolCall(id="call-2", name="Read", arguments={}, arguments_json="{}")
    messages = [
        {"role": "user", "content": "old context " * 30},
        {"role": "assistant", "content": "", "tool_calls": [call]},
        {"role": "tool", "content": "tool output"},
        {"role": "assistant", "content": "latest instruction"},
    ]

    selection = compactor.select_compaction_range(messages, retain_tokens=20)

    assert selection is not None
    _, end = selection
    retained = messages[end + 1 :]
    assert [message["role"] for message in retained] == ["assistant", "tool", "assistant"]
    assert retained[0]["tool_calls"][0].id == "call-2"


def test_select_compaction_range_keeps_parallel_tool_calls_together() -> None:
    compactor = Compactor(context_window=1_000)
    calls = [
        ToolCall(id="call-1", name="Read", arguments={}, arguments_json="{}"),
        ToolCall(id="call-2", name="Grep", arguments={}, arguments_json="{}"),
    ]
    messages = [
        {"role": "user", "content": "old context " * 30},
        {"role": "assistant", "content": "", "tool_calls": calls},
        {"role": "tool", "content": "first result", "tool_call_id": "call-1"},
        {"role": "assistant", "content": "intermediate response"},
        {"role": "tool", "content": "second result", "tool_call_id": "call-2"},
        {"role": "assistant", "content": "latest instruction"},
    ]

    selection = compactor.select_compaction_range(messages, retain_tokens=20)

    assert selection is not None
    _, end = selection
    retained = messages[end + 1 :]
    assert retained[0]["role"] == "assistant"
    assert [call.id for call in retained[0]["tool_calls"]] == ["call-1", "call-2"]
    assert [message["tool_call_id"] for message in retained[1:4:2]] == ["call-1", "call-2"]


def test_forced_compaction_honors_explicit_retain_ratio() -> None:
    compactor = Compactor(context_window=1_000, retain_ratio=0.16)
    messages = [
        {"role": "user", "content": f"message-{index} " + "x" * 400}
        for index in range(8)
    ]

    selection = compactor.select_compaction_range(
        messages,
        history_start=0,
        force=True,
        retain_ratio=0.5,
    )

    assert selection is not None
    start, end = selection
    assert start == 0
    assert len(messages) - end - 1 >= 3


def test_compaction_events_are_persisted_but_hidden_from_layered_prompt(tmp_path) -> None:
    from orchestrator.context import LayeredContext, SQLiteContextStore

    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path)
    store.append("session", "compaction/start", {"range": [1, 2]})
    store.append("session", "compaction/summary", {"summary": "hidden"})
    store.append("session", "compaction/end", {"status": "completed"})

    snapshot = context.load("session")

    assert [event.kind for event in store.events("session")] == [
        "compaction/start",
        "compaction/summary",
        "compaction/end",
    ]
    assert "compaction" not in snapshot.events_text
    store.close()


def test_runner_pressure_compaction_records_lifecycle_and_hides_events(tmp_path) -> None:
    from orchestrator.context import LayeredContext, SQLiteContextStore

    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path)
    runner = ConversationRunner.__new__(ConversationRunner)
    runner.compactor = Compactor(
        context_window=80,
        pressure_ratio=0.8,
        retain_ratio=0.16,
        compaction_retries=2,
    )
    runner.llm = type("Model", (), {"model": "test-model"})()
    runner.context_window = None
    runner.layered_context = context
    runner._context_persistence_error = ""

    messages = [ChatMessage(role="system", content="system")]
    messages.extend(
        ChatMessage(role="user", content=f"history-{index} " + "x" * 80)
        for index in range(8)
    )

    compacted = runner._compact_messages(messages, session_id="session")
    events = store.events("session")

    assert len(compacted) < len(messages)
    assert len(events) % 3 == 0
    assert len(events) <= 9
    assert [event.kind for event in events[:3]] == [
        "compaction/start", "compaction/summary", "compaction/end"
    ]
    assert [event.payload.get("attempt") for event in events if event.kind == "compaction/start"] == list(
        range(1, len(events) // 3 + 1)
    )
    assert "compaction" not in context.load("session").events_text
    store.close()


def test_tool_result_pruner_keeps_head_and_tail_without_model_call() -> None:
    compactor = Compactor(tool_result_threshold=20, tool_result_head=8, tool_result_tail=4)
    original = ChatMessage(role="tool", content="HEAD-1234567890-MIDDLE-abcdefghijkl-TAIL-xyz")

    pruned = compactor.prune_tool_results([original])

    assert pruned[0] is not original
    assert pruned[0].content.startswith("HEAD-123")
    assert pruned[0].content.endswith("-xyz")
    assert "[tool result pruned" in pruned[0].content


def test_pressure_compaction_does_not_prune_below_threshold() -> None:
    """Tool-result pruning is a pressure pass, not an unconditional mutation."""
    runner = ConversationRunner.__new__(ConversationRunner)
    runner.compactor = Compactor(
        context_window=10_000,
        pressure_ratio=0.8,
        tool_result_threshold=20,
        tool_result_head=8,
        tool_result_tail=4,
    )
    runner.llm = type("Model", (), {"model": "reasoner"})()
    runner.context_window = None
    runner._active_route = None
    runner.layered_context = None
    runner._context_persistence_error = ""

    original = "HEAD-" + ("middle-" * 20) + "-TAIL"
    messages = [
        ChatMessage(role="system", content="system"),
        ChatMessage(role="user", content="continue"),
        ChatMessage(role="tool", content=original, tool_call_id="call-1"),
    ]

    compacted = runner._compact_messages(messages, session_id="below-pressure")

    assert compacted == messages
    assert compacted[-1].content == original
    assert "[tool result pruned" not in compacted[-1].content


def test_context_overflow_error_is_retryable_only_after_forced_progress() -> None:
    from orchestrator.llm.client import is_context_window_exceeded

    assert is_context_window_exceeded(RuntimeError("CONTEXT_WINDOW_EXCEEDED")) is True
    assert is_context_window_exceeded(RuntimeError("context_length_exceeded")) is True
    assert is_context_window_exceeded(RuntimeError("provider unavailable")) is False


def test_manual_compaction_returns_update_without_model_turn(tmp_path) -> None:
    runner = ConversationRunner(
        graph=build_graph(),
        llm=None,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        compactor=Compactor(max_chars=2_000, max_messages=4),
    )
    history = [
        {"role": "user", "content": f"history-{index} " + "x" * 120}
        for index in range(8)
    ]

    update = runner.compact_now(session_id="manual-session", history=history)

    assert update is not None
    assert "[Compacted conversation history]" in update.summary
    assert update.trigger == "manual"
    assert update.removed_messages > 0
    assert update.keep_recent_messages > 0


def test_llm_compaction_summarizer_replays_context_without_enabling_tools() -> None:
    class RecordingClient:
        async def stream(self, request):
            self.request = request
            yield StreamDelta(kind="text", text="short ")
            yield StreamDelta(kind="text", text="summary")
            yield StreamDelta(kind="done")

    client = RecordingClient()
    summarizer = LLMCompactionSummarizer(client=client, provider="deepseek", model="reasoner")

    result = summarizer.summarize(
        CompactionRequest(
            prefix_messages=(ChatMessage(role="system", content="system prompt"),),
            messages=(ChatMessage(role="user", content="old history"),),
            tools=({"name": "Read", "description": "read files"},),
            provider="deepseek",
            model="reasoner",
            target="conversation history",
        )
    )

    assert result == "short summary"
    request = client.request
    assert request.model == "reasoner"
    assert request.purpose == "compaction"
    assert request.allow_tools is False
    assert request.tools == []
    assert "Read" in "\n".join(str(message.content) for message in request.messages)
    assert request.messages[-1].role == "user"


def test_llm_compaction_summarizer_stops_before_provider_when_cancelled() -> None:
    class NoCallClient:
        async def stream(self, request):
            raise AssertionError("cancelled compaction must not call provider")
            yield  # pragma: no cover

    cancel = threading.Event()
    cancel.set()
    summarizer = LLMCompactionSummarizer(
        client=NoCallClient(), provider="deepseek", model="reasoner"
    )

    with pytest.raises(RequestInterrupted):
        summarizer.summarize(
            CompactionRequest(
                prefix_messages=(),
                messages=(ChatMessage(role="user", content="history"),),
                cancel_event=cancel,
            )
        )


def test_runner_prefers_llm_compaction_summary_and_audits_metadata(tmp_path) -> None:
    from orchestrator.context import LayeredContext, SQLiteContextStore

    class SummaryAdapter:
        def summarize(self, request):
            self.request = request
            return "short llm summary"

    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path)
    runner = ConversationRunner.__new__(ConversationRunner)
    runner.compactor = Compactor(context_window=80, pressure_ratio=0.8, retain_ratio=0.16)
    runner.compaction_summarizer = SummaryAdapter()
    runner.llm = type("Model", (), {"model": "reasoner"})()
    runner.context_window = None
    runner.layered_context = context
    runner._active_route = None
    runner._context_persistence_error = ""
    runner._pending_compaction_updates = []

    messages = [ChatMessage(role="system", content="system")]
    messages.extend(
        ChatMessage(role="user", content=f"history-{index} " + "x" * 80)
        for index in range(8)
    )

    compacted = runner._compact_messages(messages, session_id="session")

    assert compacted[1].content == "[Compacted conversation history]\nshort llm summary"
    summary_event = next(event for event in store.events("session") if event.kind == "compaction/summary")
    assert summary_event.payload["summary_mode"] == "llm"
    assert summary_event.payload["provider"] == ""
    assert summary_event.payload["model"] == "reasoner"
    assert summary_event.payload["summary_sha256"]
    assert "short llm summary" not in repr(summary_event.payload)
    store.close()


@pytest.mark.parametrize("returned", ["", "x" * 10_000])
def test_runner_falls_back_when_llm_compaction_summary_is_invalid(tmp_path, returned: str) -> None:
    from orchestrator.context import LayeredContext, SQLiteContextStore

    class SummaryAdapter:
        def summarize(self, request):
            return returned

    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path)
    runner = ConversationRunner.__new__(ConversationRunner)
    runner.compactor = Compactor(context_window=80, pressure_ratio=0.8, retain_ratio=0.16)
    runner.compaction_summarizer = SummaryAdapter()
    runner.llm = type("Model", (), {"model": "reasoner"})()
    runner.context_window = None
    runner.layered_context = context
    runner._active_route = None
    runner._context_persistence_error = ""
    runner._pending_compaction_updates = []

    messages = [ChatMessage(role="system", content="system")]
    messages.extend(
        ChatMessage(role="user", content=f"history-{index} " + "x" * 80)
        for index in range(8)
    )

    compacted = runner._compact_messages(messages, session_id="session")

    assert "[Compacted conversation history]" in compacted[1].content
    assert "short llm summary" not in compacted[1].content
    summary_event = next(event for event in store.events("session") if event.kind == "compaction/summary")
    assert summary_event.payload["summary_mode"] == "deterministic-fallback"
    store.close()
