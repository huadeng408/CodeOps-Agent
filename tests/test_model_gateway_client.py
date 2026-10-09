from __future__ import annotations

import asyncio
import json
from concurrent.futures import ThreadPoolExecutor

import grpc
import pytest

from codeagent import orchestrator_pb2 as pb
from codeagent import orchestrator_pb2_grpc as rpc
from orchestrator.llm.client import ChatMessage, ChatRequest
from orchestrator.llm.gateway import HarnessModelClient
from orchestrator.server import OrchestratorServer, OrchestratorService, ServerConfig
from orchestrator.workflows.models import WorkerSpec
from orchestrator.workflows.providers import ProviderWorkerExecutor


def test_harness_model_client_uses_go_transport_and_normalized_usage():
    captured = []

    class Service(rpc.ModelGatewayServicer):
        def Invoke(self, request, context):
            captured.append(request)
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
        executor = ProviderWorkerExecutor({"default": client})
        result = asyncio.run(executor(WorkerSpec("worker-1", "Probe", "Reply briefly"), {}))
        assert result.output == "ok"
        assert captured[-1].purpose == "workflow_worker"
    finally:
        client.close()
        server.stop(0).wait()


@pytest.mark.parametrize("gateway_error", ["", "token_usage_unknown", "missing_binding"])
def test_orchestrator_conversation_uses_harness_gateway(tmp_path, monkeypatch, gateway_error):
    # Explicit blanks also prevent repository dotenv from activating a relay.
    monkeypatch.setenv("OPENAI_API_KEY", "")
    monkeypatch.setenv("ANTHROPIC_API_KEY", "")
    monkeypatch.setenv("ANTHROPIC_AUTH_TOKEN", "")
    monkeypatch.setenv("MODEL_FAST", "")
    monkeypatch.setenv("LLM_PROVIDER", "openai")
    monkeypatch.setenv("OTEL_SDK_DISABLED", "true")
    monkeypatch.setenv("CODE_AGENT_MODEL_ADMISSION", "required")
    # The controlled product must not read a credential file or construct a
    # direct provider client, even when a legacy configuration is inherited.
    monkeypatch.setenv("CODE_AGENT_PROVIDER_CONFIG", str(tmp_path / "unreadable-provider.json"))
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
