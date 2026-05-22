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
class OpenAIClient(LLMClient):
    api_key: str
    base_url: str = "https://api.openai.com"
    model: str = "gpt-4o"
    timeout: float = 60.0

    @classmethod
    def from_env(cls) -> OpenAIClient | None:
        api_key = read_env("OPENAI_API_KEY")
        if not api_key or api_key.startswith("<"):
            return None
        return cls(
            api_key=api_key,
            base_url=read_env("OPENAI_BASE_URL", "https://api.openai.com"),
            model=read_env("OPENAI_MODEL", "gpt-4o"),
        )

    async def chat(self, request: ChatRequest) -> ChatResponse:
        return await asyncio.to_thread(self._chat_sync, request)

    def _chat_sync(self, request: ChatRequest) -> ChatResponse:
        payload = {
            "model": request.model or self.model,
            "messages": [self._message_payload(message) for message in request.messages],
            "temperature": request.temperature,
        }
        if request.tools:
            payload["tools"] = request.tools

        data = json.dumps(payload).encode("utf-8")
        headers = {
            "Content-Type": "application/json",
        }
        if self.api_key.strip():
            headers["Authorization"] = f"Bearer {self.api_key}"
        http_request = urllib.request.Request(
            self._chat_completions_url(),
            data=data,
            headers=headers,
            method="POST",
        )

        try:
            with urllib.request.urlopen(http_request, timeout=self.timeout) as response:
                body = response.read().decode("utf-8")
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode("utf-8", errors="replace")
            raise RuntimeError(f"OpenAI-compatible API error {exc.code}: {detail}") from exc
        except urllib.error.URLError as exc:
            raise RuntimeError(f"OpenAI-compatible API request failed: {exc}") from exc

        return self._parse_response(json.loads(body))

    def _chat_completions_url(self) -> str:
        base = self.base_url.rstrip("/")
        if base.endswith("/chat/completions"):
            return base
        if base.endswith("/v1"):
            return f"{base}/chat/completions"
        return f"{base}/v1/chat/completions"

    @staticmethod
    def _message_payload(message: ChatMessage) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "role": message.role,
        }
        if message.role == "assistant" and message.tool_calls:
            if message.content:
                payload["content"] = message.content
        else:
            payload["content"] = message.content
        if message.name:
            payload["name"] = message.name
        if message.tool_call_id:
            payload["tool_call_id"] = message.tool_call_id
        if message.tool_calls:
            payload["tool_calls"] = [
                {
                    "id": call.id or f"{message.role}_tool_call_{index}",
                    "type": "function",
                    "function": {
                        "name": call.name,
                        "arguments": call.arguments_json
                        or json.dumps(call.arguments, ensure_ascii=False, separators=(",", ":")),
                    },
                }
                for index, call in enumerate(message.tool_calls)
            ]
        return payload

    @staticmethod
    def _parse_response(payload: dict[str, Any]) -> ChatResponse:
        choice = payload.get("choices", [{}])[0]
        message = choice.get("message", {}) or {}
        usage_payload = payload.get("usage", {}) or {}
        tool_calls = []
        for call in message.get("tool_calls", []) or []:
            function = call.get("function", {}) or {}
            raw_arguments = function.get("arguments") or "{}"
            try:
                arguments = json.loads(raw_arguments)
            except json.JSONDecodeError:
                arguments = {"raw": raw_arguments}
            tool_calls.append(
                ToolCall(
                    id=call.get("id", ""),
                    name=function.get("name", ""),
                    arguments=arguments,
                    arguments_json=raw_arguments,
                )
            )

        return ChatResponse(
            text=OpenAIClient._message_text(message.get("content")),
            tool_calls=tool_calls,
            usage=Usage(
                input_tokens=int(usage_payload.get("prompt_tokens", 0) or 0),
                output_tokens=int(usage_payload.get("completion_tokens", 0) or 0),
                cached_input_tokens=int(
                    usage_payload.get("prompt_tokens_details", {}).get(
                        "cached_tokens", 0
                    )
                    or 0
                ),
            ),
        )

    @staticmethod
    def _message_text(content: Any) -> str:
        if isinstance(content, str):
            return content
        if isinstance(content, list):
            parts: list[str] = []
            for item in content:
                if not isinstance(item, dict):
                    continue
                if item.get("type") == "text":
                    text = item.get("text")
                    if isinstance(text, str) and text:
                        parts.append(text)
            return "".join(parts)
        if content is None:
            return ""
        return str(content)
