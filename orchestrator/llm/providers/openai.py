from __future__ import annotations

import asyncio
import json
import re
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
    split_image_segments,
)

# Reasoning models that accept the "reasoning_effort" parameter.
_REASONING_MODEL_PATTERN = re.compile(r"^(o1|o3|o4|gpt-5)", re.IGNORECASE)


@dataclass(slots=True)
class OpenAIClient(LLMClient):
    api_key: str
    base_url: str = "https://api.openai.com"
    model: str = "gpt-4o"
    timeout: float = 60.0
    max_retries: int = 3
    # thinking gating is deferred to is_thinking_enabled() +
    # request.thinking_enabled + per-model _is_reasoning_model check;
    # removed the dead per-client thinking_enabled field that was
    # read but never consulted by _chat_sync.

    @classmethod
    def from_env(cls) -> OpenAIClient | None:
        api_key = read_env("OPENAI_API_KEY")
        if not api_key or api_key.startswith("<"):
            return None
        return cls(
            api_key=api_key,
            base_url=read_env("OPENAI_BASE_URL", "https://api.openai.com"),
            model=read_env("OPENAI_MODEL", "gpt-4o"),
            timeout=_read_float("OPENAI_TIMEOUT", 60.0),
            max_retries=_read_int("OPENAI_MAX_RETRIES", 3),
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
        model = request.model or self.model
        if (
            request.thinking_enabled
            and request.reasoning_effort
            and is_thinking_enabled()
            and OpenAIClient._is_reasoning_model(model)
        ):
            payload["reasoning_effort"] = request.reasoning_effort

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

        body_bytes = http_call_with_retry(
            http_request,
            timeout=self.timeout,
            max_retries=self.max_retries,
            provider_name="OpenAI",
            cancel_event=request.cancel_event,
        )
        body = body_bytes.decode("utf-8")
        return self._parse_response(json.loads(body))

    async def stream(self, request: ChatRequest):
        """Server-Sent-Events streaming override (design 22.6).

        Sends the request with ``stream: true`` and parses the OpenAI SSE
        response line by line, yielding incremental ``"text"`` deltas as
        content arrives and accumulating tool-call argument fragments (keyed
        by ``index`` -- OpenAI streams tool-call arguments in fragments).
        Finalized tool calls, usage (from the final chunk when
        ``stream_options.include_usage`` is honoured), and ``done`` are emitted
        at the end.

        The non-stream :meth:`chat` path is unchanged. Cancellation is
        cooperative: ``request.cancel_event`` is checked before opening the
        connection and between every parsed line; when set,
        :class:`RequestInterrupted` is raised so the harness aborts the turn
        (design 22.8). The connection-establishment phase is bounded by
        ``self.timeout``; once the body streams, interrupt latency is bounded
        by chunk-arrival frequency.
        """
        cancel_event = request.cancel_event
        if cancel_event is not None and cancel_event.is_set():
            raise RequestInterrupted("OpenAI stream cancelled before open")

        payload = self._stream_payload(request)
        data = json.dumps(payload).encode("utf-8")
        headers: dict[str, str] = {"Content-Type": "application/json"}
        if self.api_key.strip():
            headers["Authorization"] = f"Bearer {self.api_key}"
        http_request = urllib.request.Request(
            self._chat_completions_url(),
            data=data,
            headers=headers,
            method="POST",
        )

        tool_accum: dict[int, dict[str, Any]] = {}
        usage = Usage()
        with urllib.request.urlopen(http_request, timeout=self.timeout) as resp:
            for raw_line in resp:
                if cancel_event is not None and cancel_event.is_set():
                    raise RequestInterrupted("OpenAI stream cancelled mid-stream")
                line = raw_line.decode("utf-8", errors="replace").rstrip("\r\n")
                if not line or not line.startswith("data:"):
                    continue
                data_str = line[len("data:"):].strip()
                if data_str == "[DONE]":
                    break
                try:
                    chunk = json.loads(data_str)
                except json.JSONDecodeError:
                    continue
                chunk_usage = chunk.get("usage")
                if isinstance(chunk_usage, dict):
                    usage = Usage(
                        input_tokens=int(chunk_usage.get("prompt_tokens", 0) or 0),
                        output_tokens=int(chunk_usage.get("completion_tokens", 0) or 0),
                        cached_input_tokens=int(
                            (chunk_usage.get("prompt_tokens_details") or {}).get(
                                "cached_tokens", 0
                            )
                            or 0
                        ),
                    )
                choices = chunk.get("choices") or []
                if not choices:
                    continue
                delta = choices[0].get("delta") or {}
                content = delta.get("content")
                if isinstance(content, str) and content:
                    yield StreamDelta(kind="text", text=content)
                for frag in delta.get("tool_calls") or []:
                    idx = int(frag.get("index", 0) or 0)
                    slot = tool_accum.setdefault(
                        idx, {"id": "", "name": "", "args": ""}
                    )
                    if frag.get("id"):
                        slot["id"] = frag["id"]
                    function = frag.get("function") or {}
                    if function.get("name"):
                        slot["name"] = function["name"]
                    if function.get("arguments"):
                        slot["args"] += function["arguments"]

        tool_calls = self._finalize_streamed_tool_calls(tool_accum)
        if tool_calls:
            yield StreamDelta(kind="tool_calls", tool_calls=tool_calls)
        yield StreamDelta(kind="usage", usage=usage)
        yield StreamDelta(kind="done")

    def _stream_payload(self, request: ChatRequest) -> dict[str, Any]:
        """Build the chat-completions payload for SSE streaming.

        Mirrors :meth:`_chat_sync`'s payload construction exactly (so the
        non-stream path stays byte-identical) and adds ``stream`` plus
        ``stream_options.include_usage`` so the final chunk carries usage.
        """
        payload: dict[str, Any] = {
            "model": request.model or self.model,
            "messages": [self._message_payload(message) for message in request.messages],
            "temperature": request.temperature,
            "stream": True,
            "stream_options": {"include_usage": True},
        }
        if request.tools:
            payload["tools"] = request.tools
        model = request.model or self.model
        if (
            request.thinking_enabled
            and request.reasoning_effort
            and is_thinking_enabled()
            and OpenAIClient._is_reasoning_model(model)
        ):
            payload["reasoning_effort"] = request.reasoning_effort
        return payload

    @staticmethod
    def _finalize_streamed_tool_calls(
        accum: dict[int, dict[str, Any]],
    ) -> list[ToolCall]:
        """Merge streamed tool-call argument fragments (keyed by index)."""
        calls: list[ToolCall] = []
        for idx in sorted(accum):
            slot = accum[idx]
            raw_args = slot["args"]
            try:
                arguments = json.loads(raw_args) if raw_args.strip() else {}
            except json.JSONDecodeError:
                arguments = {"raw": raw_args}
            calls.append(
                ToolCall(
                    id=slot["id"] or f"call_{idx}",
                    name=slot["name"],
                    arguments=arguments,
                    arguments_json=raw_args or "{}",
                )
            )
        return calls

    def _chat_completions_url(self) -> str:
        base = self.base_url.rstrip("/")
        if base.endswith("/chat/completions"):
            return base
        if base.endswith("/v1"):
            return f"{base}/chat/completions"
        return f"{base}/v1/chat/completions"

    @staticmethod
    def _is_reasoning_model(model: str) -> bool:
        """Return True when *model* accepts the ``reasoning_effort`` parameter.

        Reasoning models: o1 / o3 / o4 / gpt-5 family (prefix match).
        """
        return bool(model and _REASONING_MODEL_PATTERN.match(model))

    @staticmethod
    def _message_payload(message: ChatMessage) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "role": message.role,
        }
        # Image data-URIs in user/assistant content become native image_url
        # blocks (design 22.10). Tool results stay plain text. When there is no
        # image the original string content path is preserved exactly.
        content: Any = message.content
        if message.role in ("user", "assistant"):
            segments = split_image_segments(message.content)
            if any(kind == "image" for kind, _ in segments):
                content = OpenAIClient._image_content_blocks(segments)
        if message.role == "assistant" and message.tool_calls:
            if message.content:
                payload["content"] = content
        else:
            payload["content"] = content
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
    def _image_content_blocks(
        segments: list[tuple[str, str]],
    ) -> list[dict[str, Any]]:
        """Render ordered text/image segments as OpenAI chat content parts.

        Image segments carry the full data-URI, which OpenAI accepts verbatim as
        ``image_url.url``. Empty text runs between/around images are dropped.
        """
        blocks: list[dict[str, Any]] = []
        for kind, value in segments:
            if kind == "text":
                if value:
                    blocks.append({"type": "text", "text": value})
            else:
                blocks.append({"type": "image_url", "image_url": {"url": value}})
        return blocks

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
            thinking_blocks=OpenAIClient._extract_thinking(message),
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
    def _extract_thinking(message: dict[str, Any]) -> list[dict[str, Any]]:
        """Parse reasoning content blocks emitted by OpenAI reasoning models."""
        for key in ("reasoning_content", "reasoning"):
            raw = message.get(key)
            if not raw:
                continue
            if isinstance(raw, str):
                return [{"type": "thinking", "thinking": raw}] if raw.strip() else []
            if isinstance(raw, list):
                blocks: list[dict[str, Any]] = []
                for item in raw:
                    if isinstance(item, dict) and item.get("type") == "thinking":
                        blocks.append(item)
                    elif isinstance(item, str) and item.strip():
                        blocks.append({"type": "thinking", "thinking": item})
                return blocks
            if isinstance(raw, dict):
                return [raw]
        return []

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
