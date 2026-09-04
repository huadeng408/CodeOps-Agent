from __future__ import annotations

import json
import os

from orchestrator.llm.client import (
    ChatRequest,
    ChatResponse,
    LLMClient,
    ToolCall,
    Usage,
    message_content_text,
)
from orchestrator.server import OrchestratorServer, ServerConfig, build_parser


class SessionControlLLM(LLMClient):
    """Deterministic provider used only by the production cross-process E2E."""

    model = "deterministic-session-control-e2e"

    async def chat(self, request: ChatRequest) -> ChatResponse:
        latest_user = next(
            (
                message_content_text(message.content)
                for message in reversed(request.messages)
                if message.role == "user"
            ),
            "",
        )
        tool_messages = [
            message_content_text(message.content)
            for message in request.messages
            if message.role == "tool"
        ]
        if latest_user == "SESSION_CONTROL_E2E_RESUME":
            if any('"operation":"rewind"' in content for content in tool_messages):
                return ChatResponse(text="SESSION_CONTROL_RESUMED_OK", usage=Usage(input_tokens=1, output_tokens=1))
            arguments = {"api_version": "v1", "operation": "rewind", "target_seq": 3}
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        name="SessionRewind",
                        arguments=arguments,
                        id="session-rewind-recovery-1",
                        arguments_json=json.dumps(arguments, sort_keys=True),
                    )
                ],
                usage=Usage(input_tokens=1, output_tokens=1),
            )
        if latest_user != "SESSION_CONTROL_E2E":
            return ChatResponse(text="SESSION_CONTROL_UNEXPECTED", usage=Usage(input_tokens=1, output_tokens=1))
        if any('"operation":"rewind"' in content for content in tool_messages):
            return ChatResponse(text="SESSION_CONTROL_OK", usage=Usage(input_tokens=1, output_tokens=1))
        if any('"operation":"fork"' in content for content in tool_messages):
            if os.environ.get("CODE_AGENT_SESSION_CONTROL_STAGE") == "first":
                return ChatResponse(text="SESSION_CONTROL_FORKED", usage=Usage(input_tokens=1, output_tokens=1))
            arguments = {"api_version": "v1", "operation": "rewind", "target_seq": 3}
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        name="SessionRewind",
                        arguments=arguments,
                        id="session-rewind-1",
                        arguments_json=json.dumps(arguments, sort_keys=True),
                    )
                ],
                usage=Usage(input_tokens=1, output_tokens=1),
            )
        arguments = {
            "api_version": "v1",
            "operation": "fork",
            "target_session_id": "child-session-tool-e2e",
            "target_seq": 3,
        }
        return ChatResponse(
            tool_calls=[
                ToolCall(
                    name="SessionFork",
                    arguments=arguments,
                    id="session-fork-1",
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
    deterministic = SessionControlLLM()
    app.llm = deterministic
    app.fast_llm = deterministic
    app.provider_clients = {"default": deterministic}
    app.serve()


if __name__ == "__main__":
    main()
