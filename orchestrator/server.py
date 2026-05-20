from __future__ import annotations

import argparse
import json
from dataclasses import dataclass
from concurrent import futures

import grpc

from codeagent import orchestrator_pb2, orchestrator_pb2_grpc
from .config import load_dotenv
from .graph.main_graph import build_graph
from .llm.client import ChatMessage, ChatRequest
from .llm.providers.openai import OpenAIClient


@dataclass(slots=True)
class ServerConfig:
    host: str = "127.0.0.1"
    port: int = 50051


class OrchestratorServer:
    def __init__(self, config: ServerConfig | None = None) -> None:
        load_dotenv()
        self.config = config or ServerConfig()
        self.graph = build_graph()
        self.llm = OpenAIClient.from_env()

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

        yield orchestrator_pb2.OrchestratorMessage(
            text=orchestrator_pb2.TextChunk(text="Checking workspace...\n"),
        )
        yield orchestrator_pb2.OrchestratorMessage(
            tool_request=orchestrator_pb2.ToolRequest(
                tool_name="Glob",
                parameters_json=json.dumps({"pattern": "**/*"}),
                required_permission=orchestrator_pb2.AUTO_ALLOW,
            ),
        )

        tool_result = self._next_tool_result(request_iterator)
        state = self.app.graph.run()
        file_count = self._count_lines(tool_result.output) if tool_result else 0
        text = self._final_response(user_text, tool_result, file_count, state.response)

        yield orchestrator_pb2.OrchestratorMessage(
            text=orchestrator_pb2.TextChunk(text=text),
        )
        yield orchestrator_pb2.OrchestratorMessage(
            done=orchestrator_pb2.Done(success=True),
        )

    @staticmethod
    def _next_tool_result(request_iterator):
        for message in request_iterator:
            payload = message.WhichOneof("payload")
            if payload == "tool_result":
                return message.tool_result
        return None

    @staticmethod
    def _count_lines(value: str) -> int:
        if not value.strip():
            return 0
        return len(value.splitlines())

    def _final_response(
        self,
        user_text: str,
        tool_result,
        file_count: int,
        fallback: str,
    ) -> str:
        if tool_result and tool_result.error:
            return f"{fallback}: {user_text}\nTool error: {tool_result.error}"

        if self.app.llm is None:
            return f"{fallback}: {user_text}\nFiles visible: {file_count}"

        messages = [
            ChatMessage(
                role="system",
                content=(
                    "You are the Python orchestrator for a local code agent. "
                    "Answer concisely and use the provided tool summary."
                ),
            ),
            ChatMessage(
                role="user",
                content=(
                    f"User request: {user_text}\n"
                    f"Workspace visible file count: {file_count}\n"
                    "Respond in the same language as the user when practical."
                ),
            ),
        ]
        try:
            response = self._run_llm(
                ChatRequest(
                    model=self.app.llm.model,
                    messages=messages,
                )
            )
        except Exception as exc:
            return (
                f"{fallback}: {user_text}\n"
                f"Files visible: {file_count}\n"
                f"LLM fallback: {exc}"
            )
        return response or f"{fallback}: {user_text}\nFiles visible: {file_count}"

    def _run_llm(self, request: ChatRequest) -> str:
        import asyncio

        return asyncio.run(self.app.llm.chat(request)).text.strip()


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
    return parser


def main(argv: list[str] | None = None) -> None:
    args = build_parser().parse_args(argv)
    server = OrchestratorServer(ServerConfig(host=args.host, port=args.port))
    server.serve()


if __name__ == "__main__":
    main()
