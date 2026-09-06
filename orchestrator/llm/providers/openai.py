from __future__ import annotations

import asyncio
import json
import re
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any
from urllib.parse import urlparse

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
    split_content_segments,
)

# Reasoning models that accept the "reasoning_effort" parameter.
_REASONING_MODEL_PATTERN = re.compile(r"^(o1|o3|o4|gpt-5)", re.IGNORECASE)
_DEEPSEEK_MODEL_PATTERN = re.compile(r"^deepseek-", re.IGNORECASE)


@dataclass(slots=True)
class OpenAIClient(LLMClient):
    api_key: str
    base_url: str = "https://api.openai.com"
    model: str = "gpt-4o"
    timeout: float = 60.0
    max_retries: int = 3
    max_tokens: int = 0
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
            max_tokens=max(0, _read_int("OPENAI_MAX_TOKENS", 0)),
        )

    async def chat(self, request: ChatRequest) -> ChatResponse:
        return await asyncio.to_thread(self._chat_sync, request)

    def _chat_sync(self, request: ChatRequest) -> ChatResponse:
        payload = {
            "model": request.model or self.model,
            "messages": self._request_messages(request.messages),
            "temperature": request.temperature,
        }
        if request.tools:
            payload["tools"] = request.tools
        if request.tool_choice is not None:
            payload["tool_choice"] = request.tool_choice
        if request.parallel_tool_calls is not None:
            payload["parallel_tool_calls"] = request.parallel_tool_calls
        thinking_payload = self._deepseek_thinking_payload(
            request, model=request.model or self.model
        )
        payload.update(thinking_payload)
        if self.max_tokens > 0:
            payload["max_tokens"] = self.max_tokens
        model = request.model or self.model
        if (
            request.thinking_enabled
            and request.reasoning_effort
            and is_thinking_enabled()
            and OpenAIClient._supports_reasoning_effort(
                model, request.reasoning_effort
            )
            and thinking_payload.get("thinking", {}).get("type") != "disabled"
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
        try:
            return self._parse_response(
                json.loads(body), requested_model=request.model or self.model
            )
        except Exception as exc_inner:
            raise RuntimeError(
                f"OpenAI response parse error: {exc_inner}"
            ) from exc_inner

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
        reasoning_parts: list[str] = []
        usage = Usage()
        try:
            response = urllib.request.urlopen(http_request, timeout=self.timeout)
        except urllib.error.HTTPError as exc:
            raw_detail = exc.read(4001)
            detail = raw_detail.decode("utf-8", errors="replace")
            if self.api_key:
                detail = detail.replace(self.api_key, "<redacted>")
            if len(detail) > 4000:
                detail = detail[:4000] + "[truncated]"
            raise RuntimeError(
                f"OpenAI HTTP {exc.code}: {detail or exc.reason}"
            ) from exc

        with response as resp:
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
                reasoning_content = delta.get("reasoning_content")
                if isinstance(reasoning_content, str) and reasoning_content:
                    reasoning_parts.append(reasoning_content)
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
        thinking_blocks = []
        if reasoning_parts:
            thinking_blocks.append(
                {"type": "thinking", "thinking": "".join(reasoning_parts)}
            )
        yield StreamDelta(kind="done", thinking_blocks=thinking_blocks)

    def _stream_payload(self, request: ChatRequest) -> dict[str, Any]:
        """Build the chat-completions payload for SSE streaming.

        Mirrors :meth:`_chat_sync`'s payload construction exactly (so the
        non-stream path stays byte-identical) and adds ``stream`` plus
        ``stream_options.include_usage`` so the final chunk carries usage.
        """
        payload: dict[str, Any] = {
            "model": request.model or self.model,
            "messages": self._request_messages(request.messages),
            "temperature": request.temperature,
            "stream": True,
            "stream_options": {"include_usage": True},
        }
        if request.tools:
            payload["tools"] = request.tools
        if request.tool_choice is not None:
            payload["tool_choice"] = request.tool_choice
        if request.parallel_tool_calls is not None:
            payload["parallel_tool_calls"] = request.parallel_tool_calls
        thinking_payload = self._deepseek_thinking_payload(
            request, model=request.model or self.model
        )
        payload.update(thinking_payload)
        if self.max_tokens > 0:
            payload["max_tokens"] = self.max_tokens
        model = request.model or self.model
        if (
            request.thinking_enabled
            and request.reasoning_effort
            and is_thinking_enabled()
            and OpenAIClient._supports_reasoning_effort(
                model, request.reasoning_effort
            )
            and thinking_payload.get("thinking", {}).get("type") != "disabled"
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

    def _deepseek_thinking_payload(
        self, request: ChatRequest, *, model: str
    ) -> dict[str, Any]:
        """Serialize DeepSeek's explicit thinking policy.

        DeepSeek defaults to thinking mode for V4 models, but its API rejects
        ``tool_choice`` while thinking is enabled.  The skill-selection lane
        deliberately requests one named tool, so an explicit disabled policy
        is required even though the shared request model represents that as
        ``thinking_enabled=False``.  Other OpenAI-compatible endpoints must
        not receive this provider-specific field.
        """
        endpoint_host = (urlparse(self.base_url).hostname or "").lower()
        is_deepseek = bool(_DEEPSEEK_MODEL_PATTERN.match(model)) or endpoint_host == "api.deepseek.com"
        if not is_deepseek:
            return {}
        if request.tool_choice is not None or request.purpose == "session-title":
            return {"thinking": {"type": "disabled"}}
        if not request.thinking_enabled:
            return {"thinking": {"type": "disabled"}}
        return {"thinking": {"type": "enabled"}}

    @staticmethod
    def _is_reasoning_model(model: str) -> bool:
        """Return True when *model* accepts the ``reasoning_effort`` parameter.

        Reasoning models: o1 / o3 / o4 / gpt-5 family (prefix match).
        """
        return bool(model and _REASONING_MODEL_PATTERN.match(model))

    @staticmethod
    def _supports_reasoning_effort(model: str, effort: str) -> bool:
        if OpenAIClient._is_reasoning_model(model):
            return True
        return bool(
            _DEEPSEEK_MODEL_PATTERN.match(model)
            and effort in {"low", "high", "max"}
        )

    @staticmethod
    def _message_payload(message: ChatMessage) -> dict[str, Any]:
        payload: dict[str, Any] = {
            "role": message.role,
        }
        # Chat Completions tool messages accept text only. _request_messages
        # emits their image blocks as a following user message.
        content: Any = message.content
        segments = split_content_segments(message.content)
        if message.role == "tool":
            content = "".join(value for kind, value in segments if kind == "text").strip()
            if not content:
                content = f"Tool {message.name or 'result'} returned image content."
        elif any(kind == "image" for kind, _ in segments):
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
        if message.role == "assistant" and message.thinking_blocks:
            reasoning_content = "".join(
                str(block.get("thinking", ""))
                for block in message.thinking_blocks
                if block.get("type") == "thinking"
            )
            if reasoning_content:
                payload["reasoning_content"] = reasoning_content
        return payload

    @staticmethod
    def _request_messages(messages: list[ChatMessage]) -> list[dict[str, Any]]:
        payloads: list[dict[str, Any]] = []
        pending_visual_content: list[dict[str, Any]] = []

        def flush_visual_content() -> None:
            if pending_visual_content:
                payloads.append({"role": "user", "content": list(pending_visual_content)})
                pending_visual_content.clear()

        for message in messages:
            if message.role == "tool":
                payloads.append(OpenAIClient._message_payload(message))
                image_segments = [
                    (kind, value)
                    for kind, value in split_content_segments(message.content)
                    if kind == "image"
                ]
                if image_segments:
                    pending_visual_content.append(
                        {
                            "type": "text",
                            "text": f"Visual content returned by tool {message.name or 'tool'}:",
                        }
                    )
                    pending_visual_content.extend(OpenAIClient._image_content_blocks(image_segments))
                continue
            flush_visual_content()
            payloads.append(OpenAIClient._message_payload(message))
        flush_visual_content()
        return payloads

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
    def _model_identity(
        payload: dict[str, Any], requested_model: str = ""
    ) -> dict[str, Any]:
        """Extract provider-reported model identity from a response body.

        The *requested* model name is an input we
        chose; it can never testify to which model actually served the request.
        Only fields the provider wrote into its own response body count as
        evidence, so a silent provider yields blank strings here rather than a
        backfill from ``requested_model`` — that backfill is precisely the
        false-identity failure §20.4 recorded.

        ``identity_verified`` is True only when the provider both names the
        serving model *and* returns an immutable build discriminator
        (``system_fingerprint``).  Without the fingerprint the caller must
        report ``MODEL_IDENTITY_UNVERIFIED``.
        """
        reported_model = str(payload.get("model") or "")
        system_fingerprint = str(payload.get("system_fingerprint") or "")
        created_raw = payload.get("created")
        try:
            created = int(created_raw) if created_raw is not None else 0
        except (TypeError, ValueError):
            created = 0
        return {
            "requested_model": str(requested_model or ""),
            "reported_model": reported_model,
            "response_id": str(payload.get("id") or ""),
            "system_fingerprint": system_fingerprint,
            "created": created,
            "identity_verified": bool(reported_model and system_fingerprint),
        }

    @staticmethod
    def _parse_response(
        payload: dict[str, Any], requested_model: str = ""
    ) -> ChatResponse:
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
                    (usage_payload.get("prompt_tokens_details") or {}).get(
                        "cached_tokens", 0
                    )
                    or 0
                ),
            ),
            model_identity=OpenAIClient._model_identity(
                payload, requested_model=requested_model
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
