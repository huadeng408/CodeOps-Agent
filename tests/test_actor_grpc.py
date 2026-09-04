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
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=2))
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
