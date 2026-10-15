from __future__ import annotations

from concurrent import futures

import grpc
import pytest

from codeagent import orchestrator_pb2, orchestrator_pb2_grpc
from orchestrator.llm.client import ChatResponse
from orchestrator.server import OrchestratorServer, OrchestratorService, ServerConfig


class _NoToolLLM:
    model = "actor-grpc-test"

    async def chat(self, request):
        return ChatResponse(text="authorized")


def _actor(*, actor_id: str = "user:42", session_id: str = "actor-grpc-session"):
    return orchestrator_pb2.ActorContext(
        schema_version=1,
        actor_id=actor_id,
        subject="alice" if actor_id == "user:42" else "other",
        tenant_id="org:7",
        roles=["USER"],
        session_id=session_id,
    )


@pytest.fixture
def grpc_service(tmp_path, monkeypatch):
    monkeypatch.setenv("OPENAI_API_KEY", "")
    app = OrchestratorServer(
        ServerConfig(memory_dir=str(tmp_path / "memory"), project_root=str(tmp_path))
    )
    app.llm = _NoToolLLM()
    # One reused thread makes the following RPC exercise trace-context cleanup.
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=1))
    orchestrator_pb2_grpc.add_OrchestratorServicer_to_server(
        OrchestratorService(app), server
    )
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
            yield orchestrator_pb2_grpc.OrchestratorStub(channel), app
    finally:
        server.stop(grace=0)
        app.close()


def _request(session_id: str, actor=None):
    return iter(
        [
            orchestrator_pb2.HarnessMessage(
                user_input=orchestrator_pb2.UserInput(
                    text="hello",
                    session_id=session_id,
                    actor=actor,
                )
            )
        ]
    )


def test_grpc_rejects_missing_actor_for_durable_session(grpc_service) -> None:
    stub, _ = grpc_service

    with pytest.raises(grpc.RpcError) as error:
        list(stub.Converse(_request("actor-grpc-session")))

    assert error.value.code() == grpc.StatusCode.UNAUTHENTICATED


def test_grpc_rejects_actor_session_mismatch(grpc_service) -> None:
    stub, _ = grpc_service

    with pytest.raises(grpc.RpcError) as error:
        list(stub.Converse(_request("actor-grpc-session", _actor(session_id="other-session"))))

    assert error.value.code() == grpc.StatusCode.PERMISSION_DENIED


def test_grpc_rejects_cross_actor_session_reuse(grpc_service) -> None:
    stub, _ = grpc_service
    list(stub.Converse(_request("actor-grpc-session", _actor())))

    with pytest.raises(grpc.RpcError) as error:
        list(stub.Converse(_request("actor-grpc-session", _actor(actor_id="user:99"))))

    assert error.value.code() == grpc.StatusCode.PERMISSION_DENIED


def test_grpc_allows_repeat_request_for_same_actor(grpc_service) -> None:
    stub, _ = grpc_service
    actor = _actor()

    first = list(stub.Converse(_request("actor-grpc-session", actor)))
    second = list(stub.Converse(_request("actor-grpc-session", actor)))

    assert first[-1].done.success is True
    assert second[-1].done.success is True


def test_reflection_rpc_keeps_go_trace_parent_without_leaking_to_next_rpc(grpc_service) -> None:
    from opentelemetry import baggage, trace

    stub, app = grpc_service
    observed = []

    class ReflectionLLM:
        model = "trace-reflection-fixture"

        async def chat(self, request):
            current = trace.get_current_span().get_span_context()
            observed.append((current.trace_id, current.span_id, baggage.get_baggage("eval.instance_id")))
            assert request.allow_tools is False and request.tools == []
            return ChatResponse(text='{"candidates": []}')

    app.llm = ReflectionLLM()
    request = orchestrator_pb2.MemoryReflectionRequest(
        session_id="reflection-trace-session",
        actor=_actor(session_id="reflection-trace-session"),
        source_checksum="a" * 64,
        sources=[orchestrator_pb2.MemorySource(event_id="source-1", checksum="b" * 64, text="verified evidence")],
    )
    result = stub.ReflectMemory(request, metadata=(
        ("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"),
        ("baggage", "eval.instance_id=reflection-trace-session"),
    ))
    assert result.error_code == ""
    assert observed[0] == (int("0123456789abcdef0123456789abcdef", 16), int("0123456789abcdef", 16), "reflection-trace-session")

    assert stub.ReflectMemory(request).error_code == ""
    assert observed[1] == (0, 0, None)


@pytest.mark.parametrize("streaming", [False, True])
def test_conversation_rpc_parents_provider_under_python_chat(grpc_service, monkeypatch, streaming):
    from opentelemetry import trace
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import SimpleSpanProcessor
    from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter
    from orchestrator.llm.client import StreamDelta

    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    tracer = provider.get_tracer("grpc-conversation-parent")
    monkeypatch.setattr(trace, "get_tracer", lambda *args, **kwargs: tracer)

    class TrackedChat(_NoToolLLM):
        async def chat(self, request):
            with tracer.start_as_current_span("provider.capture"):
                return ChatResponse(text="authorized")

    class TrackedStream(TrackedChat):
        async def stream(self, request):
            with tracer.start_as_current_span("provider.capture"):
                text = "authorized"
            yield StreamDelta(kind="text", text=text)
            yield StreamDelta(kind="done")

    stub, app = grpc_service
    app.llm = TrackedStream() if streaming else TrackedChat()
    try:
        result = list(stub.Converse(_request("actor-grpc-session", _actor()), metadata=(
            ("traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"),
        )))
        assert result[-1].done.success
        spans = exporter.get_finished_spans()
        chats = [span for span in spans if span.name == "chat"]
        calls = [span for span in spans if span.name == "provider.capture"]
        assert chats and len(calls) == len(chats)
        assert {span.parent.span_id for span in calls} == {span.context.span_id for span in chats}
        assert all(span.parent.span_id == int("0123456789abcdef", 16) for span in chats)
    finally:
        provider.shutdown()
