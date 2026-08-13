from __future__ import annotations

import tomllib
from pathlib import Path


def test_rag_extra_declares_runtime_imports() -> None:
    project = tomllib.loads(
        (Path(__file__).parents[1] / "pyproject.toml").read_text(encoding="utf-8")
    )
    dependencies = project["project"]["optional-dependencies"]["rag"]

    required_prefixes = (
        "fastapi",
        "httpx",
        "langchain-core",
        "langchain-openai",
        "langchain-text-splitters",
        "langgraph",
        "pydantic",
        "typing-extensions",
        "uvicorn",
    )
    for prefix in required_prefixes:
        assert any(item.lower().startswith(prefix) for item in dependencies), prefix


def test_visual_eval_extra_declares_late_interaction_runtime_imports() -> None:
    project = tomllib.loads(
        (Path(__file__).parents[1] / "pyproject.toml").read_text(encoding="utf-8")
    )
    dependencies = project["project"]["optional-dependencies"]["visual-eval"]

    for prefix in ("colpali-engine", "peft", "pillow", "datasets"):
        assert any(item.lower().startswith(prefix) for item in dependencies), prefix
