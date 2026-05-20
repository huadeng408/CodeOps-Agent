from __future__ import annotations

import asyncio
import json
from collections.abc import Iterator
from dataclasses import dataclass

from codeagent import orchestrator_pb2
from orchestrator.graph.main_graph import MainGraph
from orchestrator.llm.client import ChatMessage, ChatRequest, ChatResponse, LLMClient
from orchestrator.memory.manager import Memory, MemoryManager
from orchestrator.todo.manager import Todo, TodoManager

from .tools import ToolRegistry


@dataclass(slots=True)
class ConversationRunner:
    graph: MainGraph
    llm: LLMClient | None
    tool_registry: ToolRegistry
    todo_manager: TodoManager
    memory_manager: MemoryManager
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
                if call.name == "TodoWrite":
                    try:
                        todo_items = self._decode_todos(call.arguments_json or json.dumps(call.arguments))
                    except ValueError as exc:
                        messages.append(
                            ChatMessage(
                                role="tool",
                                name=call.name,
                                tool_call_id=call.id or call.name,
                                content=f"Invalid TodoWrite payload: {exc}",
                            )
                        )
                        yield self._text(f"TodoWrite rejected: {exc}")
                        continue
                    self.todo_manager.update(todo_items)
                    snapshot = self.todo_manager.snapshot()
                    payload = orchestrator_pb2.TodoUpdate(
                        todos=[
                            orchestrator_pb2.TodoItem(
                                content=item.content,
                                active_form=item.active_form,
                                status=item.status,
                            )
                            for item in snapshot
                        ]
                    )
                    yield orchestrator_pb2.OrchestratorMessage(todo_update=payload)
                    messages.append(
                        ChatMessage(
                            role="tool",
                            name=call.name,
                            tool_call_id=call.id or call.name,
                            content=json.dumps(
                                [
                                    {
                                        "content": item.content,
                                        "active_form": item.active_form,
                                        "status": item.status,
                                    }
                                    for item in snapshot
                                ],
                                ensure_ascii=False,
                            ),
                        )
                    )
                    continue
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
        memories = self.memory_manager.load_relevant(user_text)
        memory_context = self._memory_context(memories)
        system_prompt = (
            "You are the Python orchestrator for a local code agent. "
            "Use tools when you need workspace facts or file changes. "
            "The Go harness executes tools and enforces permissions."
        )
        if memory_context:
            system_prompt += "\n\nRelevant memories:\n" + memory_context
        return [
            ChatMessage(
                role="system",
                content=system_prompt,
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

    @staticmethod
    def _memory_context(memories: list[Memory]) -> str:
        if not memories:
            return ""
        lines = []
        for item in memories[:5]:
            tags = ", ".join(item.tags) if item.tags else "none"
            content = " ".join(item.content.split())
            if len(content) > 360:
                content = content[:357].rstrip() + "..."
            lines.append(f"- {item.name} (tags: {tags}): {content}")
        return "\n".join(lines)

    @staticmethod
    def _decode_todos(arguments_json: str) -> list[Todo]:
        try:
            payload = json.loads(arguments_json or "{}")
        except json.JSONDecodeError as exc:
            raise ValueError(str(exc)) from exc
        if not isinstance(payload, dict):
            raise ValueError("payload must be an object")
        raw_todos = payload.get("todos", [])
        if not isinstance(raw_todos, list):
            raise ValueError("todos must be a list")
        todos: list[Todo] = []
        for raw in raw_todos:
            if not isinstance(raw, dict):
                continue
            todos.append(
                Todo(
                    content=str(raw.get("content", "")).strip(),
                    active_form=str(raw.get("active_form", "")).strip(),
                    status=str(raw.get("status", "pending")).strip(),
                )
            )
        return todos
