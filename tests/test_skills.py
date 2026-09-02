from __future__ import annotations

import json

from orchestrator.skills.manager import SkillManager


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
