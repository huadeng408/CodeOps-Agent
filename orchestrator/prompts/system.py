from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path


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


def build_system_prompt(sections: Mapping[str, str]) -> str:
    return render_template(load_base_template(), sections)
