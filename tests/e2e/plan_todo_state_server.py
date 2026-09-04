from __future__ import annotations

import json

from orchestrator.llm.client import ChatRequest, ChatResponse, LLMClient, ToolCall, Usage, message_content_text
from orchestrator.server import OrchestratorServer, ServerConfig, build_parser


class DeterministicPlanTodoLLM(LLMClient):
    """Provider double used only by the cross-process Plan/Todo E2E."""

    model = "deterministic-plan-todo-e2e"

    async def chat(self, request: ChatRequest) -> ChatResponse:
        latest_user = next(
            (
                message_content_text(message.content)
                for message in reversed(request.messages)
                if message.role == "user"
            ),
            "",
        )
        rendered = "\n".join(
            f"{message.role}: {message_content_text(message.content)}"
            for message in request.messages
        )
        if latest_user == "STATE_FIRST":
            if any(message.role == "tool" for message in request.messages):
                return ChatResponse(text="STATE_FIRST_OK", usage=Usage(input_tokens=1, output_tokens=1))
            arguments = {
                "todos": [
                    {
                        "content": "patch",
                        "active_form": "patching",
                        "status": "in_progress",
                    }
                ]
            }
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        name="TodoWrite",
                        id="todo-state-first",
                        arguments=arguments,
                        arguments_json=json.dumps(arguments, sort_keys=True),
                    )
                ],
                usage=Usage(input_tokens=1, output_tokens=1),
            )
        if latest_user == "STATE_RESUME":
            if "Plan/todo state revision: 1" not in rendered or "[in_progress] patching" not in rendered:
                return ChatResponse(text="STATE_RESUME_MISSING", usage=Usage(input_tokens=1, output_tokens=1))
            if any(message.role == "tool" for message in request.messages):
                return ChatResponse(text="STATE_RESUME_OK", usage=Usage(input_tokens=1, output_tokens=1))
            arguments = {
                "steps": ["inspect", "patch"],
                "current_index": 1,
                "mode": "plan",
            }
            return ChatResponse(
                tool_calls=[
                    ToolCall(
                        name="PlanWrite",
                        id="plan-state-resume",
                        arguments=arguments,
                        arguments_json=json.dumps(arguments, sort_keys=True),
                    )
                ],
                usage=Usage(input_tokens=1, output_tokens=1),
            )
        return ChatResponse(text="STATE_UNEXPECTED", usage=Usage(input_tokens=1, output_tokens=1))


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
    deterministic = DeterministicPlanTodoLLM()
    app.llm = deterministic
    app.fast_llm = deterministic
    app.provider_clients = {"default": deterministic}
    app.serve()


if __name__ == "__main__":
    main()
