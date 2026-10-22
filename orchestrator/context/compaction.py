from __future__ import annotations

import asyncio
import hashlib
import json
import threading
from collections.abc import Callable
from dataclasses import dataclass, field, replace
from typing import Any, Protocol

from orchestrator.llm.client import (
    ChatMessage,
    ChatRequest,
    LLMClient,
    RequestInterrupted,
    message_content_text,
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
class CompactionResult:
    messages: list[ChatMessage]
    updates: list[dict[str, Any]] = field(default_factory=list)


def compact_surface(
    messages: list[ChatMessage],
    *,
    compactor,
    provider: str = "",
    model: str = "",
    context_window: int | None = None,
    harness_managed: bool = False,
    trigger: str = "pressure",
    force: bool = False,
    retain_ratio: float | None = None,
    summarizer: CompactionSummarizer | None = None,
    tool_schemas: Callable[[], list[dict[str, Any]]] | None = None,
    summary_target: str = "conversation history",
    cancel_event: threading.Event | None = None,
    record_event: Callable[[str, dict[str, Any]], bool] | None = None,
) -> CompactionResult:
    """Transform a Surface without owning its persistence or transport.

    The optional audit adapter runs synchronously and reports whether recording
    remains healthy, so a failed start never produces a failed-end record.
    """
    if compactor is None or len(messages) <= 2:
        return CompactionResult(messages)
    select_range = getattr(compactor, "select_compaction_range", None)
    if not callable(select_range):
        return CompactionResult(_compact_legacy(messages, compactor))

    def pressured(current, *, forced=None):
        kwargs = {"model": model, "context_window": context_window, "provider": provider}
        if forced is not None:
            kwargs["force"] = forced
        try:
            return compactor.should_compact(current, **kwargs)
        except TypeError:
            return compactor.should_compact(current)

    # Pressure precedes pruning: healthy Surfaces must remain untouched.
    if not pressured(messages, forced=force):
        return CompactionResult(messages)
    pruner = getattr(compactor, "prune_tool_results", None)
    if callable(pruner):
        original = messages
        pruned = pruner(original)
        if pruned != original:
            messages = pruned
            if record_event is not None:
                record_event("tool_result/prune", {
                    "count": sum(before is not after for before, after in zip(original, messages)),
                    "original_chars": sum(len(message_content_text(item.content)) for item in original if item.role == "tool"),
                    "pruned_chars": sum(len(message_content_text(item.content)) for item in messages if item.role == "tool"),
                    "threshold": compactor.tool_result_threshold,
                })
            if not pressured(messages):
                return CompactionResult(messages)

    current = list(messages)
    updates: list[dict[str, Any]] = []
    max_attempts = 1 if force else compactor.compaction_retries + 1
    for attempt in range(max_attempts):
        # Sourced system summaries are history; unsourced runtime prefixes are not.
        history_start = 1
        while (history_start < len(current)
               and current[history_start].role == "system"
               and not current[history_start].source_events):
            history_start += 1
        select_kwargs = {
            "history_start": history_start,
            "model": model,
            "context_window": context_window,
            "provider": provider,
            "force": force,
        }
        if retain_ratio is not None:
            select_kwargs["retain_ratio"] = retain_ratio
        selection_messages = current
        if harness_managed:
            # Only canonical history can be replaced; live messages have no Ledger refs.
            for index in range(history_start, len(current)):
                if not current[index].source_events:
                    selection_messages = current[:index + 1]
                    break
        selected = select_range(selection_messages, **select_kwargs)
        if selected is None:
            break
        selected_start, selected_end = selected
        compactable = current[selected_start:selected_end + 1]
        recent = current[selected_end + 1:]
        before_tokens = compactor.estimate_tokens(current)
        lifecycle_started = False
        if record_event is not None:
            lifecycle_started = record_event("compaction/start", {
                "range": [selected_start, selected_end],
                "trigger": trigger,
                "attempt": attempt + 1,
                "provider": provider,
                "model": model,
            })
        try:
            summary, summary_mode = _build_summary(compactor, summarizer, CompactionRequest(
                prefix_messages=tuple(current[:selected_start]),
                messages=tuple(compactable), provider=provider,
                model=model, target=summary_target, cancel_event=cancel_event,
            ), tool_schemas)
        except Exception:
            if lifecycle_started:
                record_event("compaction/end", {"status": "failed"})
            raise
        source_events = tuple(dict.fromkeys(
            ref for message in compactable for ref in message.source_events
        ))
        summary_message = ChatMessage(role="system", content=summary, source_events=source_events)
        current = [*current[:selected_start], summary_message, *recent]
        after_tokens = compactor.estimate_tokens(current)
        if record_event is not None:
            record_event("compaction/summary", {
                "replaced_tokens": compactor.estimate_tokens(compactable),
                "summary_tokens": compactor.estimate_tokens([summary_message]),
                "summary_sha256": hashlib.sha256(json.dumps(
                    summary, ensure_ascii=False, sort_keys=True, separators=(",", ":"), default=str,
                ).encode("utf-8")).hexdigest(),
                "attempt": attempt + 1,
                "summary_mode": summary_mode,
                "provider": provider,
                "model": model,
            })
            record_event("compaction/end", {
                "status": "completed",
                "replaced_tokens": max(0, before_tokens - after_tokens),
                "summary_tokens": compactor.estimate_tokens([summary_message]),
                "summary_mode": summary_mode,
                "provider": provider,
                "model": model,
            })
        updates.append({
            "summary": summary,
            "removed_messages": max(0, len(compactable)),
            "keep_recent_messages": max(0, len(recent)),
            "estimated_before_tokens": max(0, int(before_tokens)),
            "estimated_after_tokens": max(0, int(after_tokens)),
            "trigger": trigger,
            "source_events": [{"event_id": event_id, "checksum": checksum}
                              for event_id, checksum in source_events],
        })
        if force or not pressured(current):
            break
    return CompactionResult(current, updates)


def _message_for_compaction(message: ChatMessage) -> dict[str, object]:
    return {
        "role": message.role,
        "content": message_content_text(message.content),
        "tool_calls": list(message.tool_calls),
    }


def _build_summary(compactor, summarizer, request: CompactionRequest, tool_schemas) -> tuple[str, str]:
    messages = [_message_for_compaction(message) for message in request.messages]
    deterministic = compactor.compact_history(messages)
    if summarizer is None:
        return deterministic, "deterministic"
    try:
        if tool_schemas is not None:
            try:
                request = replace(request, tools=tuple(tool_schemas()))
            except Exception:
                pass
        candidate = summarizer.summarize(request)
    except RequestInterrupted:
        raise
    except Exception:
        return deterministic, "deterministic-fallback"
    if not isinstance(candidate, str) or not candidate.strip():
        return deterministic, "deterministic-fallback"
    framed = "[Compacted conversation history]\n" + candidate.strip()
    replaced_tokens = compactor.estimate_tokens(messages)
    summary_tokens = compactor.estimate_tokens([{"role": "system", "content": framed}])
    if summary_tokens >= replaced_tokens:
        return deterministic, "deterministic-fallback"
    return framed, "llm"


def _strip_leading_tool_messages(window: list[ChatMessage]) -> list[ChatMessage]:
    start = 0
    while start < len(window) and window[start].role == "tool":
        start += 1
    return window[start:]


def _compact_legacy(messages: list[ChatMessage], compactor) -> list[ChatMessage]:
    # Preserve old injected compactors through this one-way adapter.
    try:
        should_compact = compactor.should_compact(messages)
    except (AttributeError, TypeError):
        should_compact = True
    if not should_compact:
        return messages
    compactable = messages[1:]
    summary = compactor.compact_history([_message_for_compaction(message) for message in compactable])
    recent_count = min(max(1, compactor.max_messages // 2), len(compactable))
    recent = compactable[-recent_count:]
    orphan_start = next((i for i, message in enumerate(recent) if message.role == "tool"), None)
    if orphan_start is not None:
        anchored = any(message.role == "assistant" and message.tool_calls for message in recent[:orphan_start])
        if not anchored:
            for index in range(len(compactable) - len(recent) - 1, -1, -1):
                candidate = compactable[index]
                if candidate.role == "assistant" and candidate.tool_calls:
                    recent = compactable[index:]
                    break
            recent = _strip_leading_tool_messages(recent)
    return [messages[0], ChatMessage(role="system", content=summary), *recent]


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
