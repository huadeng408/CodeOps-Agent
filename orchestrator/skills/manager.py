from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path


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
    def __init__(self, project_root: str | Path | None = None) -> None:
        self._skills: dict[str, Skill] = {}
        self._builtin_names: set[str] = set()
        self._catalog_names: set[str] = set()
        self._catalog_defaults: dict[str, Skill] = {}
        self._manifest_names: set[str] = set()
        self._manifest_path = (
            Path(project_root).resolve() / ".agent" / "skills.json"
            if project_root
            else None
        )
        self._manifest_signature: tuple[int, int] | None = None
        from .init_skill import build_skill as build_init_skill
        from .review_skill import build_skill as build_review_skill
        from .security_skill import build_skill as build_security_skill

        for skill in (
            build_init_skill(),
            build_review_skill(),
            build_security_skill(),
        ):
            self.register(skill)
            self._builtin_names.add(skill.name)

        # Keep the standalone Python orchestrator useful before the Go Harness
        # has emitted its project manifest. Specialized built-ins above retain
        # their richer prompts; catalog entries fill the remaining names.
        from .catalog import goal_skill_specs

        for name, description, tools in goal_skill_specs():
            if name in self._skills:
                continue
            skill = Skill(
                name=name,
                description=description,
                prompt=f"Use the {name} Skill to {description}.",
                tools=list(tools),
            )
            self.register(skill)
            self._builtin_names.add(name)
            self._catalog_names.add(name)
            self._catalog_defaults[name] = skill

    def register(self, skill: Skill) -> None:
        self._skills[skill.name] = skill

    def get(self, name: str) -> Skill | None:
        self._refresh_manifest()
        return self._skills.get(name)

    def list(self) -> list[Skill]:
        self._refresh_manifest()
        return sorted(self._skills.values(), key=lambda item: item.name)

    def snapshot(self) -> SkillSnapshot:
        self._refresh_manifest()
        return SkillSnapshot(count=len(self._skills))

    def _refresh_manifest(self) -> None:
        """Mirror Go Harness Skill metadata without loading instruction bodies."""
        path = self._manifest_path
        if path is None:
            return
        try:
            mtime_ns = path.stat().st_mtime_ns
        except OSError:
            if self._manifest_names:
                for name in self._manifest_names - self._builtin_names:
                    self._skills.pop(name, None)
                for name in self._manifest_names & self._catalog_names:
                    self._skills[name] = self._catalog_defaults[name]
                self._manifest_names.clear()
                self._manifest_signature = None
            return
        try:
            signature = (mtime_ns, path.stat().st_size)
        except OSError:
            return
        if signature == self._manifest_signature:
            return
        try:
            payload = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return
        raw_skills = payload.get("skills", []) if isinstance(payload, dict) else []
        next_names: set[str] = set()
        for raw in raw_skills:
            if not isinstance(raw, dict):
                continue
            name = str(raw.get("name", "")).strip()
            description = str(raw.get("description", "")).strip()
            if not name or not description:
                continue
            tools = raw.get("tools", [])
            if not isinstance(tools, list):
                tools = []
            existing = self._skills.get(name)
            prompt = (
                existing.prompt
                if existing is not None
                and name in self._builtin_names
                and name not in self._catalog_names
                else ""
            )
            self._skills[name] = Skill(
                name=name,
                description=description,
                prompt=prompt,
                tools=[str(item).strip() for item in tools if str(item).strip()],
            )
            next_names.add(name)
        # A valid project manifest is authoritative for catalog-only Skills;
        # this keeps discovery semantics deterministic when a manifest is
        # regenerated with a smaller set.
        for name in self._catalog_names - next_names:
            self._skills.pop(name, None)
        for name in self._manifest_names - next_names - self._builtin_names:
            self._skills.pop(name, None)
        self._manifest_names = next_names
        self._manifest_signature = signature
