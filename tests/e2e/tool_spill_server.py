from __future__ import annotations

import json
from orchestrator.llm.client import ChatRequest, ChatResponse, LLMClient, ToolCall, Usage, message_content_text
from orchestrator.server import OrchestratorServer, ServerConfig, build_parser


class SpillLLM(LLMClient):
    model = "deterministic-tool-spill-e2e"

    async def chat(self, request: ChatRequest) -> ChatResponse:
        latest = next((message_content_text(m.content) for m in reversed(request.messages) if m.role == "user"), "")
        tool = next((m for m in reversed(request.messages) if m.role == "tool"), None)
        tool_text = message_content_text(tool.content) if tool is not None else ""
        if tool is None and latest == "spill":
            args = {"path": "large.txt"}
            return ChatResponse(tool_calls=[ToolCall(name="Read", arguments=args, id="spill-read-1", arguments_json=json.dumps(args))], usage=Usage(input_tokens=1, output_tokens=1))
        if "Complete redacted output: spill://" in tool_text:
            locator = tool_text.split("Complete redacted output: ", 1)[1].split(".", 1)[0]
            args = {"locator": locator, "start": 2, "limit": 2}
            return ChatResponse(tool_calls=[ToolCall(name="ReadSpill", arguments=args, id="spill-read-2", arguments_json=json.dumps(args))], usage=Usage(input_tokens=1, output_tokens=1))
        if tool is not None and "line" in tool_text:
            return ChatResponse(text="SPILL_ROUNDTRIP_OK:" + tool_text.splitlines()[0], usage=Usage(input_tokens=1, output_tokens=1))
        return ChatResponse(text="SPILL_ROUNDTRIP_FAILED", usage=Usage(input_tokens=1, output_tokens=1))


def main() -> None:
    args = build_parser().parse_args()
    app = OrchestratorServer(ServerConfig(host=args.host, port=args.port, memory_dir=args.memory_dir, project_root=args.project_root, working_dir=args.working_dir))
    llm = SpillLLM()
    app.llm = llm
    app.fast_llm = llm
    app.provider_clients = {"default": llm}
    app.serve()


if __name__ == "__main__":
    main()
