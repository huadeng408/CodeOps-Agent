from __future__ import annotations

import asyncio
import json
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any

from orchestrator.config import read_env

from ..client import ChatMessage, ChatRequest, ChatResponse, LLMClient, ToolCall, Usage


@dataclass(slots=True)
class AnthropicClient(LLMClient):
    api_key: str
    base_url: str = "https://api.anthropic.com"
    model: str = "claude-sonnet-4-6"
    timeout: float = 60.0
    max_tokens: int = 4096

    @classmethod
    def from_env(cls) -> AnthropicClient | None:
        api_key = read_env("ANTHROPIC_API_KEY")
        if not api_key or api_key.startswith("<"):
            return None
        max_tokens = _read_int("ANTHROPIC_MAX_TOKENS", 4096)
        timeout = _read_float("ANTHROPIC_TIMEOUT", 60.0)
        return cls(
            api_key=api_key,
            base_url=read_env("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
            model=read_env("ANTHROPIC_MODEL", "claude-sonnet-4-6"),
            max_tokens=max_tokens,
            timeout=timeout,
        )

    async def chat(self, request: ChatRequest) -> ChatResponse:
        return await asyncio.to_thread(self._chat_sync, request)

    def _chat_sync(self, request: ChatRequest) -> ChatResponse:
        system, messages = self._convert_messages(request.messages)
        payload: dict[str, Any] = {
            "model": request.model or self.model,
            "max_tokens": self.max_tokens,
            "messages": messages,
            "temperature": request.temperature,
        }
        if system:
            payload["system"] = system
        if request.tools:
            payload["tools"] = [self._convert_tool(tool) for tool in request.tools]

        data = json.dumps(payload).encode("utf-8")
        http_request = urllib.request.Request(
            self._messages_url(),
            data=data,
            headers={
                "Content-Type": "application/json",
                "x-api-key": self.api_key,
                "anthropic-version": "2023-06-01",
            },
            method="POST",
        )

        try:
            with urllib.request.urlopen(http_request, timeout=self.timeout) as response:
                body = response.read().decode("utf-8")
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"Anthropic API error {exc.code}: {detail}") from exc
        except urllib.error.URLError as exc:
            raise RuntimeError(f"Anthropic API request failed: {exc}") from exc

        return self._parse_response(json.loads(body))

    def _messages_url(self) -> str:
        base = self.base_url.rstrip("/")
        if base.endswith("/messages"):
            return base
        if base.endswith("/v1"):
            return f"{base}/messages"
        return f"{base}/v1/messages"

    @staticmethod
    def _convert_messages(messages: list[ChatMessage]) -> tuple[str, list[dict[str, Any]]]:
        system_parts: list[str] = []
        converted: list[dict[str, Any]] = []
        for message in messages:
            if message.role == "system":
                if message.content.strip():
                    system_parts.append(message.content)
                continue
            if message.role == "assistant":
                converted.append(AnthropicClient._assistant_payload(message))
                continue
            if message.role == "tool":
                converted.append(
                    {
                        "role": "user",
                        "content": [
                            {
                                "type": "tool_result",
                                "tool_use_id": message.tool_call_id or message.name or "tool_call",
                                "content": message.content,
                                **({"is_error": True} if message.is_error else {}),
                            }
                        ],
                    }
                )
                continue
            converted.append({"role": message.role, "content": message.content})
        return "\n\n".join(system_parts), converted

    @staticmethod
    def _assistant_payload(message: ChatMessage) -> dict[str, Any]:
        content: list[dict[str, Any]] = []
        if message.content.strip():
            content.append({"type": "text", "text": message.content})
        for index, call in enumerate(message.tool_calls):
            content.append(
                {
                    "type": "tool_use",
                    "id": call.id or f"toolu_{index}",
                    "name": call.name,
                    "input": call.arguments,
                }
            )
        if not content:
            return {"role": "assistant", "content": ""}
        if len(content) == 1 and content[0]["type"] == "text":
            return {"role": "assistant", "content": content[0]["text"]}
        return {"role": "assistant", "content": content}

    @staticmethod
    def _convert_tool(tool: dict[str, Any]) -> dict[str, Any]:
        if "input_schema" in tool and "name" in tool:
            schema = tool.get("input_schema")
            if not isinstance(schema, dict):
                schema = {"type": "object", "properties": {}}
            return {
                "name": str(tool.get("name", "")).strip(),
                "description": str(tool.get("description", "")).strip(),
                "input_schema": schema,
            }

        function = tool.get("function", {}) or {}
        schema = function.get("parameters") or tool.get("parameters") or {
            "type": "object",
            "properties": {},
        }
        if not isinstance(schema, dict):
            schema = {"type": "object", "properties": {}}
        return {
            "name": str(function.get("name") or tool.get("name") or "").strip(),
            "description": str(function.get("description") or tool.get("description") or "").strip(),
            "input_schema": schema,
        }

    @staticmethod
    def _parse_response(payload: dict[str, Any]) -> ChatResponse:
        content = payload.get("content", []) or []
        text_parts: list[str] = []
        tool_calls: list[ToolCall] = []
        for block in content:
            if not isinstance(block, dict):
                continue
            block_type = block.get("type")
            if block_type == "text":
                text = block.get("text")
                if isinstance(text, str) and text:
                    text_parts.append(text)
                continue
            if block_type == "tool_use":
                raw_input = block.get("input", {}) or {}
                if not isinstance(raw_input, dict):
                    raw_input = {"raw": raw_input}
                tool_calls.append(
                    ToolCall(
                        id=str(block.get("id", "")),
                        name=str(block.get("name", "")),
                        arguments=raw_input,
                        arguments_json=json.dumps(raw_input, ensure_ascii=False, separators=(",", ":")),
                    )
                )

        usage_payload = payload.get("usage", {}) or {}
        return ChatResponse(
            text="\n".join(text_parts).strip(),
            tool_calls=tool_calls,
            usage=Usage(
                input_tokens=int(usage_payload.get("input_tokens", 0) or 0),
                output_tokens=int(usage_payload.get("output_tokens", 0) or 0),
                cached_input_tokens=int(usage_payload.get("cache_read_input_tokens", 0) or 0),
            ),
        )


def _read_int(name: str, default: int) -> int:
    value = read_env(name)
    if not value:
        return default
    try:
        return int(value)
    except ValueError:
        return default


def _read_float(name: str, default: float) -> float:
    value = read_env(name)
    if not value:
        return default
    try:
        return float(value)
    except ValueError:
        return default
