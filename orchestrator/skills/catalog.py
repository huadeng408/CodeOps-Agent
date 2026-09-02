"""Stable built-in Skill metadata shared by the Python orchestrator.

The Go Harness exposes the same names through its manifest. Keeping a small
Python-side catalog means a standalone orchestrator is useful before the Go
process has generated ``.agent/skills.json``.
"""

from __future__ import annotations

from typing import Final

SkillSpec = tuple[str, str, tuple[str, ...]]


_GOAL_SKILL_SPECS: Final[tuple[SkillSpec, ...]] = (
    ("inspect", "inspect repository structure and relevant files", ("Read", "Glob", "Grep")),
    ("task-decomposition", "split a complex request into testable work units", ("Read", "Write", "Grep")),
    ("architecture", "map boundaries and dependencies before a change", ("Read", "Grep")),
    ("api-design", "design a stable API contract", ("Read", "Write", "Grep")),
    ("data-model", "review persistence schemas and domain models", ("Read", "Grep", "Git")),
    ("dependency-audit", "audit dependencies and lockfile provenance", ("Read", "Grep", "Git")),
    ("debug", "diagnose a reproducible failure", ("Read", "Grep", "Bash")),
    ("performance", "profile a slow path and identify bottlenecks", ("Read", "Bash", "Grep")),
    ("concurrency", "review concurrent execution and cancellation", ("Read", "Grep", "Bash")),
    ("implementation", "implement a bounded repository change", ("Read", "Write", "Bash")),
    ("refactor", "refactor without changing observable behavior", ("Read", "Write", "Grep")),
    ("migration", "plan and execute a backward-compatible migration", ("Read", "Write", "Bash", "Git")),
    ("error-handling", "harden errors and failure semantics", ("Read", "Write", "Grep")),
    ("serialization", "validate structured serialization contracts", ("Read", "Grep", "Bash")),
    ("parser", "review parser boundaries and provenance", ("Read", "Grep", "Bash")),
    ("cli", "improve a command-line workflow", ("Read", "Write", "Bash")),
    ("tests", "add focused behavior tests", ("Read", "Write", "Bash")),
    ("integration-test", "build an integration test across module boundaries", ("Read", "Write", "Bash")),
    ("regression", "capture a regression with a durable test", ("Read", "Write", "Grep")),
    ("fuzz", "probe malformed and adversarial inputs", ("Read", "Bash", "Grep")),
    ("benchmark", "run a fixed-budget performance benchmark", ("Read", "Bash")),
    ("lint", "run and fix repository lint checks", ("Read", "Bash", "Write")),
    ("type-check", "run static type and compile checks", ("Read", "Bash")),
    ("release", "prepare a reproducible release candidate", ("Read", "Git", "Bash")),
    ("ci", "review continuous integration coverage", ("Read", "Grep", "Git")),
    ("docker", "diagnose and validate Docker execution", ("Read", "Bash", "Grep")),
    ("wsl", "diagnose Windows and WSL2 integration", ("Read", "Bash", "Grep")),
    ("sandbox", "review sandbox policy and isolation", ("Read", "Grep", "Bash")),
    ("mcp", "review MCP server lifecycle and tool contracts", ("Read", "Grep", "Bash")),
    ("observability", "add auditable metrics and logs", ("Read", "Write", "Grep")),
    ("trace", "validate OpenTelemetry trace topology", ("Read", "Bash", "Grep")),
    ("security", "inspect security boundaries and secrets", ("Read", "Grep", "Bash")),
    ("secrets", "scan changes without exposing credentials", ("Git", "Grep", "Bash")),
    ("incident", "triage a production-like incident", ("Read", "Grep", "Bash")),
    ("rollback", "plan a reversible rollback", ("Read", "Git", "Bash")),
    ("git-history", "inspect history and change provenance", ("Git", "Read", "Grep")),
    ("documentation", "write concise repository documentation", ("Read", "Write", "Grep")),
    ("review", "review changes for correctness and risk", ("Read", "Git", "Grep")),
    ("commit", "draft a conventional commit message from the current diff", ("Git",)),
    ("init", "bootstrap project instructions and baseline structure", ("Read", "Glob", "Grep")),
)


def goal_skill_specs() -> tuple[SkillSpec, ...]:
    """Return the immutable built-in catalog in deterministic order."""

    return _GOAL_SKILL_SPECS
