from __future__ import annotations

import json

from orchestrator.skills.manager import SkillManager


def test_default_skill_manager_exposes_goal_catalog_without_project_manifest() -> None:
    manager = SkillManager()

    skills = manager.list()
    assert len(skills) >= 40
    assert {skill.name for skill in skills} >= {
        "inspect",
        "task-decomposition",
        "integration-test",
        "release",
    }
    assert all(skill.description and skill.prompt and skill.tools for skill in skills)


def test_skill_manager_mirrors_harness_manifest_without_loading_prompt_bodies(tmp_path) -> None:
    agent_dir = tmp_path / ".agent"
    agent_dir.mkdir()
    manifest = agent_dir / "skills.json"
    manifest.write_text(
        json.dumps(
            {
                "skills": [
                    {
                        "name": "release",
                        "description": "Prepare a release.",
                        "tools": ["Git", "Bash"],
                    }
                ]
            }
        ),
        encoding="utf-8",
    )

    manager = SkillManager(tmp_path)
    release = manager.get("release")
    assert release is not None
    assert release.description == "Prepare a release."
    assert release.tools == ["Git", "Bash"]
    assert release.prompt == ""

    manifest.write_text(
        json.dumps({"skills": [{"name": "deploy", "description": "Deploy safely."}]}),
        encoding="utf-8",
    )
    assert manager.get("release") is None
    deploy = manager.get("deploy")
    assert deploy is not None
    assert deploy.description == "Deploy safely."


def test_skill_manifest_rejects_malformed_metadata_without_partial_refresh(tmp_path) -> None:
    agent_dir = tmp_path / ".agent"
    agent_dir.mkdir()
    manifest = agent_dir / "skills.json"
    manifest.write_text(
        json.dumps(
            {
                "skills": [
                    {
                        "name": "release",
                        "description": "Prepare a release.",
                        "tools": ["Git"],
                    }
                ]
            }
        ),
        encoding="utf-8",
    )
    manager = SkillManager(tmp_path)
    assert manager.get("release") is not None

    manifest.write_text(
        json.dumps(
            {
                "skills": [
                    {"name": ["not-a-name"], "description": "coercion must fail"},
                    {"name": "Bad_Name", "description": "invalid name"},
                    {"name": "broken-tools", "description": "invalid tools", "tools": ["Git", 7]},
                ]
            }
        ),
        encoding="utf-8",
    )
    # A malformed replacement is fail-closed: the last good catalog remains
    # available and no malformed entry is partially applied.
    assert manager.get("release") is not None
    assert manager.get("not-a-name") is None
    assert manager.get("Bad_Name") is None
    assert manager.get("broken-tools") is None


def test_skill_manifest_rejects_duplicate_names_and_control_character_tools(tmp_path) -> None:
    agent_dir = tmp_path / ".agent"
    agent_dir.mkdir()
    manifest = agent_dir / "skills.json"
    manifest.write_text(
        json.dumps(
            {
                "skills": [
                    {"name": "custom-skill", "description": "one"},
                    {"name": "custom-skill", "description": "two"},
                ]
            }
        ),
        encoding="utf-8",
    )
    manager = SkillManager(tmp_path)
    assert manager.get("custom-skill") is None

    manifest.write_text(
        json.dumps(
            {
                "skills": [
                    {"name": "safe-skill", "description": "safe", "tools": ["Git\nBash"]}
                ]
            }
        ),
        encoding="utf-8",
    )
    assert manager.get("safe-skill") is None

def test_skill_manifest_mirrors_routing_policy_and_snapshot_revision(tmp_path) -> None:
    agent_dir = tmp_path / ".agent"
    agent_dir.mkdir()
    manifest = agent_dir / "skills.json"
    manifest.write_text(
        json.dumps({"skills": [{
            "name": "model-only",
            "description": "Model-only skill",
            "whenToUse": "Use for context routing",
            "invocation": {"modelInvocable": True, "userInvocable": False},
            "source": "project-dsh",
            "provider": "filesystem",
            "path": "C:/private/SKILL.md",
        }]}),
        encoding="utf-8",
    )
    manager = SkillManager(tmp_path)
    skill = manager.get("model-only")
    assert skill is not None
    assert skill.when_to_use == "Use for context routing"
    assert skill.invocation.model_invocable is True
    assert skill.invocation.user_invocable is False
    assert skill.source == "project-dsh"
    assert skill.provider == "filesystem"
    assert skill.path == "C:/private/SKILL.md"
    first = manager.snapshot()
    assert first.complete is True and first.revision > 0
    manifest.write_text("{bad", encoding="utf-8")
    second = manager.snapshot()
    assert second.complete is False
    assert second.count == first.count and second.revision == first.revision

def test_skill_manifest_missing_retains_last_good_catalog(tmp_path) -> None:
    agent_dir = tmp_path / ".agent"
    agent_dir.mkdir()
    manifest = agent_dir / "skills.json"
    manifest.write_text(
        json.dumps({"skills": [{"name": "custom-skill", "description": "Custom"}]}),
        encoding="utf-8",
    )
    manager = SkillManager(tmp_path)
    assert manager.get("custom-skill") is not None
    first = manager.snapshot()
    manifest.unlink()
    assert manager.get("custom-skill") is not None
    second = manager.snapshot()
    assert second.complete is False
    assert second.count == first.count and second.revision == first.revision


def _write_fs_skill(root, name, description, *, body="body", disable_model=False, user_invocable=True):
    skill_dir = root / name
    skill_dir.mkdir(parents=True, exist_ok=True)
    skill_dir.joinpath("SKILL.md").write_text(
        "---\n"
        f"name: {name}\n"
        f"description: {description}\n"
        f"disable-model-invocation: {'true' if disable_model else 'false'}\n"
        f"user-invocable: {'true' if user_invocable else 'false'}\n"
        "tools: [Read]\n"
        "---\n"
        f"{body}\n",
        encoding="utf-8",
    )


def test_skill_manager_discovers_layered_filesystem_skills_with_stable_precedence(tmp_path) -> None:
    project_dsh = tmp_path / ".dsh" / "skills"
    project_agents = tmp_path / ".agents" / "skills"
    legacy = tmp_path / ".agent" / "skills"
    custom = tmp_path / "custom-skills"
    _write_fs_skill(project_dsh, "same", "project dsh", body="dsh body")
    _write_fs_skill(project_agents, "same", "project agents", body="agents body")
    _write_fs_skill(legacy, "same", "legacy", body="legacy body")
    _write_fs_skill(custom, "same", "custom", body="custom body")
    _write_fs_skill(project_agents, "agents-only", "agents only")
    custom.joinpath("flat.md").write_text(
        "---\nname: flat\ndescription: flat skill\n---\nflat body\n",
        encoding="utf-8",
    )

    manager = SkillManager(tmp_path)
    manager.discover(
        directories=[custom],
        project_dsh_dir=project_dsh,
        project_agents_dir=project_agents,
        project_dir=legacy,
    )

    same = manager.get("same")
    assert same is not None
    assert same.description == "project dsh"
    assert same.prompt == ""
    assert same.source == "project-dsh"
    assert manager.get("agents-only") is not None
    assert manager.get("flat") is not None
    loaded = manager.load("same")
    assert loaded.prompt == "dsh body"


def test_skill_manager_keeps_last_good_filesystem_catalog_on_refresh_failure(tmp_path) -> None:
    root = tmp_path / ".dsh" / "skills"
    _write_fs_skill(root, "durable", "durable skill")
    manager = SkillManager(tmp_path)
    manager.discover(project_dsh_dir=root)
    first = manager.snapshot()
    assert manager.get("durable") is not None

    root.joinpath("broken").mkdir()
    root.joinpath("broken", "SKILL.md").write_text("not frontmatter", encoding="utf-8")
    manager.discover(project_dsh_dir=root)
    second = manager.snapshot()
    assert second.complete is False
    assert second.count == first.count
    assert second.revision == first.revision
    assert manager.get("durable") is not None
    assert manager.get("broken") is None


def test_skill_manager_filters_model_and_user_invocation_views(tmp_path) -> None:
    root = tmp_path / ".agents" / "skills"
    _write_fs_skill(root, "internal-only", "runtime", disable_model=True, user_invocable=False)
    _write_fs_skill(root, "user-only", "manual", disable_model=True, user_invocable=True)
    _write_fs_skill(root, "model-only", "automatic", disable_model=False, user_invocable=False)
    manager = SkillManager(tmp_path)
    manager.discover(project_agents_dir=root)

    assert {item.name for item in manager.list(for_model=True)} >= {"model-only"}
    assert "internal-only" not in {item.name for item in manager.list(for_model=True)}
    assert "user-only" not in {item.name for item in manager.list(for_model=True)}
    assert {item.name for item in manager.list(for_user=True)} >= {"user-only"}
    assert "internal-only" not in {item.name for item in manager.list(for_user=True)}


def test_skill_manager_reads_resources_without_path_traversal(tmp_path) -> None:
    root = tmp_path / ".dsh" / "skills"
    _write_fs_skill(root, "resource-skill", "resource skill")
    resource_dir = root / "resource-skill" / "resources"
    resource_dir.mkdir()
    resource_dir.joinpath("guide.txt").write_text("guide", encoding="utf-8")
    manager = SkillManager(tmp_path)
    manager.discover(project_dsh_dir=root)
    assert manager.read_resource("resource-skill", "resources/guide.txt") == b"guide"
    import pytest
    with pytest.raises(ValueError, match="escapes"):
        manager.read_resource("resource-skill", "../SKILL.md")
