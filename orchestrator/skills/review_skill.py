from __future__ import annotations

from .manager import Skill


def build_skill() -> Skill:
    return Skill(
        name="review",
        description="review changes for correctness and risk",
        prompt="Review the current change set and report concrete correctness, security, and regression risks first.",
        tools=["Read", "Git", "Grep"],
    )
