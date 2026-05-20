from __future__ import annotations

from concurrent import futures

import grpc

from codeagent import orchestrator_pb2, orchestrator_pb2_grpc
from orchestrator.llm.client import ChatResponse, ToolCall
from orchestrator.memory.manager import MemoryManager
from orchestrator.server import OrchestratorServer, OrchestratorService, ServerConfig


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


class TodoFakeLLM:
    model = "fake"

    def __init__(self) -> None:
        self.requests = []

    async def chat(self, request):
        self.requests.append(request)
        if len(self.requests) == 1:
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        id="todo-1",
                        name="TodoWrite",
                        arguments={
                            "todos": [
                                {
                                    "content": "Draft the plan",
                                    "active_form": "drafting the plan",
                                    "status": "in_progress",
                                },
                                {
                                    "content": "Review the plan",
                                    "active_form": "reviewing the plan",
                                    "status": "pending",
                                },
                            ]
                        },
                        arguments_json=(
                            '{"todos":['
                            '{"content":"Draft the plan","active_form":"drafting the plan","status":"in_progress"},'
                            '{"content":"Review the plan","active_form":"reviewing the plan","status":"pending"}'
                            ']}'
                        ),
                    )
                ]
            )
        return ChatResponse(text="todo list updated")


def test_health_and_converse(monkeypatch, tmp_path) -> None:
    monkeypatch.setenv("OPENAI_API_KEY", "")
    app = OrchestratorServer(ServerConfig(memory_dir=str(tmp_path)))
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


def test_llm_tool_call_roundtrip(monkeypatch, tmp_path) -> None:
    monkeypatch.setenv("OPENAI_API_KEY", "")
    app = OrchestratorServer(ServerConfig(memory_dir=str(tmp_path)))
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


def test_todo_write_emits_update_and_persists(monkeypatch, tmp_path) -> None:
    monkeypatch.setenv("OPENAI_API_KEY", "")
    app = OrchestratorServer(ServerConfig(memory_dir=str(tmp_path)))
    app.llm = TodoFakeLLM()
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
                        user_input=orchestrator_pb2.UserInput(text="track tasks")
                    )
                ]
            )
            responses = list(stub.Converse(messages))
            assert responses[0].todo_update.todos[0].content == "Draft the plan"
            assert responses[0].todo_update.todos[0].status == "in_progress"
            assert responses[1].text.text == "todo list updated"
            assert responses[-1].done.success
            assert app.todos.snapshot()[0].content == "Draft the plan"
            assert len(app.todos.snapshot()) == 2
    finally:
        server.stop(grace=0)


def test_markdown_memory_manager_persists_and_searches(tmp_path) -> None:
    manager = MemoryManager(str(tmp_path))
    saved = manager.add("Prefer Markdown memory files for durable project facts.", ["project", "#memory"])

    assert (tmp_path / f"{saved.name}.md").exists()
    assert (tmp_path / "MEMORY.md").read_text(encoding="utf-8").find(saved.name) >= 0

    reloaded = MemoryManager(str(tmp_path))
    matches = reloaded.load_relevant("durable")
    assert len(matches) == 1
    assert matches[0].content == "Prefer Markdown memory files for durable project facts."
    assert matches[0].tags == ["project", "memory"]


def test_relevant_memory_is_added_to_llm_prompt(monkeypatch, tmp_path) -> None:
    monkeypatch.setenv("OPENAI_API_KEY", "")
    app = OrchestratorServer(ServerConfig(memory_dir=str(tmp_path)))
    app.llm = FakeLLM()
    app.memory.add("Use the memory subsystem when users mention durable facts.", ["memory"])

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
                        user_input=orchestrator_pb2.UserInput(text="check memory behavior")
                    ),
                    orchestrator_pb2.HarnessMessage(
                        tool_result=orchestrator_pb2.ToolResult(
                            tool_name="Glob",
                            output="orchestrator/memory/manager.py",
                        )
                    ),
                ]
            )
            list(stub.Converse(messages))

            system_prompt = app.llm.requests[0].messages[0].content
            assert "Relevant memories:" in system_prompt
            assert "Use the memory subsystem" in system_prompt
    finally:
        server.stop(grace=0)
