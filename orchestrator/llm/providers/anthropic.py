from __future__ import annotations

import asyncio
import json
import urllib.request
from dataclasses import dataclass
from typing import Any

from orchestrator.config import is_thinking_enabled, read_env

from ..client import (
    ChatMessage,
    ChatRequest,
    ChatResponse,
    LLMClient,
    RequestInterrupted,
    StreamDelta,
    ToolCall,
    Usage,
    http_call_with_retry,
    message_content_text,
    split_content_segments,
    split_data_uri,
)


@dataclass(slots=True)
class AnthropicClient(LLMClient):
    api_key: str
    base_url: str = "https://api.anthropic.com"
    model: str = "claude-sonnet-4-6"
    timeout: float = 60.0
    max_tokens: int = 4096
    thinking_budget_tokens: int = 10000
    max_retries: int = 3

    @classmethod
    def from_env(cls) -> AnthropicClient | None:
        api_key = read_env("ANTHROPIC_API_KEY")
        if not api_key or api_key.startswith("<"):
            return None
        max_tokens = _read_int("ANTHROPIC_MAX_TOKENS", 4096)
        timeout = _read_float("ANTHROPIC_TIMEOUT", 60.0)
        thinking_budget = _read_int("ANTHROPIC_THINKING_BUDGET_TOKENS", 10000)
        max_retries_val = _read_int("ANTHROPIC_MAX_RETRIES", 3)
        return cls(
            api_key=api_key,
            base_url=read_env("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
            model=read_env("ANTHROPIC_MODEL", "claude-sonnet-4-6"),
            max_tokens=max_tokens,
            timeout=timeout,
            thinking_budget_tokens=thinking_budget,
            max_retries=max_retries_val,
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
        if request.thinking_enabled and is_thinking_enabled():
            budget = request.thinking_budget or self.thinking_budget_tokens
            payload["thinking"] = {"type": "enabled", "budget_tokens": budget}
            if self.max_tokens <= budget:
                payload["max_tokens"] = budget + 1024
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

        body_bytes = http_call_with_retry(
            http_request,
            timeout=self.timeout,
            max_retries=self.max_retries,
            provider_name="Anthropic",
            cancel_event=request.cancel_event,
        )
        body = body_bytes.decode("utf-8")
        return self._parse_response(json.loads(body))

    async def stream(self, request: ChatRequest):
        """Server-Sent-Events streaming override (design 22.6).

        Sends the request with ``stream: true`` and parses the Anthropic SSE
        event stream:

        - ``message_start`` -> ``message.usage.input_tokens`` (and
          ``cache_read_input_tokens``).
        - ``content_block_start`` -> registers the block type at its index
          (``text`` / ``thinking`` / ``redacted_thinking`` / ``tool_use``).
        - ``content_block_delta`` -> ``delta.text`` yields a ``"text"`` delta;
          ``delta.thinking`` / ``delta.signature`` accumulate thinking;
          ``delta.partial_json`` accumulates tool-use input.
        - ``message_delta`` -> ``usage.output_tokens``.
        - ``message_stop`` -> terminate.

        Finalized tool calls, usage, and ``done`` (carrying any thinking
        blocks, so the assistant message round-trips when thinking is enabled)
        are emitted at ``message_stop``.

        The non-stream :meth:`chat` path is unchanged. Cancellation is
        cooperative: ``request.cancel_event`` is checked before opening and
        between every parsed line; when set, :class:`RequestInterrupted` is
        raised (design 22.8).
        """
        cancel_event = request.cancel_event
        if cancel_event is not None and cancel_event.is_set():
            raise RequestInterrupted("Anthropic stream cancelled before open")

        payload = self._stream_payload(request)
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

        input_tokens = 0
        output_tokens = 0
        cached_tokens = 0
        tool_accum: dict[int, dict[str, Any]] = {}
        thinking_by_index: dict[int, dict[str, str]] = {}
        redacted_blocks: list[dict[str, Any]] = []
        current_event = ""

        with urllib.request.urlopen(http_request, timeout=self.timeout) as resp:
            for raw_line in resp:
                if cancel_event is not None and cancel_event.is_set():
                    raise RequestInterrupted("Anthropic stream cancelled mid-stream")
                line = raw_line.decode("utf-8", errors="replace").rstrip("\r\n")
                if not line:
                    continue
                if line.startswith("event:"):
                    current_event = line[len("event:"):].strip()
                    continue
                if not line.startswith("data:"):
                    continue
                data_str = line[len("data:"):].strip()
                try:
                    evt = json.loads(data_str)
                except json.JSONDecodeError:
                    continue
                etype = evt.get("type") or current_event
                if etype == "message_start":
                    message = evt.get("message", {}) or {}
                    usage_payload = message.get("usage", {}) or {}
                    input_tokens = int(usage_payload.get("input_tokens", 0) or 0)
                    cached_tokens = int(
                        usage_payload.get("cache_read_input_tokens", 0) or 0
                    )
                elif etype == "content_block_start":
                    block = evt.get("content_block", {}) or {}
                    idx = int(evt.get("index", 0) or 0)
                    block_type = block.get("type", "")
                    if block_type == "tool_use":
                        tool_accum[idx] = {
                            "id": str(block.get("id", "")),
                            "name": str(block.get("name", "")),
                            "partial_json": "",
                        }
                    elif block_type == "thinking":
                        thinking_by_index[idx] = {"thinking": "", "signature": ""}
                    elif block_type == "redacted_thinking":
                        redacted_blocks.append(
                            {
                                "type": "redacted_thinking",
                                "data": str(block.get("data", "")),
                            }
                        )
                elif etype == "content_block_delta":
                    delta = evt.get("delta", {}) or {}
                    idx = int(evt.get("index", 0) or 0)
                    dtype = delta.get("type", "")
                    if dtype == "text_delta":
                        text = delta.get("text")
                        if isinstance(text, str) and text:
                            yield StreamDelta(kind="text", text=text)
                    elif dtype == "thinking_delta":
                        slot = thinking_by_index.setdefault(
                            idx, {"thinking": "", "signature": ""}
                        )
                        slot["thinking"] += delta.get("thinking", "")
                    elif dtype == "signature_delta":
                        slot = thinking_by_index.setdefault(
                            idx, {"thinking": "", "signature": ""}
                        )
                        slot["signature"] += delta.get("signature", "")
                    elif dtype == "input_json_delta":
                        slot = tool_accum.setdefault(
                            idx, {"id": "", "name": "", "partial_json": ""}
                        )
                        slot["partial_json"] += delta.get("partial_json", "")
                elif etype == "message_delta":
                    usage_payload = evt.get("usage", {}) or {}
                    if usage_payload.get("output_tokens") is not None:
                        output_tokens = int(usage_payload.get("output_tokens", 0) or 0)
                elif etype == "message_stop":
                    break

        thinking_blocks: list[dict[str, Any]] = list(redacted_blocks)
        for idx in sorted(thinking_by_index):
            slot = thinking_by_index[idx]
            thinking_blocks.append(
                {
                    "type": "thinking",
                    "thinking": slot["thinking"],
                    "signature": slot["signature"],
                }
            )
        tool_calls = self._finalize_streamed_tool_calls(tool_accum)
        if tool_calls:
            yield StreamDelta(kind="tool_calls", tool_calls=tool_calls)
        yield StreamDelta(
            kind="usage",
            usage=Usage(
                input_tokens=input_tokens,
                output_tokens=output_tokens,
                cached_input_tokens=cached_tokens,
            ),
        )
        yield StreamDelta(kind="done", thinking_blocks=thinking_blocks)

    def _stream_payload(self, request: ChatRequest) -> dict[str, Any]:
        """Build the Anthropic messages payload for SSE streaming.

        Mirrors :meth:`_chat_sync`'s payload construction exactly (so the
        non-stream path stays byte-identical) and adds ``stream: True``.
        """
        system, messages = self._convert_messages(request.messages)
        payload: dict[str, Any] = {
            "model": request.model or self.model,
            "max_tokens": self.max_tokens,
            "messages": messages,
            "temperature": request.temperature,
            "stream": True,
        }
        if request.thinking_enabled and is_thinking_enabled():
            budget = request.thinking_budget or self.thinking_budget_tokens
            payload["thinking"] = {"type": "enabled", "budget_tokens": budget}
            if self.max_tokens <= budget:
                payload["max_tokens"] = budget + 1024
        if system:
            payload["system"] = system
        if request.tools:
            payload["tools"] = [self._convert_tool(tool) for tool in request.tools]
        return payload

    @staticmethod
    def _finalize_streamed_tool_calls(
        accum: dict[int, dict[str, Any]],
    ) -> list[ToolCall]:
        """Build finalized tool calls from accumulated ``partial_json`` input."""
        calls: list[ToolCall] = []
        for idx in sorted(accum):
            slot = accum[idx]
            raw_input = slot["partial_json"]
            try:
                arguments = json.loads(raw_input) if raw_input.strip() else {}
            except json.JSONDecodeError:
                arguments = {"raw": raw_input}
            if not isinstance(arguments, dict):
                arguments = {"raw": arguments}
            calls.append(
                ToolCall(
                    id=slot["id"] or f"toolu_{idx}",
                    name=slot["name"],
                    arguments=arguments,
                    arguments_json=json.dumps(
                        arguments, ensure_ascii=False, separators=(",", ":")
                    ),
                )
            )
        return calls

    def _messages_url(self) -> str:
        base = self.base_url.rstrip("/")
        if base.endswith("/messages"):
            return base
        if base.endswith("/v1"):
            return f"{base}/messages"
        return f"{base}/v1/messages"

    @staticmethod
    def _convert_messages(
        messages: list[ChatMessage],
    ) -> tuple[str | list[dict[str, Any]], list[dict[str, Any]]]:
        system_blocks: list[dict[str, Any]] = []
        converted: list[dict[str, Any]] = []
        for message in messages:
            if message.role == "system":
                system_text = message_content_text(message.content).strip()
                if not system_text:
                    continue
                block: dict[str, Any] = {"type": "text", "text": system_text}
                if message.cache_control == "ephemeral":
                    block["cache_control"] = {"type": "ephemeral"}
                system_blocks.append(block)
                continue
            if message.role == "assistant":
                converted.append(AnthropicClient._assistant_payload(message))
                continue
            if message.role == "tool":
                segments = split_content_segments(message.content)
                has_image = any(kind == "image" for kind, _ in segments)
                tool_content: Any = (
                    AnthropicClient._image_content_blocks(segments)
                    if has_image
                    else message_content_text(message.content)
                )
                converted.append(
                    {
                        "role": "user",
                        "content": [
                            {
                                "type": "tool_result",
                                "tool_use_id": message.tool_call_id or message.name or "tool_call",
                                "content": tool_content,
                                **({"is_error": True} if message.is_error else {}),
                            }
                        ],
                    }
                )
                continue
            converted.append(AnthropicClient._user_payload(message))

        if len(system_blocks) == 1 and "cache_control" not in system_blocks[0]:
            return system_blocks[0]["text"], converted
        if not system_blocks:
            return "", converted
        return system_blocks, converted

    @staticmethod
    def _user_payload(message: ChatMessage) -> dict[str, Any]:
        """Build the Anthropic payload for a user-role message.

        When the content carries an image data-URI, the data-URI is converted
        to a native ``{"type":"image",...}`` source block (design 22.10) and any
        surrounding text becomes ``text`` blocks; otherwise the original string
        content is passed through unchanged. Image conversion applies only to
        user/assistant roles -- tool results remain plain text.
        """
        segments = split_content_segments(message.content)
        if not any(kind == "image" for kind, _ in segments):
            return {"role": message.role, "content": message_content_text(message.content)}
        return {"role": message.role, "content": AnthropicClient._image_content_blocks(segments)}

    @staticmethod
    def _image_content_blocks(
        segments: list[tuple[str, str]],
    ) -> list[dict[str, Any]]:
        """Render ordered text/image segments as Anthropic content blocks."""
        blocks: list[dict[str, Any]] = []
        for kind, value in segments:
            if kind == "text":
                if value:
                    blocks.append({"type": "text", "text": value})
                continue
            split = split_data_uri(value)
            if split is None:
                # Defensive: regex matched but split failed -- keep as text.
                blocks.append({"type": "text", "text": value})
                continue
            media_type, payload = split
            blocks.append(
                {
                    "type": "image",
                    "source": {
                        "type": "base64",
                        "media_type": media_type,
                        "data": payload,
                    },
                }
            )
        return blocks

    @staticmethod
    def _assistant_payload(message: ChatMessage) -> dict[str, Any]:
        content: list[dict[str, Any]] = []
        has_thinking = bool(message.thinking_blocks)
        for block in message.thinking_blocks:
            block_type = str(block.get("type", "")).strip()
            if block_type == "redacted_thinking":
                content.append({"type": "redacted_thinking", "data": str(block.get("data", ""))})
            elif block_type == "thinking":
                content.append(
                    {
                        "type": "thinking",
                        "thinking": str(block.get("thinking", "")),
                        "signature": str(block.get("signature", "")),
                    }
                )
        assistant_text = message_content_text(message.content)
        if assistant_text.strip():
            segments = split_content_segments(message.content)
            if any(kind == "image" for kind, _ in segments):
                content.extend(AnthropicClient._image_content_blocks(segments))
            else:
                content.append({"type": "text", "text": assistant_text})
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
        # When thinking blocks are present, always use array form (API requires it)
        # even when there is only a single text block.
        if not has_thinking and len(content) == 1 and content[0]["type"] == "text":
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
        thinking_blocks: list[dict[str, Any]] = []
        for block in content:
            if not isinstance(block, dict):
                continue
            block_type = block.get("type")
            if block_type == "thinking":
                thinking_blocks.append(
                    {
                        "type": "thinking",
                        "thinking": str(block.get("thinking", "")),
                        "signature": str(block.get("signature", "")),
                    }
                )
                continue
            if block_type == "redacted_thinking":
                thinking_blocks.append(
                    {
                        "type": "redacted_thinking",
                        "data": str(block.get("data", "")),
                    }
                )
                continue
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
            thinking_blocks=thinking_blocks,
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
