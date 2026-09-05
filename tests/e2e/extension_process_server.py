from __future__ import annotations

import json
import os

from orchestrator.llm.client import ChatRequest, ChatResponse, LLMClient, ToolCall, Usage
from orchestrator.server import OrchestratorServer, ServerConfig, build_parser


class ExtensionLLM(LLMClient):
    model = "deterministic-extension-e2e"

    async def chat(self, request: ChatRequest) -> ChatResponse:
        if any(message.role == "tool" for message in request.messages):
            return ChatResponse(
                text=f"EXTENSION_E2E_OK:{os.getpid()}",
                usage=Usage(input_tokens=1, output_tokens=1),
            )
        arguments = {
            "extension_id": "python-runtime",
            "kind": "code_runtime",
            "version": "v1",
            "operation": "inspect",
            "payload": {"path": "README.md"},
        }
        return ChatResponse(
            tool_calls=[
                ToolCall(
                    id="extension-process-1",
                    name="Extension",
                    arguments=arguments,
                    arguments_json=json.dumps(arguments, sort_keys=True),
                )
            ],
            usage=Usage(input_tokens=1, output_tokens=1),
        )


def main() -> None:
    args = build_parser().parse_args()
    app = OrchestratorServer(
        ServerConfig(
            host=args.host,
            port=args.port,
            memory_dir=args.memory_dir,
            project_root=args.project_root,
            working_dir=args.working_dir,
            max_tokens=args.max_tokens,
            max_cost=args.max_cost,
            context_window=args.context_window,
        )
    )
    deterministic = ExtensionLLM()
    app.llm = deterministic
    app.fast_llm = deterministic
    app.provider_clients = {"default": deterministic}
    app.serve()


if __name__ == "__main__":
    main()
