from __future__ import annotations

import asyncio
import json
import threading
from concurrent.futures import ThreadPoolExecutor

import grpc
import pytest

from codeagent import orchestrator_pb2 as pb
from codeagent import orchestrator_pb2_grpc as rpc
from orchestrator.llm.client import ChatMessage, ChatRequest, RequestInterrupted
from orchestrator.llm.gateway import HarnessModelClient
from orchestrator.server import OrchestratorServer, OrchestratorService, ServerConfig
from orchestrator.workflows.models import WorkerSpec
from orchestrator.workflows.providers import ProviderWorkerExecutor


@pytest.mark.parametrize("rpc_timeout,minimum,maximum", [(0, 60, 66), (210, 205, 212), (570, 565, 572), (1830, 1825, 1840)])
def test_harness_model_client_uses_go_issued_rpc_deadline(rpc_timeout, minimum, maximum):
    deadlines = []

    class Service(rpc.ModelGatewayServicer):
        def Invoke(self, request, context):
            deadlines.append(context.time_remaining())
            return pb.ModelCallResponse(payload_json=json.dumps({
                "model": "fixture-model", "choices": [{"message": {"content": "ok"}}],
            }).encode(), cost_status="unknown")

    server = grpc.server(ThreadPoolExecutor(max_workers=1))
    rpc.add_ModelGatewayServicer_to_server(Service(), server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    client = HarnessModelClient(pb.ModelGatewayBinding(
        schema_version=1, address=f"127.0.0.1:{port}", capability=b"a" * 32,
        protocol="openai", model="fixture-model", rpc_timeout_seconds=rpc_timeout,
    ))
    try:
        response = asyncio.run(client.chat(ChatRequest(
            model="fixture-model", messages=[ChatMessage("user", "hello")],
        )))
        assert response.text == "ok"
        # gRPC rounds longer wire deadlines up to seconds or tens of seconds.
        assert len(deadlines) == 1 and minimum <= deadlines[0] <= maximum
    finally:
        client.close()
        server.stop(0).wait()


def test_harness_model_client_rejects_unbounded_rpc_deadline():
    from orchestrator.llm.gateway import ModelGatewayError

    with pytest.raises(ModelGatewayError, match="model_gateway_binding_invalid"):
        HarnessModelClient(pb.ModelGatewayBinding(
            schema_version=1, address="127.0.0.1:1", capability=b"a" * 32,
            protocol="openai", model="fixture-model", rpc_timeout_seconds=1831,
        ))


def test_harness_model_client_can_cancel_long_admitted_rpc():
    started, stopped, cancel = threading.Event(), threading.Event(), threading.Event()

    class Service(rpc.ModelGatewayServicer):
        def Invoke(self, request, context):
            context.add_callback(stopped.set)
            started.set()
            stopped.wait(3)
            return pb.ModelCallResponse(error_code="provider_transport_unknown")

    server = grpc.server(ThreadPoolExecutor(max_workers=1))
    rpc.add_ModelGatewayServicer_to_server(Service(), server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    client = HarnessModelClient(pb.ModelGatewayBinding(
        schema_version=1, address=f"127.0.0.1:{port}", capability=b"a" * 32,
        protocol="openai", model="fixture-model", rpc_timeout_seconds=570,
    ))

    def cancel_running_request():
        started.wait(2)
        cancel.set()

    interrupter = threading.Thread(target=cancel_running_request)
    interrupter.start()
    try:
        with pytest.raises(RequestInterrupted):
            asyncio.run(client.chat(ChatRequest(
                model="fixture-model", messages=[ChatMessage("user", "hello")], cancel_event=cancel,
            )))
        assert started.is_set() and stopped.wait(2)
    finally:
        client.close()
        server.stop(0).wait()
        interrupter.join(2)


def test_harness_model_client_uses_go_transport_and_normalized_usage(monkeypatch):
    from opentelemetry import trace
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import SimpleSpanProcessor
    from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    tracer = provider.get_tracer("model-gateway-boundary")
    monkeypatch.setattr(trace, "get_tracer", lambda *args, **kwargs: tracer)
    captured = []
    trace_metadata = []

    class Service(rpc.ModelGatewayServicer):
        def Invoke(self, request, context):
            captured.append(request)
            trace_metadata.append(dict(context.invocation_metadata()))
            return pb.ModelCallResponse(
                payload_json=json.dumps({"model": "fixture-model", "choices": [{"message": {"content": "ok"}}],
                                         "usage": {"prompt_tokens": 8, "completion_tokens": 3}}).encode(),
                input_tokens=17, output_tokens=3, cached_input_tokens=4, cost_status="unknown",
            )

    server = grpc.server(ThreadPoolExecutor(max_workers=1))
    rpc.add_ModelGatewayServicer_to_server(Service(), server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    client = HarnessModelClient(pb.ModelGatewayBinding(
        schema_version=1, address=f"127.0.0.1:{port}", capability=b"a" * 32,
        protocol="openai", model="fixture-model",
    ))
    try:
        with tracer.start_as_current_span("go.parent") as parent:
            response = asyncio.run(client.chat(ChatRequest(model="fixture-model", messages=[ChatMessage("user", "hello")], purpose="compaction")))
        assert response.text == "ok"
        assert (response.usage.input_tokens, response.usage.output_tokens, response.usage.cached_input_tokens) == (17, 3, 4)
        assert response.model_identity["cost_status"] == "unknown"
        assert len(captured) == 1 and captured[0].purpose == "compaction"
        assert captured[0].call_id and captured[0].capability == b"a" * 32
        payload = json.loads(captured[0].payload_json)
        assert payload["model"] == "fixture-model"
        assert payload["messages"][0]["content"] == "hello"
        assert "api_key" not in payload and "authorization" not in payload
        calls = [span for span in exporter.get_finished_spans() if span.name == "provider.call"]
        assert len(calls) == 1
        span = calls[0]
        assert span.parent.span_id == parent.get_span_context().span_id
        assert span.context.trace_id == parent.get_span_context().trace_id
        assert span.attributes["model.call_id"] == captured[0].call_id
        assert span.attributes["model.purpose"] == "compaction"
        assert span.attributes["gen_ai.usage.input_tokens"] == 17
        assert trace_metadata[0]["traceparent"] == f"00-{span.context.trace_id:032x}-{span.context.span_id:016x}-{span.context.trace_flags:02x}"
        assert "hello" not in str(span.attributes) and not span.events
        executor = ProviderWorkerExecutor({"default": client})
        result = asyncio.run(executor(WorkerSpec("worker-1", "Probe", "Reply briefly"), {}))
        assert result.output == "ok"
        assert captured[-1].purpose == "workflow_worker"
    finally:
        client.close()
        server.stop(0).wait()
        provider.shutdown()


@pytest.mark.parametrize("gateway_error", [
    "", "token_usage_unknown", "task_prerequisites_missing", "missing_binding",
    "provider_rate_limited", "provider_transient", "provider_authentication",
    "provider_http", "provider_retry_canceled",
    "provider_timeout_unknown",
])
@pytest.mark.parametrize("admission_mode", ["required", ""])
def test_orchestrator_conversation_uses_harness_gateway(tmp_path, monkeypatch, gateway_error, admission_mode):
    # Explicit blanks also prevent repository dotenv from activating a relay.
    monkeypatch.setenv("OPENAI_API_KEY", "")
    monkeypatch.setenv("ANTHROPIC_API_KEY", "")
    monkeypatch.setenv("ANTHROPIC_AUTH_TOKEN", "")
    monkeypatch.setenv("MODEL_FAST", "")
    monkeypatch.setenv("LLM_PROVIDER", "openai")
    monkeypatch.setenv("OTEL_SDK_DISABLED", "true")
    monkeypatch.setenv("CODE_AGENT_MODEL_ADMISSION", admission_mode)
    # The controlled product must not read a credential file or construct a
    # direct provider client, even when a legacy configuration is inherited.
    monkeypatch.setenv("CODE_AGENT_PROVIDER_CONFIG", str(tmp_path / "unreadable-provider.json") if admission_mode else "")
    app = OrchestratorServer(ServerConfig(project_root=str(tmp_path), working_dir=str(tmp_path), memory_dir=str(tmp_path / "memory")))
    calls = []
    class Service(rpc.ModelGatewayServicer):
        def Invoke(self, request, context):
            calls.append(request)
            if gateway_error:
                return pb.ModelCallResponse(error_code=gateway_error, cost_status="unknown")
            return pb.ModelCallResponse(payload_json=json.dumps({"model":"fixture-model", "choices":[{"message":{"content":"ok"}}]}).encode(),
                                        input_tokens=17, output_tokens=3, cost_status="unknown")
    model_server = grpc.server(ThreadPoolExecutor(max_workers=1))
    rpc.add_ModelGatewayServicer_to_server(Service(), model_server)
    model_port = model_server.add_insecure_port("127.0.0.1:0")
    model_server.start()
    binding = pb.ModelGatewayBinding(schema_version=1, address=f"127.0.0.1:{model_port}", capability=b"a" * 32,
                                    protocol="openai", model="fixture-model")
    server = grpc.server(ThreadPoolExecutor(max_workers=1))
    rpc.add_OrchestratorServicer_to_server(OrchestratorService(app), server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
            request = pb.UserInput(text="hello",session_id="session-1",harness_managed=True,model_gateway=binding,
                                  actor=pb.ActorContext(schema_version=1,actor_id="fixture",subject="fixture",tenant_id="fixture",roles=["USER"],session_id="session-1"))
            if gateway_error == "missing_binding":
                request.ClearField("model_gateway")
            responses = list(rpc.OrchestratorStub(channel).Converse(iter([pb.HarnessMessage(user_input=request)]),timeout=10))
        if gateway_error:
            assert not responses[-1].done.success
            assert responses[-1].done.error_code == ("model_prerequisites_missing" if gateway_error == "missing_binding" else gateway_error)
            assert not responses[-1].done.retryable
            assert len(calls) == (0 if gateway_error == "missing_binding" else 1)
            return
        assert responses[-1].done.success, responses[-1].done.error_code
        meta = next(item.session_meta for item in responses if item.HasField("session_meta"))
        assert meta.cost_status == "unknown"
        assert len(calls) == 1 and calls[0].capability == b"a" * 32
        assert json.loads(calls[0].payload_json)["model"] == "fixture-model"
        if admission_mode:
            assert app.llm is None and app.fast_llm is None and not app.provider_clients
    finally:
        server.stop(0).wait()
        model_server.stop(0).wait()
        app.close()


@pytest.mark.parametrize("gateway_error", ["", "provider_rate_limited_unknown"])
def test_memory_reflection_uses_harness_gateway(tmp_path, monkeypatch, gateway_error):
    monkeypatch.setenv("OPENAI_API_KEY", "")
    monkeypatch.setenv("ANTHROPIC_API_KEY", "")
    monkeypatch.setenv("ANTHROPIC_AUTH_TOKEN", "")
    monkeypatch.setenv("LLM_PROVIDER", "openai")
    monkeypatch.setenv("MODEL_FAST", "")
    monkeypatch.setenv("OTEL_SDK_DISABLED", "true")
    app = OrchestratorServer(ServerConfig(project_root=str(tmp_path),working_dir=str(tmp_path),memory_dir=str(tmp_path / "memory")))
    calls = []
    class Service(rpc.ModelGatewayServicer):
        def Invoke(self, request, context):
            calls.append(request)
            if gateway_error:
                return pb.ModelCallResponse(error_code=gateway_error, cost_status="unknown")
            return pb.ModelCallResponse(payload_json=json.dumps({"model":"fixture-model","choices":[{"message":{"content":"{\"candidates\":[]}"}}]}).encode(),
                                        input_tokens=17,output_tokens=3,cost_status="unknown")
    model_server = grpc.server(ThreadPoolExecutor(max_workers=1))
    rpc.add_ModelGatewayServicer_to_server(Service(), model_server)
    model_port = model_server.add_insecure_port("127.0.0.1:0")
    model_server.start()
    server = grpc.server(ThreadPoolExecutor(max_workers=1))
    rpc.add_OrchestratorServicer_to_server(OrchestratorService(app),server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
            binding=pb.ModelGatewayBinding(schema_version=1,address=f"127.0.0.1:{model_port}",capability=b"a"*32,protocol="openai",model="fixture-model")
            actor=pb.ActorContext(schema_version=1,actor_id="fixture",subject="fixture",tenant_id="fixture",roles=["USER"],session_id="session-1")
            result=rpc.OrchestratorStub(channel).ReflectMemory(pb.MemoryReflectionRequest(session_id="session-1",actor=actor,source_checksum="b"*64,
                sources=[pb.MemorySource(event_id="source-1",checksum="c"*64,text="fixture trajectory")],model_gateway=binding),timeout=10)
        assert result.error_code == gateway_error
        assert len(calls)==1 and calls[0].purpose=="memory_reflection"
    finally:
        server.stop(0).wait()
        model_server.stop(0).wait()
        app.close()


@pytest.mark.parametrize("head_seq,head_checksum,surface,predecessor,checkpoint_seq,resumed_run_id,success", [
    (8, "d" * 64, "surface:unchanged", "run:original", 6, "run:retry", True),
    (8, "d" * 64, "surface:unchanged", "", 6, "run:original", True),
    (5, "d" * 64, "surface:unchanged", "run:original", 6, "run:retry", False),
    (6, "d" * 64, "surface:unchanged", "run:original", 6, "run:retry", False),
    (8, "d" * 64, "surface:foreign", "run:original", 6, "run:retry", False),
    (8, "d" * 64, "surface:unchanged", "run:foreign", 6, "run:retry", False),
    (8, "invalid", "surface:unchanged", "run:original", 6, "run:retry", False),
    (8, "d" * 64, "surface:unchanged", "run:original", "missing", "run:retry", False),
    (8, "d" * 64, "surface:unchanged", "run:original", -1, "run:retry", False),
    (8, "d" * 64, "surface:unchanged", "run:original", None, "run:retry", False),
    (8, "d" * 64, "surface:unchanged", "run:original", True, "run:retry", False),
])
def test_conversation_retries_failed_run_with_fresh_harness_binding(
    tmp_path, monkeypatch, head_seq, head_checksum, surface, predecessor, checkpoint_seq, resumed_run_id, success
):
    for name in ("OPENAI_API_KEY", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "MODEL_FAST", "CODE_AGENT_PROVIDER_CONFIG"):
        monkeypatch.setenv(name, "")
    monkeypatch.setenv("CODE_AGENT_MODEL_ADMISSION", "required")
    monkeypatch.setenv("CODE_AGENT_REQUIRE_HARNESS_WORKTREE", "1")
    monkeypatch.setenv("OTEL_SDK_DISABLED", "true")
    working_dir = tmp_path / "lease-fixture"
    working_dir.mkdir()
    app = OrchestratorServer(ServerConfig(project_root=str(tmp_path), working_dir=str(tmp_path), memory_dir=str(tmp_path / "memory")))
    calls = []

    class Service(rpc.ModelGatewayServicer):
        def Invoke(self, request, context):
            calls.append(request)
            if len(calls) == 1:
                return pb.ModelCallResponse(error_code="provider_bound_violated", cost_status="unknown")
            return pb.ModelCallResponse(payload_json=json.dumps({"model": "fixture-model", "content": [{"type": "text", "text": "recovered"}]}).encode(),
                                        input_tokens=17, output_tokens=3, cost_status="unknown")

    model_server = grpc.server(ThreadPoolExecutor(max_workers=1))
    rpc.add_ModelGatewayServicer_to_server(Service(), model_server)
    model_port = model_server.add_insecure_port("127.0.0.1:0")
    model_server.start()
    server = grpc.server(ThreadPoolExecutor(max_workers=1))
    rpc.add_OrchestratorServicer_to_server(OrchestratorService(app), server)
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
            stub = rpc.OrchestratorStub(channel)
            request = pb.UserInput(text="read the repository", session_id="session-retry", harness_managed=True, working_dir=str(working_dir),
                actor=pb.ActorContext(schema_version=1, actor_id="fixture", subject="fixture", tenant_id="fixture", roles=["USER"], session_id="session-retry"),
                model_gateway=pb.ModelGatewayBinding(schema_version=1, address=f"127.0.0.1:{model_port}", capability=b"a" * 32, protocol="anthropic", model="fixture-model"))
            request.context_envelope.CopyFrom(pb.ContextEnvelope(schema_version=1, ledger_seq=6, ledger_checksum="c" * 64,
                events=[pb.ContextEventSummary(seq=6, type="session/run-leased", checksum="c" * 64)]))
            first = list(stub.Converse(iter([pb.HarnessMessage(user_input=request)]), timeout=10,
                metadata=(("x-code-agent-run-id", "run:original"), ("x-code-agent-resume", "true"),
                          ("x-code-agent-surface-sha256", "surface:unchanged"))))
            assert first[-1].done.error_code == "provider_bound_violated"
            if checkpoint_seq != 6:
                checkpoint = app.graph.get_checkpoint("session-retry")
                assert checkpoint is not None
                if checkpoint_seq == "missing":
                    checkpoint.metadata.pop("ledger_seq")
                else:
                    checkpoint.metadata["ledger_seq"] = checkpoint_seq
                app.graph.write_checkpoint(checkpoint, thread_id="session-retry")
            request.model_gateway.capability = b"b" * 32
            request.context_envelope.CopyFrom(pb.ContextEnvelope(schema_version=1, ledger_seq=head_seq, ledger_checksum=head_checksum,
                events=[] if head_checksum == "invalid" else [pb.ContextEventSummary(seq=head_seq, type="session/run-leased", checksum=head_checksum)]))
            retried = list(stub.Converse(iter([pb.HarnessMessage(user_input=request)]), timeout=10,
                metadata=(("x-code-agent-run-id", resumed_run_id), ("x-code-agent-resume", "true"),
                          ("x-code-agent-surface-sha256", surface), ("x-code-agent-retry-of-run-id", predecessor))))
        if success:
            assert retried[-1].done.success, retried[-1].done.error_code
            assert len(calls) == 2 and calls[1].capability == b"b" * 32
            assert "recovered" == "".join(item.text.text for item in retried if item.HasField("text"))
        else:
            assert not retried[-1].done.success
            assert retried[-1].done.error_code == "provider_runtime_error"
            assert len(calls) == 1
    finally:
        server.stop(0).wait()
        model_server.stop(0).wait()
        app.close()
