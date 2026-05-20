from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(slots=True)
class Skill:
    name: str
    description: str
    prompt: str
    tools: list[str] = field(default_factory=list)


@dataclass(slots=True)
class SkillSnapshot:
    count: int


class SkillManager:
    def __init__(self) -> None:
        self._skills: dict[str, Skill] = {}
        from .init_skill import build_skill as build_init_skill
        from .review_skill import build_skill as build_review_skill
        from .security_skill import build_skill as build_security_skill

        for skill in (
            build_init_skill(),
            build_review_skill(),
            build_security_skill(),
        ):
            self.register(skill)

    def register(self, skill: Skill) -> None:
        self._skills[skill.name] = skill

    def get(self, name: str) -> Skill | None:
        return self._skills.get(name)

    def list(self) -> list[Skill]:
        return sorted(self._skills.values(), key=lambda item: item.name)

    def snapshot(self) -> SkillSnapshot:
        return SkillSnapshot(count=len(self._skills))
