from __future__ import annotations

import json
import os
import re
import stat
from collections.abc import Iterable
from dataclasses import dataclass, field
from pathlib import Path
import yaml


@dataclass(slots=True)
class Skill:
    name: str
    description: str
    prompt: str
    tools: list[str] = field(default_factory=list)
    when_to_use: str = ""
    source: str = ""
    provider: str = ""
    path: str = ""
    resource_base: str = ""
    invocation: "InvocationPolicy" = field(default_factory=lambda: InvocationPolicy())


@dataclass(slots=True)
class InvocationPolicy:
    model_invocable: bool = True
    user_invocable: bool = True


@dataclass(slots=True)
class SkillSnapshot:
    count: int
    complete: bool = True
    revision: int = 1


@dataclass(frozen=True, slots=True)
class _DiscoveryRoot:
    path: Path | None
    source: str
    rank: int


class SkillManager:
    def __init__(self, project_root: str | Path | None = None) -> None:
        self._skills: dict[str, Skill] = {}
        self._builtin_names: set[str] = set()
        self._catalog_names: set[str] = set()
        self._catalog_defaults: dict[str, Skill] = {}
        self._builtin_defaults: dict[str, Skill] = {}
        self._manifest_names: set[str] = set()
        self._discovered_names: set[str] = set()
        self._lazy_paths: dict[str, Path] = {}
        self._discovery_complete = True
        self._manifest_path = (
            Path(project_root).resolve() / ".agent" / "skills.json"
            if project_root
            else None
        )
        self._manifest_signature: tuple[int, int] | None = None
        self._complete = True
        self._revision = 1
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

        self._builtin_defaults = dict(self._skills)
        if project_root:
            root = Path(project_root).resolve()
            self.discover(
                project_dsh_dir=root / ".dsh" / "skills",
                project_agents_dir=root / ".agents" / "skills",
                project_dir=root / ".agent" / "skills",
            )

    def register(self, skill: Skill) -> None:
        _validate_skill_metadata(skill.name, skill.description, skill.tools)
        self._skills[skill.name] = skill

    def get(self, name: str) -> Skill | None:
        self._refresh_manifest()
        return self._skills.get(name)

    def load(self, name: str) -> Skill | None:
        self._refresh_manifest()
        skill = self._skills.get(name)
        path = self._lazy_paths.get(name)
        if skill is None or path is None:
            return skill
        try:
            skill.prompt = _read_skill_body(path)
        except OSError as exc:
            raise ValueError(f"load skill {name!r}: {exc}") from exc
        self._lazy_paths.pop(name, None)
        return skill

    def read_resource(self, name: str, resource: str) -> bytes:
        """Read a Skill resource while keeping access inside its directory."""
        skill = self.get(name)
        if skill is None:
            raise ValueError(f"skill {name!r} not found")
        if not skill.resource_base:
            raise ValueError(f"skill {name!r} has no resource directory")
        requested = Path(resource)
        if not resource.strip() or requested.is_absolute():
            raise ValueError("invalid skill resource path")
        base = Path(skill.resource_base).resolve()
        target = (base / requested).resolve()
        try:
            target.relative_to(base)
        except ValueError as exc:
            raise ValueError("skill resource path escapes skill directory") from exc
        return _read_bounded_file(target)

    def list(self, *, for_model: bool = False, for_user: bool = False) -> list[Skill]:
        if for_model and for_user:
            raise ValueError("for_model and for_user are mutually exclusive")
        self._refresh_manifest()
        values = self._skills.values()
        if for_model:
            values = (item for item in values if item.invocation.model_invocable)
        elif for_user:
            values = (item for item in values if item.invocation.user_invocable)
        return sorted(values, key=lambda item: item.name)

    def snapshot(self) -> SkillSnapshot:
        self._refresh_manifest()
        return SkillSnapshot(
            count=len(self._skills),
            complete=self._complete and self._discovery_complete,
            revision=self._revision,
        )

    def discover(
        self,
        directories: Iterable[str | Path] = (),
        *,
        project_dsh_dir: str | Path | None = None,
        project_agents_dir: str | Path | None = None,
        project_dir: str | Path | None = None,
        user_dsh_dir: str | Path | None = None,
        user_agents_dir: str | Path | None = None,
        global_dir: str | Path | None = None,
        bundled_dir: str | Path | None = None,
    ) -> None:
        roots = [
            _DiscoveryRoot(_as_path(project_dsh_dir), "project-dsh", 100),
            _DiscoveryRoot(_as_path(project_agents_dir), "project-agents", 200),
            _DiscoveryRoot(_as_path(project_dir), "project", 250),
        ]
        roots.extend(_DiscoveryRoot(_as_path(path), "custom", 300) for path in directories)
        roots.extend(
            [
                _DiscoveryRoot(_as_path(user_dsh_dir), "user-dsh", 400),
                _DiscoveryRoot(_as_path(user_agents_dir), "user-agents", 500),
                _DiscoveryRoot(_as_path(global_dir), "global", 550),
                _DiscoveryRoot(_as_path(bundled_dir), "bundled", 600),
            ]
        )
        winners: dict[str, tuple[Skill, Path, int, int]] = {}
        order = 0
        try:
            for root in roots:
                for skill, path in _discover_root(root):
                    current = winners.get(skill.name)
                    candidate = (skill, path, root.rank, order)
                    if current is None or (root.rank, order) < (current[2], current[3]):
                        winners[skill.name] = candidate
                    order += 1
        except (OSError, ValueError) as exc:
            self._discovery_complete = False
            self._complete = False
            # Discovery is a read-model refresh. Keep the last-good catalog
            # intact when one candidate is malformed or temporarily unreadable.
            return

        for name in self._discovered_names:
            self._lazy_paths.pop(name, None)
            if name in self._builtin_names:
                self._skills[name] = self._builtin_defaults[name]
            else:
                self._skills.pop(name, None)
                self._lazy_paths.pop(name, None)
        for name, (skill, path, _rank, _order) in winners.items():
            self._skills[name] = skill
            self._lazy_paths[name] = path
        self._discovered_names = set(winners)
        self._discovery_complete = True
        self._complete = True
        self._revision += 1

    def _refresh_manifest(self) -> None:
        """Mirror Go Harness Skill metadata without loading instruction bodies."""
        path = self._manifest_path
        if path is None:
            return
        try:
            mtime_ns = path.stat().st_mtime_ns
        except OSError:
            self._complete = False
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
            self._complete = False
            return
        entries = _parse_manifest_entries(payload)
        if entries is None:
            # A malformed replacement must not erase the last known-good
            # catalog or partially apply attacker-controlled metadata.
            self._complete = False
            return
        next_names: set[str] = set()
        for entry in entries:
            name = entry["name"]
            description = entry["description"]
            tools = entry["tools"]
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
                when_to_use=entry["when_to_use"],
                source=entry["source"],
                provider=entry["provider"],
                path=entry["path"],
                resource_base=entry["resource_base"],
                invocation=entry["invocation"],
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
        self._complete = True
        self._revision += 1


_SKILL_NAME = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
_MAX_FRONTMATTER_BYTES = 64 << 10
_MAX_RESOURCE_BYTES = 256 << 10


def _as_path(value: str | Path | None) -> Path | None:
    """Normalize optional discovery roots without treating an empty path as cwd."""
    if value is None:
        return None
    if isinstance(value, Path):
        return value
    text = str(value).strip()
    return Path(text) if text else None


def _discover_root(root: _DiscoveryRoot) -> list[tuple[Skill, Path]]:
    """Read metadata from one filesystem root while keeping prompt bodies lazy."""
    if root.path is None or not root.path.exists():
        return []
    if not root.path.is_dir():
        raise ValueError(f"Skill discovery root is not a directory: {root.path}")

    candidates: list[Path] = []
    for child in sorted(root.path.iterdir(), key=lambda item: item.name.casefold()):
        if child.is_dir():
            skill_file = child / "SKILL.md"
            if skill_file.exists():
                candidates.append(skill_file)
        elif child.is_file() and child.suffix.casefold() == ".md":
            candidates.append(child)

    discovered: list[tuple[Skill, Path]] = []
    for path in candidates:
        metadata = _read_skill_metadata(path)
        skill = _skill_from_metadata(metadata, path, root)
        discovered.append((skill, path))
    return discovered


def _read_skill_metadata(path: Path) -> dict[str, object]:
    try:
        with path.open("rb") as stream:
            first = stream.readline(_MAX_FRONTMATTER_BYTES + 1)
            if first.decode("utf-8-sig").strip() != "---":
                raise ValueError(f"Skill file missing frontmatter: {path}")
            lines: list[str] = []
            consumed = len(first)
            while True:
                line = stream.readline(_MAX_FRONTMATTER_BYTES + 1)
                consumed += len(line)
                if consumed > _MAX_FRONTMATTER_BYTES:
                    raise ValueError(f"Skill frontmatter is too large: {path}")
                if not line:
                    raise ValueError(f"Skill file missing closing frontmatter: {path}")
                text = line.decode("utf-8")
                if text.strip() == "---":
                    break
                lines.append(text)
        raw = yaml.safe_load("".join(lines))
    except (OSError, UnicodeError, yaml.YAMLError) as exc:
        raise ValueError(f"read {path}: {exc}") from exc
    if raw is None:
        raw = {}
    if not isinstance(raw, dict):
        raise ValueError(f"Skill frontmatter must be a mapping: {path}")
    return {str(key): value for key, value in raw.items()}


def _read_skill_body(path: Path) -> str:
    text = _read_bounded_file(path).decode("utf-8-sig")
    lines = text.splitlines()
    closing = next((index for index, line in enumerate(lines[1:], start=1) if line.strip() == "---"), None)
    if closing is None or not lines or lines[0].strip() != "---":
        raise ValueError(f"Skill file missing frontmatter: {path}")
    return "\n".join(lines[closing + 1:]).strip()


def _skill_from_metadata(metadata: dict[str, object], path: Path, root: _DiscoveryRoot) -> Skill:
    name = metadata.get("name")
    description = metadata.get("description")
    if not isinstance(name, str) or not isinstance(description, str):
        raise ValueError(f"Skill {path} requires string name and description")
    raw_tools = metadata.get("tools", metadata.get("allowed-tools", []))
    if isinstance(raw_tools, str):
        tools = [item for item in re.split(r"[,\s]+", raw_tools) if item]
    elif isinstance(raw_tools, list):
        if not all(isinstance(item, str) for item in raw_tools):
            raise ValueError(f"Skill {path} contains invalid tools metadata")
        tools = [item.strip() for item in raw_tools]
    else:
        raise ValueError(f"Skill {path} contains invalid tools metadata")

    disable_model = metadata.get("disable-model-invocation", metadata.get("disable_model_invocation", False))
    user_invocable = metadata.get("user-invocable", metadata.get("user_invocable", True))
    if not isinstance(disable_model, bool) or not isinstance(user_invocable, bool):
        raise ValueError(f"Skill {path} contains invalid invocation metadata")
    when_to_use = metadata.get("whenToUse", metadata.get("when_to_use", ""))
    if not isinstance(when_to_use, str):
        raise ValueError(f"Skill {path} contains invalid whenToUse metadata")
    _validate_skill_metadata(name.strip(), description.strip(), tools)
    return Skill(
        name=name.strip(),
        description=description.strip(),
        prompt="",
        tools=tools,
        when_to_use=when_to_use.strip(),
        source=root.source,
        provider="filesystem",
        path=str(path.resolve()),
        resource_base=str(path.parent.resolve()),
        invocation=InvocationPolicy(
            model_invocable=not disable_model,
            user_invocable=user_invocable,
        ),
    )


def _read_bounded_file(path: Path) -> bytes:
    with path.open("rb") as stream:
        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
            raise ValueError("skill resource must be a regular file")
        data = stream.read(_MAX_RESOURCE_BYTES + 1)
    if len(data) > _MAX_RESOURCE_BYTES:
        raise ValueError("skill resource is too large")
    return data


def _validate_skill_metadata(name: str, description: str, tools: list[str]) -> None:
    if not isinstance(name, str) or not _SKILL_NAME.fullmatch(name):
        raise ValueError(f"invalid Skill name: {name!r}")
    if not isinstance(description, str) or not description.strip():
        raise ValueError(f"Skill {name!r} requires a description")
    for tool in tools:
        if not isinstance(tool, str) or not tool.strip() or any(char in tool for char in "\r\n\t"):
            raise ValueError(f"Skill {name!r} contains invalid tool metadata")


def _parse_manifest_entries(payload: object) -> list[dict[str, object]] | None:
    if not isinstance(payload, dict) or not isinstance(payload.get("skills"), list):
        return None
    entries: list[dict[str, object]] = []
    seen: set[str] = set()
    for raw in payload["skills"]:
        if not isinstance(raw, dict):
            return None
        name = raw.get("name")
        description = raw.get("description")
        tools_value = raw.get("tools", [])
        if not isinstance(name, str) or not isinstance(description, str) or not isinstance(tools_value, list):
            return None
        when_to_use = raw.get("whenToUse", "")
        source = raw.get("source", "")
        provider = raw.get("provider", "")
        path = raw.get("path", "")
        resource_base = raw.get("resourceBase", "")
        invocation_raw = raw.get("invocation", {})
        if not all(isinstance(value, str) for value in (when_to_use, source, provider, path, resource_base)):
            return None
        if not isinstance(invocation_raw, dict):
            return None
        model_invocable = invocation_raw.get("modelInvocable", True)
        user_invocable = invocation_raw.get("userInvocable", True)
        if not isinstance(model_invocable, bool) or not isinstance(user_invocable, bool):
            return None
        normalized_name = name.strip()
        normalized_description = description.strip()
        tools: list[str] = []
        for value in tools_value:
            if not isinstance(value, str):
                return None
            tools.append(value.strip())
        try:
            _validate_skill_metadata(normalized_name, normalized_description, tools)
        except ValueError:
            return None
        if normalized_name in seen:
            return None
        seen.add(normalized_name)
        entries.append({
            "name": normalized_name,
            "description": normalized_description,
            "tools": tools,
            "when_to_use": when_to_use.strip(),
            "source": source.strip(),
            "provider": provider.strip(),
            "path": path.strip(),
            "resource_base": resource_base.strip(),
            "invocation": InvocationPolicy(model_invocable=model_invocable, user_invocable=user_invocable),
        })
    return entries
