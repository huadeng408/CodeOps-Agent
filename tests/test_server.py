from __future__ import annotations

from concurrent import futures

import grpc

from codeagent import orchestrator_pb2, orchestrator_pb2_grpc
from orchestrator.llm.client import ChatResponse, ToolCall
from orchestrator.server import OrchestratorServer, OrchestratorService


class FakeLLM:
    model = "fake"

    def __init__(self) -> None:
        self.requests = []

    async def chat(self, request):
        self.requests.append(request)
        if len(self.requests) == 1:
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        id="call-1",
                        name="Glob",
                        arguments={"pattern": "**/*.py"},
                        arguments_json='{"pattern":"**/*.py"}',
                    )
                ]
            )
        return ChatResponse(text="found python files")


def test_health_and_converse(monkeypatch) -> None:
    monkeypatch.setenv("OPENAI_API_KEY", "")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=2))
    orchestrator_pb2_grpc.add_OrchestratorServicer_to_server(
        OrchestratorService(OrchestratorServer()),
        server,
    )
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
            stub = orchestrator_pb2_grpc.OrchestratorStub(channel)
            health = stub.Health(orchestrator_pb2.Empty())
            assert health.status == "ok"

            messages = iter(
                [
                    orchestrator_pb2.HarnessMessage(
                        user_input=orchestrator_pb2.UserInput(text="ping")
                    ),
                    orchestrator_pb2.HarnessMessage(
                        tool_result=orchestrator_pb2.ToolResult(
                            tool_name="Glob",
                            output="a.txt\nb.txt",
                        )
                    )
                ]
            )
            responses = list(stub.Converse(messages))
            assert responses[0].text.text == "Checking workspace...\n"
            assert responses[1].tool_request.tool_name == "Glob"
            assert "Files visible: 2" in responses[2].text.text
            assert responses[-1].done.success
    finally:
        server.stop(grace=0)


def test_llm_tool_call_roundtrip(monkeypatch) -> None:
    monkeypatch.setenv("OPENAI_API_KEY", "")
    app = OrchestratorServer()
    app.llm = FakeLLM()
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=2))
    orchestrator_pb2_grpc.add_OrchestratorServicer_to_server(
        OrchestratorService(app),
        server,
    )
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()
    try:
        with grpc.insecure_channel(f"127.0.0.1:{port}") as channel:
            stub = orchestrator_pb2_grpc.OrchestratorStub(channel)
            messages = iter(
                [
                    orchestrator_pb2.HarnessMessage(
                        user_input=orchestrator_pb2.UserInput(text="list python files")
                    ),
                    orchestrator_pb2.HarnessMessage(
                        tool_result=orchestrator_pb2.ToolResult(
                            tool_name="Glob",
                            output="orchestrator/server.py",
                        )
                    ),
                ]
            )
            responses = list(stub.Converse(messages))
            assert responses[0].tool_request.tool_name == "Glob"
            assert responses[0].tool_request.parameters_json == '{"pattern":"**/*.py"}'
            assert responses[1].text.text == "found python files"
            assert responses[-1].done.success
            assert app.llm.requests[1].messages[-1].role == "tool"
            assert app.llm.requests[1].messages[-1].content == "orchestrator/server.py"
    finally:
        server.stop(grace=0)
