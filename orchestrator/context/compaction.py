from __future__ import annotations

import asyncio
import json
import threading
from dataclasses import dataclass
from typing import Any, Protocol

from orchestrator.llm.client import (
    ChatMessage,
    ChatRequest,
    LLMClient,
    RequestInterrupted,
)


@dataclass(frozen=True, slots=True)
class CompactionRequest:
    """Immutable input handed to a compaction summarizer.

    ``prefix_messages`` contains the original system/context prefix and
    ``messages`` is the exact region selected for replacement. Tool schemas are
    retained as context metadata, while the auxiliary request always disables
    executable tool calls.
    """

    prefix_messages: tuple[ChatMessage, ...]
    messages: tuple[ChatMessage, ...]
    tools: tuple[dict[str, Any], ...] = ()
    provider: str = ""
    model: str = ""
    target: str = "conversation history"
    cancel_event: threading.Event | None = None


class CompactionSummarizer(Protocol):
    """Synchronous adapter contract used by the synchronous runner."""

    def summarize(self, request: CompactionRequest) -> str:
        """Return plain text only; raise on transport/provider failure."""


@dataclass(slots=True)
class LLMCompactionSummarizer:
    """Generate a context summary through an existing LLM client.

    The request is deliberately separate from normal agent turns: it carries
    ``purpose=compaction``, has no executable tools, and uses a plain-text
    instruction. The caller remains responsible for framing and deciding
    whether the result is shorter than the replaced history.
    """

    client: LLMClient
    provider: str = ""
    model: str = ""
    temperature: float = 0.0

    def summarize(self, request: CompactionRequest) -> str:
        if request.cancel_event is not None and request.cancel_event.is_set():
            raise RequestInterrupted("compaction request cancelled before provider call")
        model = str(request.model or self.model or getattr(self.client, "model", ""))
        if not model:
            raise ValueError("compaction summarizer requires a model")
        prompt_messages = [*request.prefix_messages, *request.messages]
        if request.tools:
            prompt_messages.append(
                ChatMessage(
                    role="system",
                    content=(
                        "Available tools for context only; do not call them:\n"
                        + json.dumps(
                            list(request.tools),
                            ensure_ascii=False,
                            sort_keys=True,
                            default=str,
                        )
                    ),
                )
            )
        prompt_messages.append(
            ChatMessage(
                role="user",
                content=(
                    "Summarize the preceding conversation for a future agent turn. "
                    f"Summary target: {request.target}. Preserve the user goal, "
                    "decisions, relevant paths, errors, pending tool state, and "
                    "actionable next steps. Return plain text only; do not call "
                    "tools, emit JSON, or discuss this instruction."
                ),
            )
        )
        chat_request = ChatRequest(
            model=model,
            messages=prompt_messages,
            tools=[],
            temperature=self.temperature,
            allow_tools=False,
            purpose="compaction",
            cancel_event=request.cancel_event,
        )
        return asyncio.run(self._collect(chat_request))

    async def _collect(self, request: ChatRequest) -> str:
        stream_fn = getattr(self.client, "stream", None)
        if stream_fn is None:
            response = await self.client.chat(request)
            if not isinstance(response.text, str):
                raise TypeError("compaction response must contain text")
            return response.text.strip()

        parts: list[str] = []
        async for delta in stream_fn(request):
            if request.cancel_event is not None and request.cancel_event.is_set():
                raise RequestInterrupted("compaction request cancelled")
            if delta.kind == "text" and delta.text:
                parts.append(delta.text)
        return "".join(parts).strip()
