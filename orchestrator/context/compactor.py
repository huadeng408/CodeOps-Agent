from __future__ import annotations

from dataclasses import dataclass


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
        return ["[compacted history omitted]"] + keep
