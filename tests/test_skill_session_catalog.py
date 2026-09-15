from pathlib import Path
from collections import OrderedDict
import threading

from orchestrator.runtime.tools import ToolRegistry
from orchestrator.skills.manager import SkillManager


def test_session_catalog_is_lazy_and_not_overridden_by_parent_manifest(tmp_path: Path) -> None:
    parent = tmp_path / "parent"
    child = tmp_path / "child"
    (parent / ".agent").mkdir(parents=True)
    (parent / ".agent" / "skills.json").write_text(
        '{"skills":[{"name":"parent-only","description":"Parent metadata"}]}',
        encoding="utf-8",
    )
    directory = child / ".agents" / "skills" / "inspect"
    directory.mkdir(parents=True)
    (directory / "SKILL.md").write_text(
        "---\nname: child-inspect\ndescription: Child metadata\n---\nBODY_NOT_DISCOVERED",
        encoding="utf-8",
    )
    skills = SkillManager(child)
    registry = ToolRegistry(str(parent), skills=skills)
    spec = registry.get("Skill")
    assert "child-inspect: Child metadata" in spec.description
    assert "parent-only" not in spec.description
    assert "BODY_NOT_DISCOVERED" not in str(registry.openai_schemas())
    assert skills.get("child-inspect").prompt == ""
    assert "BODY_NOT_DISCOVERED" in skills.load("child-inspect").prompt


def test_session_catalog_keeps_model_invocation_filter(tmp_path: Path) -> None:
    directory = tmp_path / ".agents" / "skills" / "manual"
    directory.mkdir(parents=True)
    (directory / "SKILL.md").write_text(
        "---\nname: manual-only\ndescription: Explicit only\ndisable-model-invocation: true\n---\nBody",
        encoding="utf-8",
    )
    skills = SkillManager(tmp_path)
    registry = ToolRegistry(str(tmp_path), skills=skills)
    assert "manual-only" not in registry.get("Skill").description
    assert any(item.name == "manual-only" for item in skills.list(for_user=True))


def test_child_runner_scopes_tools_extensions_and_skills_to_child_root(tmp_path: Path, monkeypatch) -> None:
    from orchestrator.server import OrchestratorServer, OrchestratorService, ServerConfig

    monkeypatch.setenv("OPENAI_API_KEY", "")
    parent = tmp_path / "parent"
    child = parent / "child"
    (parent / ".agent").mkdir(parents=True)
    (child / ".agent").mkdir(parents=True)
    (parent / ".agent" / "mcp-tools.json").write_text(
        '{"tools":[{"name":"parent-mcp","description":"Parent only"}]}',
        encoding="utf-8",
    )
    (child / ".agent" / "mcp-tools.json").write_text(
        '{"tools":[{"name":"child-mcp","description":"Child only"}]}',
        encoding="utf-8",
    )
    (parent / ".agent" / "extensions.json").write_text(
        '{"extensions":[{"id":"parent-ext","kind":"lsp","version":"v1",'
        '"description":"Parent extension","operations":["inspect"]}]}',
        encoding="utf-8",
    )
    (child / ".agent" / "extensions.json").write_text(
        '{"extensions":[{"id":"child-ext","kind":"lsp","version":"v1",'
        '"description":"Child extension","operations":["inspect"]}]}',
        encoding="utf-8",
    )
    skill_dir = child / ".agents" / "skills" / "inspect"
    skill_dir.mkdir(parents=True)
    (skill_dir / "SKILL.md").write_text(
        "---\nname: child-inspect\ndescription: Child metadata\n---\nCHILD_SKILL_BODY",
        encoding="utf-8",
    )

    app = OrchestratorServer(
        ServerConfig(project_root=str(parent), memory_dir=str(tmp_path / "memory"))
    )
    try:
        runner = OrchestratorService(app)._new_runner(
            working_dir=str(child), project_root=str(child)
        )
        schemas = runner.tool_registry.openai_schemas()
        names = {schema["function"]["name"] for schema in schemas}
        assert "child-mcp" in names
        assert "parent-mcp" not in names
        assert runner.project_root == str(child.resolve())
        assert runner.tool_registry._project_root == child.resolve()
        assert runner.extensions.resolve("child-ext") is not None
        assert runner.extensions.resolve("parent-ext") is None
        assert runner.skills.get("child-inspect").prompt == ""
        assert "CHILD_SKILL_BODY" not in str(schemas)
        assert "CHILD_SKILL_BODY" in runner.skills.load("child-inspect").prompt
    finally:
        app.close()


def test_last_good_catalog_survives_requests_without_sharing_loaded_bodies(tmp_path, monkeypatch):
    from orchestrator.server import OrchestratorServer

    monkeypatch.setattr(Path, "home", lambda: tmp_path / "unused-home")
    app = OrchestratorServer.__new__(OrchestratorServer)
    app._skill_catalogs = OrderedDict()
    app._skill_catalog_lock = threading.Lock()
    directory = tmp_path / ".agents" / "skills" / "inspect"
    directory.mkdir(parents=True)
    path = directory / "SKILL.md"
    path.write_text("---\nname: isolated\ndescription: Good metadata\n---\nBODY", encoding="utf-8")
    first = app.session_skills(str(tmp_path))
    first.load("isolated")
    second = app.session_skills(str(tmp_path))
    assert second.get("isolated").prompt == ""
    assert first is not second
    path.write_text("---\nname: isolated\ninvalid: [\n---\nBAD_BODY", encoding="utf-8")
    degraded = app.session_skills(str(tmp_path))
    assert degraded.get("isolated").description == "Good metadata"
    assert not degraded.snapshot().complete
    other = app.session_skills(str(tmp_path / "other"))
    assert other.get("isolated") is None
