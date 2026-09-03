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

_MARKER_PATH = "runtime/context-recovery.txt"
_TOOL_CALL_ID = "context-write-1"


class DeterministicContextLLM(LLMClient):
    """Deterministic model double for the cross-language runtime E2E only."""

    model = "deterministic-context-e2e"

    async def chat(self, request: ChatRequest) -> ChatResponse:
        rendered = "\n".join(
            f"{message.role}: {message_content_text(message.content)}"
            for message in request.messages
        )
        latest_user = next(
            (
                message_content_text(message.content)
                for message in reversed(request.messages)
                if message.role == "user"
            ),
            "",
        )

        if latest_user == "RESUME_AFTER_RESTART":
            recovered = all(
                marker in rendered
                for marker in (
                    " tool_call:",
                    " file_diff:",
                    _TOOL_CALL_ID,
                    _MARKER_PATH,
                    '"status": "completed"',
                )
            )
            return ChatResponse(
                text=(
                    f"RECOVERY_CONFIRMED:{os.getpid()}"
                    if recovered
                    else f"RECOVERY_MISSING:{os.getpid()}"
                ),
                usage=Usage(input_tokens=1, output_tokens=1),
            )

        if latest_user == "COMPACTION_CHECK":
            return ChatResponse(
                text=f"COMPACTION_OK:{os.getpid()}",
                usage=Usage(input_tokens=1, output_tokens=1),
            )

        if latest_user == "COMPACTION_RECOVER":
            recovered = "[Conversation summary]" in rendered
            return ChatResponse(
                text=(
                    f"COMPACTION_RECOVERED:{os.getpid()}"
                    if recovered
                    else f"COMPACTION_MISSING:{os.getpid()}"
                ),
                usage=Usage(input_tokens=1, output_tokens=1),
            )

        if any(
            message.role == "tool" and message.tool_call_id == _TOOL_CALL_ID
            for message in request.messages
        ):
            return ChatResponse(
                text=f"WRITE_COMPLETE:{os.getpid()}",
                usage=Usage(input_tokens=1, output_tokens=1),
            )

        arguments = {
            "path": _MARKER_PATH,
            "content": "context recovery marker\n",
        }
        return ChatResponse(
            tool_calls=[
                ToolCall(
                    name="Write",
                    arguments=arguments,
                    id=_TOOL_CALL_ID,
                    arguments_json=json.dumps(arguments, sort_keys=True),
                )
            ],
            usage=Usage(input_tokens=1, output_tokens=1),
        )


def main() -> None:
    args = build_parser().parse_args()
    context_window = int(os.environ.get("CODE_AGENT_CONTEXT_E2E_WINDOW", args.context_window))
    app = OrchestratorServer(
        ServerConfig(
            host=args.host,
            port=args.port,
            memory_dir=args.memory_dir,
            project_root=args.project_root,
            working_dir=args.working_dir,
            max_tokens=args.max_tokens,
            max_cost=args.max_cost,
            context_window=context_window,
        )
    )
    deterministic = DeterministicContextLLM()
    app.llm = deterministic
    app.fast_llm = deterministic
    app.provider_clients = {"default": deterministic}
    app.serve()


if __name__ == "__main__":
    main()
