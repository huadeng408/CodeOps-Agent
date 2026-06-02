from __future__ import annotations

import json
import re
from dataclasses import dataclass
from typing import Any


@dataclass(slots=True)
class Compactor:
    max_chars: int = 12_000
    max_messages: int = 40

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

    def should_compact(self, messages: list[Any]) -> bool:
        total_chars = 0
        for message in messages:
            total_chars += len(str(getattr(message, "content", message)))
        return len(messages) > self.max_messages or total_chars > self.max_chars

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
