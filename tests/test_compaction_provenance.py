from orchestrator.context.compactor import Compactor
from orchestrator.context.compaction import compact_surface
from orchestrator.graph.main_graph import build_graph
from orchestrator.llm.client import ChatMessage, ChatResponse, ToolCall
from orchestrator.memory.manager import MemoryManager
from orchestrator.runtime.conversation import ConversationRunner
from orchestrator.runtime.tools import ToolRegistry
from orchestrator.skills.manager import SkillManager
from orchestrator.todo.manager import TodoManager
from orchestrator.llm.providers.anthropic import AnthropicClient
from orchestrator.llm.providers.openai import OpenAIClient
import json
from copy import deepcopy
import pytest


@pytest.mark.parametrize("history_count", [0, 8])
def test_harness_compaction_keeps_active_input_after_large_tool_batch(tmp_path, history_count):
    from codeagent import orchestrator_pb2 as pb

    class ReadThenAnswer:
        model = "fixture"

        def __init__(self):
            self.requests = []

        async def chat(self, request):
            self.requests.append(request)
            if len(self.requests) == 1:
                return ChatResponse(tool_calls=[
                    ToolCall(id=f"read-{i}", name="Read", arguments={"path": f"file-{i}.go"})
                    for i in range(6)
                ])
            return ChatResponse(text="analysis complete")

    llm = ReadThenAnswer()
    runner = ConversationRunner(
        graph=build_graph(), llm=llm,
        tool_registry=ToolRegistry(str(tmp_path), allowed_tools=["Read"]),
        todo_manager=TodoManager(), memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(), project_root=str(tmp_path), working_dir=str(tmp_path),
        harness_managed=True,
        compactor=Compactor(max_chars=20000, max_messages=100, compaction_retries=0,
                            tool_result_threshold=100000),
    )
    responses = list(runner.run(
        "Analyze these files without modifying them",
        iter([pb.HarnessMessage(tool_result=pb.ToolResult(
            tool_name="Read", tool_call_id=f"read-{i}", output="source line\n" * 1000,
        )) for i in range(6)]),
        history=[{"role": "user" if i % 2 == 0 else "assistant", "content": "earlier fact " * 80,
                  "event_id": f"event-{i}", "event_checksum": str(i) * 64} for i in range(history_count)],
    ))

    assert responses[-1].done.success
    assert len(llm.requests) == 2
    updates = [response.compaction_update for response in responses
               if response.HasField("compaction_update")]
    assert bool(updates) == bool(history_count)
    followup = llm.requests[1].messages
    assert [message.content for message in followup if message.role == "user"][-1] == (
        "Analyze these files without modifying them"
    )
    assert [message.tool_call_id for message in followup if message.role == "tool"] == [
        f"read-{i}" for i in range(6)
    ]
    assert all(update.removed_messages == len(update.source_events) for update in updates)


def test_compaction_preserves_anthropic_system_prefix_and_canonical_count():
    from orchestrator.prompts.system import build_with_cache_breaks

    prefix = build_with_cache_breaks(
        {"identity": "static rules", "session": "dynamic request context"},
        provider="anthropic",
    )
    assert len(prefix) == 2
    history = [ChatMessage(
        role="user", content=f"fact-{i} " + "x" * 80,
        source_events=((f"event-{i}", str(i) * 64),),
    ) for i in range(8)]
    compactor = Compactor(max_chars=100000, max_messages=100)
    result = compact_surface([*prefix, *history], compactor=compactor, force=True)
    current = result.messages
    update = result.updates[0]
    assert current[:2] == prefix
    assert update["removed_messages"] == len(update["source_events"])
    assert current[2].source_events == tuple(
        (ref["event_id"], ref["checksum"]) for ref in update["source_events"]
    )
    # A sourced system summary is history, not part of the runtime prefix.
    result = compact_surface(current, compactor=compactor, force=True)
    assert result.messages[:2] == prefix
    assert result.messages == current
    assert result.updates == []


def test_compaction_preserves_exact_canonical_sources_across_recompaction():
    messages = [ChatMessage(
        role="user", content=f"fact-{i} " + "x" * 80,
        source_events=((f"event-{i}", str(i) * 64),),
    ) for i in range(8)]
    compactor = Compactor(max_chars=100000, max_messages=100)
    current = [ChatMessage(role="system", content="rules"), *messages]
    for _ in range(2):
        before = [ref for message in current for ref in getattr(message, "source_events", ())]
        result = compact_surface(current, compactor=compactor, force=True)
        current = result.messages
        assert len(result.updates) == 1
        update = result.updates[0]
        sources = [(ref["event_id"], ref["checksum"]) for ref in update["source_events"]]
        assert sources
        assert sources == list(current[1].source_events)
        assert [ref for message in current for ref in message.source_events] == before
        current.append(ChatMessage(role="user", content="new request"))


def test_legacy_history_does_not_invent_canonical_sources():
    messages = ConversationRunner._history_messages([{"role": "user", "content": "legacy"}])
    assert getattr(messages[0], "source_events", None) == ()


def test_canonical_source_identity_is_not_sent_to_provider():
    messages = [ChatMessage(role="user", content="fact", source_events=(("internal-event-marker", "f" * 64),))]
    for payload in (OpenAIClient._request_messages(messages), AnthropicClient._convert_messages(messages)):
        serialized = json.dumps(payload)
        assert "internal-event-marker" not in serialized
        assert "f" * 64 not in serialized


@pytest.fixture
def canonical_summary_history():
    amendment = 'UPDATED_CONSTRAINT_GENERIC_7: keep "literal" \\values\nincluding this second line.'
    previous = "[Compacted conversation history]\n" + "Archived observation. " * 40 + "\n" + amendment
    messages = [
        ChatMessage(role="system", content="runtime rules"),
        ChatMessage(role="system", content=previous, source_events=(("prior-summary", "a" * 64),)),
        ChatMessage(role="user", content="Earlier observation. " * 100, source_events=(("earlier-user", "b" * 64),)),
        ChatMessage(role="assistant", content="", tool_calls=[{
            "id": "old-read", "name": "Read", "arguments": {"path": "old.txt"},
        }], source_events=(("old-call", "c" * 64),)),
        ChatMessage(role="tool", content="Older result. " * 150, tool_call_id="old-read",
                    source_events=(("old-result", "d" * 64),)),
        ChatMessage(role="assistant", content="", tool_calls=[{
            "id": "recent-read", "name": "Read", "arguments": {"path": "recent.txt"},
        }], source_events=(("recent-call", "e" * 64),)),
        ChatMessage(role="tool", content="Recent result. " * 65, tool_call_id="recent-read",
                    source_events=(("recent-result", "f" * 64),)),
        ChatMessage(role="user", content="Apply the revised agreement and verify.",
                    source_events=(("active-input", "1" * 64),)),
    ]
    return previous, amendment, messages


@pytest.mark.parametrize("max_messages", [2, 100], ids=["summary-in-older", "summary-in-recent"])
@pytest.mark.parametrize("returned", [None, "", "expanded " * 10_000, RuntimeError("summary unavailable")],
                         ids=["invalid", "empty", "too-long", "exception"])
def test_fallback_preserves_complete_canonical_summary(canonical_summary_history, max_messages, returned):
    previous, amendment, messages = canonical_summary_history

    class SummaryAdapter:
        def summarize(self, request):
            if isinstance(returned, Exception):
                raise returned
            return returned

    result = compact_surface(
        messages, compactor=Compactor(max_chars=16_000, max_messages=max_messages),
        force=True, retain_ratio=0.1, harness_managed=True, summarizer=SummaryAdapter(),
    )

    assert len(result.updates) == 1
    assert previous in result.messages[1].content
    assert amendment in result.messages[1].content
    assert result.messages[0] is messages[0]
    assert result.messages[-3:] == messages[-3:]
    assert [ref for message in result.messages for ref in message.source_events] == [
        ref for message in messages for ref in message.source_events
    ]
    assert [(ref["event_id"], ref["checksum"]) for ref in result.updates[0]["source_events"]] == [
        ref for message in messages[1:5] for ref in message.source_events
    ]
    assert "prior-summary" not in result.messages[1].content
    assert "a" * 64 not in result.messages[1].content


@pytest.mark.parametrize("extra_budget", [-1, 1], ids=["prior-summary-too-large", "combined-summary-too-large"])
def test_fallback_budget_refusal_does_not_publish_compaction(canonical_summary_history, extra_budget):
    previous, _, messages = canonical_summary_history
    before = deepcopy(messages)
    events = []
    calls = []

    class EmptySummary:
        def summarize(self, request):
            calls.append(request)
            return ""

    with pytest.raises(ValueError, match="exceed compaction budget"):
        compact_surface(
            messages, compactor=Compactor(max_chars=len(previous) + extra_budget,
                                         tool_result_threshold=100_000),
            force=True, retain_ratio=0.1, harness_managed=True, summarizer=EmptySummary(),
            record_event=lambda kind, payload: events.append((kind, payload)) or True,
        )

    assert len(calls) == 1
    assert messages == before
    assert [kind for kind, _ in events] == ["compaction/start", "compaction/end"]
    assert events[-1][1] == {"status": "failed"}


def test_valid_llm_summary_does_not_need_fallback_budget(canonical_summary_history):
    _, amendment, messages = canonical_summary_history

    class ValidSummary:
        def summarize(self, request):
            return amendment

    result = compact_surface(
        messages, compactor=Compactor(max_chars=128), force=True, retain_ratio=0.1,
        harness_managed=True, summarizer=ValidSummary(),
    )

    assert len(result.updates) == 1
    assert result.messages[1].content == "[Compacted conversation history]\n" + amendment
    assert result.messages[-3:] == messages[-3:]
    assert [ref for message in result.messages for ref in message.source_events] == [
        ref for message in messages for ref in message.source_events
    ]


def test_canonical_summary_alone_fits_exact_budget_without_repeated_wrapping(canonical_summary_history):
    previous, _, original = canonical_summary_history
    messages = [original[0], original[1], original[-1]]
    compactor = Compactor(max_chars=len(previous))

    for _ in range(3):
        result = compact_surface(messages, compactor=compactor, force=True, harness_managed=True)
        assert result.updates == []
        assert result.messages == messages
        assert result.messages[1].content == previous
        assert result.messages[-1] is original[-1]
        assert result.messages[1].source_events == original[1].source_events
        messages = result.messages


def test_fallback_recompaction_preserves_prior_text_without_json_nesting(canonical_summary_history):
    previous, amendment, messages = canonical_summary_history
    compactor = Compactor(max_chars=16_000)

    for turn in range(3):
        sources = [ref for message in messages for ref in message.source_events]
        active = messages[-1]
        result = compact_surface(
            messages, compactor=compactor, force=True, retain_ratio=0.1, harness_managed=True,
        )
        assert len(result.updates) == (1 if turn == 0 else 0)
        if not result.updates:
            assert result.messages == messages
        content = result.messages[1].content
        assert previous in content
        assert content.count(amendment) == 1
        assert len(content) <= compactor.max_chars
        assert result.messages[-1] is active
        assert [ref for message in result.messages for ref in message.source_events] == sources
        previous = content
        messages = [*result.messages, ChatMessage(
            role="user", content=f"New request {turn}; keep all prior amendments.",
            source_events=((f"next-input-{turn}", str(turn + 2) * 64),),
        )]


def test_python_grpc_compaction_returns_canonical_sources(tmp_path):
    from concurrent import futures
    import grpc
    from codeagent import orchestrator_pb2 as pb, orchestrator_pb2_grpc as rpc
    from orchestrator.server import OrchestratorServer, OrchestratorService, ServerConfig

    app = OrchestratorServer(ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path)))
    app.llm, app.fast_llm, app.provider_clients = None, None, {}
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=2))
    rpc.add_OrchestratorServicer_to_server(OrchestratorService(app), server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
            result = rpc.OrchestratorStub(channel).Compact(pb.CompactRequest(
                session_id="provenance", actor=pb.ActorContext(schema_version=1, actor_id="user:7",
                    subject="test", tenant_id="test", roles=["USER"], session_id="provenance"),
                history=[pb.ConversationMessage(role="user", content=f"fact-{i} " + "x" * 80,
                    event_id=f"event-{i}", event_checksum=str(i) * 64) for i in range(8)],
            ), timeout=10)
        sources = [(ref.event_id, ref.checksum) for ref in result.source_events]
        assert sources == [(f"event-{i}", str(i) * 64) for i in range(len(sources))]
        assert 1 <= len(sources) < 8
    finally:
        server.stop(grace=0).wait()
        app.close()


def test_runner_retains_surface_when_automatic_compaction_refuses_budget(tmp_path):
    prior_summary = ("[Compacted conversation history]\n" + ("Prior canonical constraint. " * 20)).strip()
    history = [
        {
            "role": "system",
            "content": prior_summary,
            "event_id": "summary-1",
            "event_checksum": "a" * 64,
        },
        {
            "role": "user",
            "content": "Earlier user turn " * 20,
            "event_id": "user-1",
            "event_checksum": "b" * 64,
        },
        {
            "role": "assistant",
            "content": "Earlier assistant reply " * 20,
            "event_id": "assistant-1",
            "event_checksum": "c" * 64,
        },
    ]

    class HealthyLLM:
        model = "fixture"

        def __init__(self):
            self.requests = []

        async def chat(self, request):
            self.requests.append(request)
            return ChatResponse(text="completed with intact surface")

    def make_runner(llm, *, max_messages: int, max_overflow_retries: int = 3, memory_subdir: str = "memory"):
        return ConversationRunner(
            graph=build_graph(),
            llm=llm,
            tool_registry=ToolRegistry(str(tmp_path), allowed_tools=[]),
            todo_manager=TodoManager(),
            memory_manager=MemoryManager(str(tmp_path / memory_subdir)),
            skills=SkillManager(),
            project_root=str(tmp_path),
            working_dir=str(tmp_path),
            harness_managed=True,
            max_overflow_retries=max_overflow_retries,
            compactor=Compactor(max_chars=len(prior_summary) - 1, max_messages=max_messages),
        )

    llm = HealthyLLM()
    runner = make_runner(llm, max_messages=2)
    responses = list(runner.run("Continue under the existing constraints", iter([]), history=history))

    assert responses[-1].done.success is True
    assert len(llm.requests) == 1
    assert any(msg.content == prior_summary for msg in llm.requests[0].messages)

    class OverflowLLM:
        model = "fixture"

        async def chat(self, request):
            raise RuntimeError("maximum context length exceeded")

    overflow_runner = make_runner(
        OverflowLLM(),
        max_messages=100,
        max_overflow_retries=1,
        memory_subdir="memory-overflow",
    )
    overflow_responses = list(
        overflow_runner.run("Trigger overflow recovery", iter([]), history=history)
    )

    assert overflow_responses[-1].HasField("done")
    assert overflow_responses[-1].done.success is False
    assert overflow_responses[-1].done.error_code == "context_window_exceeded"


def test_llm_compaction_summarizer_succeeds_inside_running_event_loop():
    import asyncio
    from orchestrator.context.compaction import CompactionRequest, LLMCompactionSummarizer

    class AsyncSummaryClient:
        model = "summary-fixture"

        async def chat(self, request):
            await asyncio.sleep(0)
            return ChatResponse(text="Preserved summary from running loop")

    summarizer = LLMCompactionSummarizer(client=AsyncSummaryClient())
    request = CompactionRequest(
        prefix_messages=(),
        messages=(ChatMessage(role="user", content="Keep network disabled."),),
        model="summary-fixture",
    )

    async def invoke_from_running_loop():
        return summarizer.summarize(request)

    assert asyncio.run(invoke_from_running_loop()) == "Preserved summary from running loop"


def test_llm_compaction_retains_standing_constraints_across_replacements():
    class OmittingSummarizer:
        def __init__(self):
            self.calls = 0

        def summarize(self, request):
            self.calls += 1
            return f"Concise summary pass {self.calls}."

    constraint_line = "Standing constraint: 禁止网络访问，不增依赖 (do not add dependencies or use network)."
    revision_block = 'Revision: keep "literal" \\values\nincluding this second line.'
    messages = [
        ChatMessage(role="system", content="runtime rules"),
        ChatMessage(
            role="user",
            content="Inspect the repository structure.\n" + constraint_line,
            source_events=(("event-u1", "1" * 64),),
        ),
        ChatMessage(
            role="assistant",
            content="Detailed inspection findings. " * 80,
            source_events=(("event-a1", "2" * 64),),
        ),
        ChatMessage(
            role="user",
            content="Apply the updated rule.\n" + revision_block,
            source_events=(("event-u2", "3" * 64),),
        ),
        ChatMessage(
            role="assistant",
            content="Updated findings. " * 80,
            source_events=(("event-a2", "4" * 64),),
        ),
        ChatMessage(
            role="user",
            content="Continue with the next package.",
            source_events=(("event-u3", "5" * 64),),
        ),
    ]
    summarizer = OmittingSummarizer()
    compactor = Compactor(max_chars=16_000, max_messages=100)

    first = compact_surface(
        messages,
        compactor=compactor,
        force=True,
        retain_ratio=0.05,
        harness_managed=True,
        summarizer=summarizer,
    )
    assert len(first.updates) == 1
    assert constraint_line in first.messages[1].content
    assert revision_block in first.messages[1].content

    deterministic = compact_surface(
        [
            *first.messages,
            ChatMessage(
                role="assistant",
                content="Intermediate deterministic fallback turn. " * 80,
                source_events=(("event-a3", "6" * 64),),
            ),
            ChatMessage(
                role="user",
                content="Proceed after fallback.",
                source_events=(("event-u4", "7" * 64),),
            ),
        ],
        compactor=compactor,
        force=True,
        retain_ratio=0.05,
        harness_managed=True,
        summarizer=None,
    )

    second_input = [
        *deterministic.messages,
        ChatMessage(
            role="assistant",
            content="Second package inspection findings. " * 80,
            source_events=(("event-a4", "8" * 64),),
        ),
        ChatMessage(
            role="user",
            content="Prepare final handoff.",
            source_events=(("event-u5", "9" * 64),),
        ),
    ]
    second = compact_surface(
        second_input,
        compactor=compactor,
        force=True,
        retain_ratio=0.05,
        harness_managed=True,
        summarizer=summarizer,
    )
    assert len(second.updates) == 1
    assert constraint_line in second.messages[1].content
    assert revision_block in second.messages[1].content
    assert '{"role":' not in second.messages[1].content
    assert [ref for msg in second.messages for ref in msg.source_events] == [
        ref for msg in second_input for ref in msg.source_events
    ]
