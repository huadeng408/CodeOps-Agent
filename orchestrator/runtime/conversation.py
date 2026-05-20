from __future__ import annotations

import asyncio
import json
from collections.abc import Iterator
from dataclasses import dataclass

from codeagent import orchestrator_pb2
from orchestrator.graph.main_graph import MainGraph
from orchestrator.llm.client import ChatMessage, ChatRequest, ChatResponse, LLMClient

from .tools import ToolRegistry


@dataclass(slots=True)
class ConversationRunner:
    graph: MainGraph
    llm: LLMClient | None
    tool_registry: ToolRegistry
    max_tool_rounds: int = 6

    def run(self, user_text: str, request_iterator) -> Iterator[orchestrator_pb2.OrchestratorMessage]:
        if self.llm is None:
            yield from self._fallback_conversation(user_text, request_iterator)
            return

        messages = self._initial_messages(user_text)
        for _ in range(self.max_tool_rounds):
            response = self._chat(messages)
            if response.text:
                yield self._text(response.text)
            if not response.tool_calls:
                yield self._done(True)
                return

            for call in response.tool_calls:
                request = self._tool_request(call.name, call.arguments_json or json.dumps(call.arguments))
                yield request
                result = self._next_tool_result(request_iterator)
                messages.append(self._tool_result_message(call.id, call.name, result))
                if result is None:
                    yield self._text("Tool result stream ended before a result was received.")
                    yield self._done(False)
                    return

        yield self._text("Tool round limit reached.")
        yield self._done(False)

    def _fallback_conversation(self, user_text: str, request_iterator) -> Iterator[orchestrator_pb2.OrchestratorMessage]:
        yield self._text("Checking workspace...\n")
        yield self._tool_request("Glob", json.dumps({"pattern": "**/*"}))
        tool_result = self._next_tool_result(request_iterator)
        state = self.graph.run()
        file_count = self._count_lines(tool_result.output) if tool_result else 0
        if tool_result and tool_result.error:
            text = f"{state.response}: {user_text}\nTool error: {tool_result.error}"
        else:
            text = f"{state.response}: {user_text}\nFiles visible: {file_count}"
        yield self._text(text)
        yield self._done(True)

    def _initial_messages(self, user_text: str) -> list[ChatMessage]:
        return [
            ChatMessage(
                role="system",
                content=(
                    "You are the Python orchestrator for a local code agent. "
                    "Use tools when you need workspace facts or file changes. "
                    "The Go harness executes tools and enforces permissions."
                ),
            ),
            ChatMessage(role="user", content=user_text),
        ]

    def _chat(self, messages: list[ChatMessage]) -> ChatResponse:
        return asyncio.run(
            self.llm.chat(
                ChatRequest(
                    model=getattr(self.llm, "model", ""),
                    messages=messages,
                    tools=self.tool_registry.openai_schemas(),
                )
            )
        )

    def _tool_request(self, name: str, parameters_json: str) -> orchestrator_pb2.OrchestratorMessage:
        return orchestrator_pb2.OrchestratorMessage(
            tool_request=orchestrator_pb2.ToolRequest(
                tool_name=name,
                parameters_json=parameters_json,
                required_permission=self.tool_registry.permission_for(name),
            )
        )

    @staticmethod
    def _tool_result_message(call_id: str, tool_name: str, result) -> ChatMessage:
        if result is None:
            content = "No tool result received."
        elif result.error:
            content = f"Tool {tool_name} failed: {result.error}\n{result.output}"
        else:
            content = result.output
        return ChatMessage(
            role="tool",
            name=tool_name,
            tool_call_id=call_id or tool_name,
            content=content,
        )

    @staticmethod
    def _next_tool_result(request_iterator):
        for message in request_iterator:
            payload = message.WhichOneof("payload")
            if payload == "tool_result":
                return message.tool_result
        return None

    @staticmethod
    def _text(text: str) -> orchestrator_pb2.OrchestratorMessage:
        return orchestrator_pb2.OrchestratorMessage(
            text=orchestrator_pb2.TextChunk(text=text),
        )

    @staticmethod
    def _done(success: bool) -> orchestrator_pb2.OrchestratorMessage:
        return orchestrator_pb2.OrchestratorMessage(
            done=orchestrator_pb2.Done(success=success),
        )

    @staticmethod
    def _count_lines(value: str) -> int:
        if not value.strip():
            return 0
        return len(value.splitlines())
