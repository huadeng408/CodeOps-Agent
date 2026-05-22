from __future__ import annotations

import argparse
from dataclasses import dataclass
from concurrent import futures
from pathlib import Path

import grpc

from codeagent import orchestrator_pb2, orchestrator_pb2_grpc
from .config import load_dotenv
from .context import TokenBudget
from .graph.main_graph import build_graph
from .llm.providers import build_default_client
from .memory.manager import MemoryManager
from .runtime import ConversationRunner, ToolRegistry
from .skills.manager import SkillManager
from .todo.manager import TodoManager


@dataclass(slots=True)
class ServerConfig:
    host: str = "127.0.0.1"
    port: int = 50051
    memory_dir: str = ".agent/memory"
    project_root: str = "."
    working_dir: str = "."
    max_tokens: int = 1_000_000
    max_cost: float = 5.0


class OrchestratorServer:
    def __init__(self, config: ServerConfig | None = None) -> None:
        load_dotenv()
        self.config = config or ServerConfig()
        self.project_root = str(Path(self.config.project_root).resolve())
        self.working_dir = str(Path(self.config.working_dir).resolve())
        self.graph = build_graph()
        self.llm = build_default_client()
        self.tools = ToolRegistry(self.project_root)
        self.todos = TodoManager()
        self.memory = MemoryManager(self.config.memory_dir)
        self.skills = SkillManager()
        self.token_budget = TokenBudget(
            max_tokens=self.config.max_tokens,
            max_cost=self.config.max_cost,
        )

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
            skills=self.app.skills,
            project_root=self.app.project_root,
            working_dir=self.app.working_dir,
            token_budget=self.app.token_budget,
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
    parser.add_argument("--project-root", default=".")
    parser.add_argument("--working-dir", default=".")
    parser.add_argument("--max-tokens", type=int, default=1_000_000)
    parser.add_argument("--max-cost", type=float, default=5.0)
    return parser


def main(argv: list[str] | None = None) -> None:
    args = build_parser().parse_args(argv)
    server = OrchestratorServer(
        ServerConfig(
            host=args.host,
            port=args.port,
            memory_dir=args.memory_dir,
            project_root=args.project_root,
            working_dir=args.working_dir,
            max_tokens=args.max_tokens,
            max_cost=args.max_cost,
        )
    )
    server.serve()


if __name__ == "__main__":
    main()
