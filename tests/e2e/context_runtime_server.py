from __future__ import annotations

import json
import os
from pathlib import Path

from orchestrator.llm.client import (
    ChatRequest,
    ChatResponse,
    LLMClient,
    ToolCall,
    Usage,
    message_content_text,
)
from orchestrator.server import OrchestratorServer, ServerConfig, build_parser
from orchestrator.runtime.hooks import HookResult
from orchestrator.llm.providers.anthropic import AnthropicClient

_MARKER_PATH = "runtime/context-recovery.txt"
_TOOL_CALL_ID = "context-write-1"


def build_e2e_llm() -> LLMClient:
    """Build the deterministic double or an explicitly requested real client."""
    mode = os.environ.get("CODE_AGENT_E2E_PROVIDER", "deterministic").strip().lower()
    if mode in {"", "deterministic", "fake"}:
        return DeterministicContextLLM()
    if mode in {"anthropic", "real"}:
        client = AnthropicClient.from_env()
        if client is None:
            raise RuntimeError(
                "real provider credentials are required for CODE_AGENT_E2E_PROVIDER=anthropic"
            )
        return client
    raise ValueError(f"unsupported CODE_AGENT_E2E_PROVIDER: {mode!r}")


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

        if latest_user == "PERSIST_MEMORY_ANCHOR":
            return ChatResponse(
                text=f"MEMORY_ANCHOR_WRITTEN:{os.getpid()}",
                usage=Usage(input_tokens=1, output_tokens=1),
            )

        if latest_user == "PERSIST_MEMORY_NOISE":
            return ChatResponse(
                text=f"MEMORY_NOISE_WRITTEN:{os.getpid()}",
                usage=Usage(input_tokens=1, output_tokens=1),
            )

        if latest_user == "SEARCH MEMORY ANCHOR AFTER RESTART":
            anchor_index = rendered.find("MEMORY_ANCHOR_WRITTEN:")
            noise_index = rendered.find("MEMORY_NOISE_WRITTEN:")
            recovered = (
                "Long-term memory:" in rendered
                and anchor_index >= 0
                and noise_index >= 0
                and anchor_index < noise_index
            )
            return ChatResponse(
                text=(
                    f"MEMORY_RECOVERED:{os.getpid()}"
                    if recovered
                    else f"MEMORY_MISSING:{os.getpid()}"
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
    loop_receipt = os.environ.get("CODE_AGENT_AGENT_LOOP_E2E_RECEIPT", "").strip()
    if loop_receipt:
        receipt_path = Path(loop_receipt).resolve()

        def record_loop_phase(event) -> None:
            # Test-only observability sink. It records phase names only; no
            # prompt, tool output, credentials, or Session state is written.
            with receipt_path.open("a", encoding="utf-8", newline="\n") as handle:
                handle.write(event.phase + "\n")

        app.loop_plugins.register("e2e-phase-recorder", record_loop_phase)
    route_receipt = os.environ.get("CODE_AGENT_PROVIDER_ROUTE_E2E_RECEIPT", "").strip()
    if route_receipt:
        route_path = Path(route_receipt).resolve()
        real_route = os.environ.get("CODE_AGENT_E2E_PROVIDER", "").strip().lower() in {
            "anthropic",
            "real",
        }

        def record_route(event) -> None:
            should_record = event.phase == "model_before" and not route_path.exists()
            if real_route and event.phase == "model_after":
                should_record = True
            if not should_record:
                return
            with route_path.open("a", encoding="utf-8", newline="\n") as handle:
                handle.write(
                    json.dumps(
                        {"phase": event.phase, **event.metadata},
                        ensure_ascii=False,
                        sort_keys=True,
                    )
                    + "\n"
                )

        app.loop_plugins.register("e2e-route-recorder", record_route)
    hook_receipt = os.environ.get("CODE_AGENT_HOOK_E2E_RECEIPT", "").strip()
    if hook_receipt:
        hook_path = Path(hook_receipt).resolve()

        def record_hook(event):
            with hook_path.open("a", encoding="utf-8", newline="\n") as handle:
                handle.write(f"{event.phase}:{event.tool_name}\n")
            if event.phase == "pre_step":
                return HookResult(context=f"hook-pre-step-{event.turn}")
            if event.phase == "post_tool":
                return HookResult(context="hook-post-tool")
            return None

        app.hooks.register("e2e-hook-session-start", "session_start", record_hook)
        app.hooks.register("e2e-hook-pre-step", "pre_step", record_hook)
        app.hooks.register("e2e-hook-post-model", "post_model", record_hook)
        app.hooks.register("e2e-hook-pre-tool", "pre_tool", record_hook)
        app.hooks.register("e2e-hook-post-tool", "post_tool", record_hook)
        app.hooks.register("e2e-hook-stopping", "turn_stopping", record_hook)
        app.hooks.register("e2e-hook-session-end", "session_end", record_hook)
    llm = build_e2e_llm()
    app.llm = llm
    app.fast_llm = llm
    app.provider_clients = (
        {"route-e2e": llm}
        if route_receipt
        else {"default": llm}
    )
    app.serve()


if __name__ == "__main__":
    main()
