from __future__ import annotations

from .manager import Skill


def build_skill() -> Skill:
    return Skill(
        name="security",
        description="inspect risky commands and operations",
        prompt="Analyze commands for destructive or unsafe behavior before execution.",
        tools=["Bash", "Git", "Read"],
    )
