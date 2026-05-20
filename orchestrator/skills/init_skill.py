from __future__ import annotations

from .manager import Skill


def build_skill() -> Skill:
    return Skill(
        name="init",
        description="bootstrap project instructions and baseline structure",
        prompt="Initialize the repository skeleton and explain the active structure.",
        tools=["Read", "Glob", "Grep"],
    )
