from __future__ import annotations

from .manager import Skill


def build_skill() -> Skill:
    return Skill(
        name="security",
        description="inspect security boundaries and secrets",
        prompt="Inspect trust boundaries, authorization, secret handling, injection risks, and fail-closed paths.",
        tools=["Read", "Grep", "Bash"],
    )
