from __future__ import annotations

import json
import re
from copy import copy
from dataclasses import dataclass, field
from math import ceil
from typing import Any


@dataclass(slots=True)
class Compactor:
    max_chars: int = 12_000
    max_messages: int = 40
    context_window: int | None = None
    pressure_ratio: float = 0.8
    retain_ratio: float = 0.16
    compaction_retries: int = 2
    model_context_windows: dict[str, int] = field(default_factory=dict)
    # Exact provider/model overrides, e.g. {"deepseek/reasoner": {"retain_tokens": 4096}}.
    # The mapping is intentionally metadata-only; provider credentials never belong here.
    model_policies: dict[str, dict[str, Any]] = field(default_factory=dict)
    tool_result_threshold: int = 8_192
    tool_result_head: int = 4_096
    tool_result_tail: int = 1_024

    def __post_init__(self) -> None:
        if self.context_window is not None and self.context_window < 1:
            self.context_window = None
        self.pressure_ratio = min(max(float(self.pressure_ratio), 0.01), 1.0)
        self.retain_ratio = min(max(float(self.retain_ratio), 0.01), 0.95)
        self.compaction_retries = max(0, int(self.compaction_retries))
        self.tool_result_threshold = max(0, int(self.tool_result_threshold))
        self.tool_result_head = max(0, int(self.tool_result_head))
        self.tool_result_tail = max(0, int(self.tool_result_tail))
        self.model_context_windows = {
            str(model): int(window)
            for model, window in self.model_context_windows.items()
            if int(window) > 0
        }
        normalized_policies: dict[str, dict[str, Any]] = {}
        allowed_policy_keys = {
            "context_window",
            "pressure_ratio",
            "retain_ratio",
            "retain_tokens",
        }
        for raw_key, raw_policy in self.model_policies.items():
            key = str(raw_key).strip()
            if not key:
                raise ValueError("model policy key must not be empty")
            if not isinstance(raw_policy, dict):
                raise TypeError(f"model policy {key!r} must be a mapping")
            unknown = set(raw_policy) - allowed_policy_keys
            if unknown:
                raise ValueError(
                    f"model policy {key!r} has unknown keys: {', '.join(sorted(map(str, unknown)))}"
                )
            policy = dict(raw_policy)
            if "context_window" in policy:
                context_window = int(policy["context_window"])
                if context_window < 1:
                    raise ValueError(f"model policy {key!r} context_window must be positive")
                policy["context_window"] = context_window
            for ratio_name in ("pressure_ratio", "retain_ratio"):
                if ratio_name in policy:
                    ratio = float(policy[ratio_name])
                    if not 0.0 < ratio <= 1.0:
                        raise ValueError(f"model policy {key!r} {ratio_name} must be in (0, 1]")
                    policy[ratio_name] = ratio
            if "retain_tokens" in policy:
                retain_tokens = int(policy["retain_tokens"])
                if retain_tokens < 0:
                    raise ValueError(f"model policy {key!r} retain_tokens must be non-negative")
                policy["retain_tokens"] = retain_tokens
            if "retain_ratio" in policy and "retain_tokens" in policy:
                raise ValueError(
                    f"model policy {key!r} cannot set retain_ratio and retain_tokens together"
                )
            if (
                "retain_ratio" in policy
                and "pressure_ratio" in policy
                and policy["retain_ratio"] >= policy["pressure_ratio"]
            ):
                raise ValueError(
                    f"model policy {key!r} retain_ratio must be less than pressure_ratio"
                )
            normalized_policies[key] = policy
        self.model_policies = normalized_policies

    def compact_text(self, text: str) -> str:
        if len(text) <= self.max_chars:
            return text
        return text[: self.max_chars] + "\n\n[compacted]"

    def compact_messages(self, messages: list[str]) -> list[str]:
        if len(messages) <= self.max_messages:
            return list(messages)
        keep = messages[-self.max_messages :]
        summary = self.compact_history([{"role": "message", "content": item} for item in messages[: -self.max_messages]])
        return [summary] + keep

    def compact_history(self, messages: list[dict[str, Any]], keep_recent: int | None = None) -> str:
        if keep_recent is None:
            keep_recent = max(1, min(self.max_messages, len(messages)))
        older = messages[:-keep_recent] if keep_recent < len(messages) else []
        recent = messages[-keep_recent:]
        source = older or messages
        summary = {
            "user_goal": self._first_content(source, "user"),
            "decisions": self._matching_lines(source, ("decided", "decision", "plan", "approved", "选择", "决定")),
            "paths": sorted(self._paths(source)),
            "pending_tools": self._pending_tools(source),
            "errors": self._matching_lines(source, ("error", "failed", "exception", "traceback", "错误", "失败")),
            "recent_messages": [self._message_line(message) for message in recent],
        }
        text = "[Compacted conversation history]\n" + json.dumps(summary, ensure_ascii=False, indent=2)
        return self.compact_text(text)

    def should_compact(
        self,
        messages: list[Any],
        *,
        model: str = "",
        context_window: int | None = None,
        force: bool = False,
        provider: str = "",
    ) -> bool:
        if force:
            return len(messages) > 1
        threshold = self.pressure_threshold_tokens(
            model, context_window, provider=provider
        )
        if threshold is not None:
            return self.estimate_tokens(messages) >= threshold
        total_chars = 0
        for message in messages:
            total_chars += len(str(getattr(message, "content", message)))
        return len(messages) > self.max_messages or total_chars > self.max_chars

    def effective_context_window(
        self,
        model: str = "",
        context_window: int | None = None,
        *,
        provider: str = "",
    ) -> int | None:
        policy = self._policy(provider, model)
        if "context_window" in policy:
            return int(policy["context_window"])
        if model and model in self.model_context_windows:
            return self.model_context_windows[model]
        if context_window is not None and context_window > 0:
            return int(context_window)
        return self.context_window

    def pressure_threshold_tokens(
        self,
        model: str = "",
        context_window: int | None = None,
        *,
        provider: str = "",
    ) -> int | None:
        window = self.effective_context_window(
            model, context_window, provider=provider
        )
        if window is None:
            return None
        ratio = self.effective_pressure_ratio(provider=provider, model=model)
        return max(1, int(window * ratio))

    def effective_pressure_ratio(self, *, provider: str = "", model: str = "") -> float:
        """Resolve the pressure ratio for one exact routed target."""
        policy = self._policy(provider, model)
        return float(policy.get("pressure_ratio", self.pressure_ratio))

    def effective_retain_ratio(self, *, provider: str = "", model: str = "") -> float:
        """Resolve the relative tail-retention ratio for one exact target."""
        policy = self._policy(provider, model)
        return float(policy.get("retain_ratio", self.retain_ratio))

    def effective_retain_tokens(
        self,
        *,
        provider: str = "",
        model: str = "",
        context_window: int | None = None,
    ) -> int | None:
        """Resolve absolute retention when configured, otherwise scale its ratio."""
        policy = self._policy(provider, model)
        if "retain_tokens" in policy:
            return int(policy["retain_tokens"])
        window = self.effective_context_window(
            model, context_window, provider=provider
        )
        if window is None:
            return None
        return max(0, int(window * self.effective_retain_ratio(provider=provider, model=model)))

    @staticmethod
    def estimate_tokens(messages: list[Any]) -> int:
        """Estimate provider input tokens without requiring a provider tokenizer.

        The estimate is deliberately deterministic: UTF-8 bytes are priced at
        four bytes per token, with one framing token per message and additional
        JSON cost for tool calls.  It is used for pressure and relative
        before/after measurements, never as billing evidence.
        """
        total = 0
        for message in messages:
            role = str(message.get("role", "message")) if isinstance(message, dict) else str(getattr(message, "role", "message"))
            content = message.get("content", "") if isinstance(message, dict) else getattr(message, "content", "")
            text = Compactor._content_text(content)
            total += 1 + max(1, ceil(len((role + text).encode("utf-8")) / 4))
            calls = message.get("tool_calls", []) if isinstance(message, dict) else getattr(message, "tool_calls", [])
            for call in calls or []:
                if isinstance(call, dict):
                    payload = call
                else:
                    payload = {
                        "id": getattr(call, "id", ""),
                        "name": getattr(call, "name", ""),
                        "arguments": getattr(call, "arguments", {}),
                    }
                encoded = json.dumps(payload, ensure_ascii=False, sort_keys=True, default=str)
                total += max(1, ceil(len(encoded.encode("utf-8")) / 4))
        return total

    def select_compaction_range(
        self,
        messages: list[Any],
        *,
        history_start: int = 0,
        model: str = "",
        context_window: int | None = None,
        retain_ratio: float | None = None,
        force: bool = False,
        provider: str = "",
        retain_tokens: int | None = None,
    ) -> tuple[int, int] | None:
        """Return a head-anchored inclusive range safe to replace.

        The newest history is retained by token budget.  If the retained tail
        would begin with a tool result, its assistant tool-call anchor is pulled
        into the tail.  Unattributable leading tool results are omitted rather
        than sent as an invalid provider request.
        """
        start = max(0, int(history_start))
        if len(messages) - start <= 1:
            return None
        if force and retain_ratio is None and retain_tokens is None:
            ratio = 0.0
            resolved_retain_tokens = None
        elif retain_ratio is not None:
            # An explicit call-site ratio is stronger than a target policy.
            resolved_retain_tokens = None
            ratio = min(max(float(retain_ratio), 0.0), 0.95)
        else:
            resolved_retain_tokens = retain_tokens
            if resolved_retain_tokens is None:
                resolved_retain_tokens = self.effective_retain_tokens(
                    provider=provider,
                    model=model,
                    context_window=context_window,
                )
            ratio = self.effective_retain_ratio(provider=provider, model=model)
        total_tokens = self.estimate_tokens(messages[start:])
        # Even forced overflow recovery keeps the newest request message so a
        # retry still contains the active user instruction.
        retain_tokens = (
            max(1, int(resolved_retain_tokens))
            if resolved_retain_tokens is not None
            else max(1, int(total_tokens * ratio))
        )
        keep_from = len(messages)
        accumulated = 0
        while keep_from > start and accumulated < retain_tokens:
            keep_from -= 1
            accumulated += self.estimate_tokens([messages[keep_from]])
        if keep_from <= start:
            return None

        if keep_from < len(messages) and (getattr(messages[keep_from], "role", None) == "tool" or (
            isinstance(messages[keep_from], dict) and messages[keep_from].get("role") == "tool"
        )):
            call_id = self._tool_call_id(messages[keep_from])
            anchor = self._find_tool_anchor(messages, keep_from - 1, start, call_id)
            if anchor is None:
                while keep_from < len(messages) and self._role(messages[keep_from]) == "tool":
                    keep_from += 1
            else:
                keep_from = anchor
        if keep_from <= start:
            return None
        return start, keep_from - 1

    def _policy(self, provider: str, model: str) -> dict[str, Any]:
        provider_id = str(provider).strip()
        model_id = str(model).strip()
        if provider_id and model_id:
            exact = self.model_policies.get(f"{provider_id}/{model_id}")
            if exact is not None:
                return exact
        # A model-only key is retained as a compatibility fallback for callers
        # that cannot identify a provider yet.
        return self.model_policies.get(model_id, {}) if model_id else {}

    def prune_tool_results(self, messages: list[Any]) -> list[Any]:
        """Shrink oversized tool output without invoking a model.

        The beginning and end are retained because they commonly contain the
        command summary and the final diagnostics.  Message metadata and tool
        pairing fields remain unchanged.
        """
        result: list[Any] = []
        for message in messages:
            if self._role(message) != "tool":
                result.append(message)
                continue
            content = message.get("content", "") if isinstance(message, dict) else getattr(message, "content", "")
            if not isinstance(content, str) or len(content) <= self.tool_result_threshold:
                result.append(message)
                continue
            head = content[: self.tool_result_head]
            tail = content[-self.tool_result_tail :] if self.tool_result_tail else ""
            omitted = max(0, len(content) - len(head) - len(tail))
            replacement = (
                f"{head}\n\n[tool result pruned: {omitted} characters omitted]\n\n{tail}"
            )
            if isinstance(message, dict):
                cloned = dict(message)
                cloned["content"] = replacement
            else:
                cloned = copy(message)
                cloned.content = replacement
            result.append(cloned)
        return result

    @staticmethod
    def _role(message: Any) -> str:
        return str(message.get("role", "") if isinstance(message, dict) else getattr(message, "role", ""))

    @staticmethod
    def _content_text(content: Any) -> str:
        if isinstance(content, str):
            return content
        if isinstance(content, list):
            return " ".join(Compactor._content_text(item) for item in content)
        if isinstance(content, dict):
            if "text" in content:
                return str(content["text"])
            return json.dumps(content, ensure_ascii=False, sort_keys=True, default=str)
        return str(content)

    @classmethod
    def _tool_call_id(cls, message: Any) -> str:
        if isinstance(message, dict):
            return str(message.get("tool_call_id", ""))
        return str(getattr(message, "tool_call_id", "") or "")

    @classmethod
    def _find_tool_anchor(
        cls, messages: list[Any], index: int, start: int, call_id: str
    ) -> int | None:
        for candidate_index in range(index, start - 1, -1):
            candidate = messages[candidate_index]
            if cls._role(candidate) != "assistant":
                continue
            calls = candidate.get("tool_calls", []) if isinstance(candidate, dict) else getattr(candidate, "tool_calls", [])
            if not calls:
                continue
            if not call_id or any(str(getattr(call, "id", "") if not isinstance(call, dict) else call.get("id", "")) == call_id for call in calls):
                return candidate_index
        return None

    @staticmethod
    def _first_content(messages: list[dict[str, Any]], role: str) -> str:
        for message in messages:
            if message.get("role") == role:
                return str(message.get("content", "")).strip()[:800]
        return ""

    @staticmethod
    def _message_line(message: dict[str, Any]) -> str:
        role = str(message.get("role", "message"))
        content = " ".join(str(message.get("content", "")).split())
        if len(content) > 500:
            content = content[:497].rstrip() + "..."
        return f"{role}: {content}"

    def _matching_lines(self, messages: list[dict[str, Any]], needles: tuple[str, ...]) -> list[str]:
        matches: list[str] = []
        for message in messages:
            content = str(message.get("content", ""))
            for line in content.splitlines():
                lower = line.lower()
                if any(needle in lower for needle in needles):
                    matches.append(line.strip()[:500])
                    break
            if len(matches) >= 12:
                break
        return matches

    @staticmethod
    def _paths(messages: list[dict[str, Any]]) -> set[str]:
        pattern = re.compile(r"(?:[A-Za-z0-9_.-]+/)+[A-Za-z0-9_.-]+")
        paths: set[str] = set()
        for message in messages:
            content = str(message.get("content", ""))
            paths.update(match.group(0) for match in pattern.finditer(content))
        return paths

    @staticmethod
    def _pending_tools(messages: list[dict[str, Any]]) -> list[str]:
        pending: list[str] = []
        for message in messages:
            tool_calls = message.get("tool_calls") or []
            for call in tool_calls:
                name = getattr(call, "name", None)
                if name is None and isinstance(call, dict):
                    name = call.get("name")
                if name:
                    pending.append(str(name))
        return pending[:20]
