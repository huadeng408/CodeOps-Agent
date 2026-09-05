from __future__ import annotations

import tomllib
from pathlib import Path


def test_core_orchestrator_dependencies_are_not_rag_optional() -> None:
    project = tomllib.loads(
        (Path(__file__).resolve().parents[1] / "pyproject.toml").read_text(
            encoding="utf-8"
        )
    )
    project_dependencies = set(project["project"]["dependencies"])
    rag_dependencies = set(project["project"]["optional-dependencies"]["rag"])

    assert "langgraph>=0.2,<1" in project_dependencies
    assert "langchain-core>=0.3,<1" in project_dependencies
    assert "langgraph>=0.2,<1" not in rag_dependencies
    assert "langchain-core>=0.3,<1" not in rag_dependencies
