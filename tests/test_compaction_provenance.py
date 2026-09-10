from orchestrator.context.compactor import Compactor
from orchestrator.graph.main_graph import build_graph
from orchestrator.llm.client import ChatMessage
from orchestrator.memory.manager import MemoryManager
from orchestrator.runtime.conversation import ConversationRunner
from orchestrator.runtime.tools import ToolRegistry
from orchestrator.skills.manager import SkillManager
from orchestrator.todo.manager import TodoManager
from orchestrator.llm.providers.anthropic import AnthropicClient
from orchestrator.llm.providers.openai import OpenAIClient
import json


def test_compaction_preserves_anthropic_system_prefix_and_canonical_count(tmp_path):
    from orchestrator.prompts.system import build_with_cache_breaks

    prefix = build_with_cache_breaks(
        {"identity": "static rules", "session": "dynamic request context"},
        provider="anthropic",
    )
    assert len(prefix) == 2
    history = ConversationRunner._history_messages([
        {"role": "user", "content": f"fact-{i} " + "x" * 80,
         "event_id": f"event-{i}", "event_checksum": str(i) * 64}
        for i in range(8)
    ], bounded=False)
    runner = ConversationRunner(
        graph=build_graph(), llm=None, tool_registry=ToolRegistry(),
        todo_manager=TodoManager(), memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(), project_root=str(tmp_path), working_dir=str(tmp_path),
        compactor=Compactor(max_chars=100000, max_messages=100),
    )
    current = runner._compact_messages([*prefix, *history], force=True)
    update = list(runner._emit_compaction_updates())[0].compaction_update
    assert current[:2] == prefix
    assert update.removed_messages == len(update.source_events)
    assert current[2].source_events == tuple(
        (ref.event_id, ref.checksum) for ref in update.source_events
    )
    # A sourced system summary is history, not part of the runtime prefix.
    current = runner._compact_messages(current, force=True)
    assert current[:2] == prefix
    assert len(list(runner._emit_compaction_updates())) == 1


def test_compaction_preserves_exact_canonical_sources_across_recompaction(tmp_path):
    history = [{"role": "user", "content": f"fact-{i} " + "x" * 80,
                "event_id": f"event-{i}", "event_checksum": str(i) * 64}
               for i in range(8)]
    messages = ConversationRunner._history_messages(history, bounded=False)
    assert getattr(messages[0], "source_events", ()) == (("event-0", "0" * 64),)
    runner = ConversationRunner(
        graph=build_graph(), llm=None, tool_registry=ToolRegistry(),
        todo_manager=TodoManager(), memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(), project_root=str(tmp_path), working_dir=str(tmp_path),
        compactor=Compactor(max_chars=100000, max_messages=100),
    )
    current = [ChatMessage(role="system", content="rules"), *messages]
    for _ in range(2):
        before = [ref for message in current for ref in getattr(message, "source_events", ())]
        current = runner._compact_messages(current, force=True)
        updates = list(runner._emit_compaction_updates())
        assert len(updates) == 1
        update = updates[0].compaction_update
        sources = [(ref.event_id, ref.checksum) for ref in update.source_events]
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
