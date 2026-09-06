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
