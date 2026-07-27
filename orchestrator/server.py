from __future__ import annotations

import argparse
import logging
import os
import threading
from dataclasses import dataclass
from concurrent import futures
from pathlib import Path

import grpc

from codeagent import orchestrator_pb2, orchestrator_pb2_grpc
from .config import configure_otel, load_dotenv
from .context import TokenBudget
from .graph.main_graph import build_graph
from .llm.providers import build_default_client, build_fast_client
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
        self._otel_shutdown = configure_otel()
        self.project_root = str(Path(self.config.project_root).resolve())
        self.working_dir = str(Path(self.config.working_dir).resolve())
        self.graph = build_graph()
        self.llm = build_default_client()
        self.fast_llm = build_fast_client()
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
        finally:
            self._otel_shutdown()


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
        session_id = ""
        history: list[dict[str, str]] = []
        for message in request_iterator:
            payload = message.WhichOneof("payload")
            if payload == "user_input":
                user_input = message.user_input
                user_text = user_input.text
                session_id = user_input.session_id
                history = [
                    {
                        "role": item.role,
                        "content": item.content,
                        "created_at": item.created_at,
                    }
                    for item in user_input.history
                ]
                break

        # ── W3C TraceContext recovery ─────────────────────────────────
        # The Go harness injects the active span context via the
        # TRACEPARENT environment variable.  Parse it into an OTel Context
        # and attach it so the gen_ai inference spans inside the runner
        # become correct children of the Go-side invoke_agent span.
        otel_token = None
        try:
            traceparent = os.environ.get("TRACEPARENT", "").strip()
            if traceparent:
                from opentelemetry import context as otel_context
                from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator

                propagator = TraceContextTextMapPropagator()
                parent_ctx = propagator.extract(carrier={"traceparent": traceparent})
                otel_token = otel_context.attach(parent_ctx)
        except Exception:
            pass

        # Propagate a user interrupt (Ctrl+C) from the Go harness into the
        # orchestrator (design 22.8). When the harness cancels the gRPC call,
        # grpc fires the RPC-termination callback, which sets the event. The
        # runner polls it cooperatively and aborts the in-flight LLM call.
        cancel_event = threading.Event()
        context.add_callback(cancel_event.set)

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
            fast_llm=self.app.fast_llm,
            main_llm=self.app.llm,
        )
        try:
            yield from runner.run(
                user_text,
                request_iterator,
                session_id=session_id,
                history=history,
                cancel_event=cancel_event,
            )
        finally:
            # Detach the TraceContext parent so following calls on this
            # thread do not inherit it.
            if otel_token is not None:
                try:
                    from opentelemetry import context as _otel_context
                    _otel_context.detach(otel_token)
                except Exception:
                    pass


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
