from __future__ import annotations

import argparse
from dataclasses import dataclass
from concurrent import futures

import grpc

from codeagent import orchestrator_pb2, orchestrator_pb2_grpc
from .config import load_dotenv
from .graph.main_graph import build_graph
from .llm.providers.openai import OpenAIClient
from .memory.manager import MemoryManager
from .runtime import ConversationRunner, ToolRegistry
from .todo.manager import TodoManager


@dataclass(slots=True)
class ServerConfig:
    host: str = "127.0.0.1"
    port: int = 50051
    memory_dir: str = ".agent/memory"


class OrchestratorServer:
    def __init__(self, config: ServerConfig | None = None) -> None:
        load_dotenv()
        self.config = config or ServerConfig()
        self.graph = build_graph()
        self.llm = OpenAIClient.from_env()
        self.tools = ToolRegistry()
        self.todos = TodoManager()
        self.memory = MemoryManager(self.config.memory_dir)

    def serve(self) -> None:
        server = create_grpc_server(self)
        address = f"{self.config.host}:{self.config.port}"
        server.add_insecure_port(address)
        server.start()
        print(
            f"[orchestrator] gRPC server skeleton on "
            f"{address}"
        )
        print(f"[orchestrator] graph: {self.graph.name}")
        try:
            server.wait_for_termination()
        except KeyboardInterrupt:
            server.stop(grace=1)


class OrchestratorService(orchestrator_pb2_grpc.OrchestratorServicer):
    def __init__(self, app: OrchestratorServer) -> None:
        self.app = app

    def Health(self, request, context):
        return orchestrator_pb2.HealthResponse(
            status="ok",
            version="0.1.0",
        )

    def Converse(self, request_iterator, context):
        user_text = ""
        for message in request_iterator:
            payload = message.WhichOneof("payload")
            if payload == "user_input":
                user_text = message.user_input.text
                break

        runner = ConversationRunner(
            graph=self.app.graph,
            llm=self.app.llm,
            tool_registry=self.app.tools,
            todo_manager=self.app.todos,
            memory_manager=self.app.memory,
        )
        yield from runner.run(user_text, request_iterator)


def create_grpc_server(app: OrchestratorServer | None = None) -> grpc.Server:
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=10))
    orchestrator_pb2_grpc.add_OrchestratorServicer_to_server(
        OrchestratorService(app or OrchestratorServer()),
        server,
    )
    return server


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="orchestrator")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=50051)
    parser.add_argument("--memory-dir", default=".agent/memory")
    return parser


def main(argv: list[str] | None = None) -> None:
    args = build_parser().parse_args(argv)
    server = OrchestratorServer(
        ServerConfig(host=args.host, port=args.port, memory_dir=args.memory_dir)
    )
    server.serve()


if __name__ == "__main__":
    main()
