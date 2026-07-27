from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path

CACHE_BREAK_SEPARATOR = "---CACHE_BREAK---"

# Section registry: name -> (cacheable, priority)
# Cacheable sections are placed first so the LLM provider can cache them.
_SECTION_META: dict[str, tuple[bool, int]] = {
    "identity":     (True, 0),
    "capabilities": (True, 1),
    "tools":        (True, 2),
    "project":      (True, 3),
    "memory":       (False, 4),
    "session":      (False, 5),
}


@dataclass(slots=True)
class PromptSection:
    name: str
    content: str
    cacheable: bool = True
    priority: int = 0


class SystemPromptBuilder:
    def __init__(self) -> None:
        self._sections: list[PromptSection] = []

    def add(self, section: PromptSection) -> SystemPromptBuilder:
        self._sections.append(section)
        return self

    def build(self) -> str:
        sections = sorted(
            self._sections,
            key=lambda section: (not section.cacheable, section.priority),
        )
        return "\n\n".join(
            section.content.strip()
            for section in sections
            if section.content.strip()
        )


def load_base_template() -> str:
    template_path = Path(__file__).with_name("templates") / "base.txt"
    return template_path.read_text(encoding="utf-8")


def render_template(template: str, sections: Mapping[str, str]) -> str:
    rendered = template
    for name in ("identity", "capabilities", "tools", "project", "memory", "session"):
        rendered = rendered.replace(f"{{{{{name}}}}}", (sections.get(name) or "").strip())
    return rendered.strip()


def build_system_prompt(
    sections: Mapping[str, str],
    provider: str = "",
) -> str:
    """Build a single system prompt string from sections.

    When ``provider`` is ``"anthropic"``, the caller is expected to set
    ``cache_control="ephemeral"`` on the resulting ChatMessage.  This
    function returns only the text — the caller owns the message envelope.

    The ``provider`` key inside *sections* (if present) is ignored by the
    template render itself and serves as metadata for downstream builders.
    """
    return render_template(load_base_template(), sections)


def build_with_cache_breaks(
    sections: Mapping[str, str],
    provider: str = "",
) -> list[ChatMessage]:
    """Build system prompt with cache-break aware message partitioning.

    Cacheable sections (identity, capabilities, tools, project) are placed in
    one message and marked with ``cache_control="ephemeral"`` for Anthropic.
    Dynamic sections (memory, session) follow in a separate message.

    For non-Anthropic providers the sections are joined into a single message
    with ``CACHE_BREAK_SEPARATOR`` between cacheable and dynamic parts.
    """
    from orchestrator.llm.client import ChatMessage  # local to avoid top-level coupling

    builder = SystemPromptBuilder()
    cacheable_names = {name for name, (cacheable, _) in _SECTION_META.items() if cacheable}

    for name in ("identity", "capabilities", "tools", "project", "memory", "session"):
        content = (sections.get(name) or "").strip()
        if not content:
            continue
        cacheable, priority = _SECTION_META[name]
        builder.add(
            PromptSection(
                name=name,
                content=content,
                cacheable=cacheable,
                priority=priority,
            )
        )

    sorted_sections = sorted(
        builder._sections,
        key=lambda s: (not s.cacheable, s.priority),
    )

    cacheable_text = "\n\n".join(
        s.content.strip() for s in sorted_sections if s.cacheable
    )
    dynamic_text = "\n\n".join(
        s.content.strip() for s in sorted_sections if not s.cacheable
    )

    provider_lower = provider.strip().lower()

    if provider_lower == "anthropic":
        messages: list[ChatMessage] = []
        if cacheable_text:
            messages.append(
                ChatMessage(
                    role="system",
                    content=cacheable_text,
                    cache_control="ephemeral",
                )
            )
        if dynamic_text:
            messages.append(ChatMessage(role="system", content=dynamic_text))
        return messages

    # OpenAI and other providers: single message with text separator
    parts: list[str] = []
    if cacheable_text:
        parts.append(cacheable_text)
    if dynamic_text:
        if parts:
            parts.append(CACHE_BREAK_SEPARATOR)
        parts.append(dynamic_text)

    return [ChatMessage(role="system", content="\n\n".join(parts))] if parts else []
