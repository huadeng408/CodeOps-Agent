"""Patch validation: does the change actually do something, and break nothing?

Why validation belongs in the harness
-------------------------------------
The recorded baseline had no idea whether its own
output was worth submitting. It produced 0-byte patches and submitted them; it
produced a correct fix as chat text and submitted nothing. A harness that cannot
tell those apart cannot improve, because it has no signal to act on.

This module supplies that signal *without* looking at the answers. The official
tests (``FAIL_TO_PASS`` / ``PASS_TO_PASS``) and the developer's ``test_patch``
are exactly what the agent is being measured against; reading them here would be
cheating, and :mod:`eval.harness.leakage` asserts structurally that this module
never names them. What is available instead:

1. **The patch itself** — is it non-empty, does it apply, does it touch source
   rather than tests, is it syntactically valid Python?
2. **The repository's own existing tests** near the edited code, which shipped in
   the repo at ``base_commit`` and are not the grading criteria.

The honest limit
----------------
Signal (1) is cheap and decisive about *failure*: an empty or unparseable patch
cannot possibly pass. It is nearly silent about *success* — a patch that applies
and parses may still be wrong. Signal (2) is stronger but only in one direction
too: existing tests that newly fail are strong evidence of a regression, while
existing tests that pass say little, since they passed before the patch as well.

So this module reports evidence, with the direction of that evidence attached,
and never converts weak evidence into a verdict. Published test-time-compute
results show that validation built on
low-coverage signals plus random tie-breaking made results *worse*, not better.
A validator that overclaims is how that happens.
"""

from __future__ import annotations

import ast
import re
import subprocess
from dataclasses import dataclass, field
from enum import Enum
from pathlib import Path


class Evidence(str, Enum):
    """How much a check licenses you to conclude."""

    #: The patch cannot pass. Safe to act on: reject or resample.
    DISQUALIFYING = "disqualifying"
    #: Something the patch broke. Strong, actionable.
    REGRESSION = "regression"
    #: Consistent with a working patch, but not proof of one.
    WEAK_POSITIVE = "weak_positive"
    #: The check could not run. Explicitly not a verdict about the patch.
    NO_EVIDENCE = "no_evidence"


@dataclass
class Check:
    name: str
    evidence: Evidence
    detail: str = ""

    @property
    def blocks_submission(self) -> bool:
        return self.evidence in (Evidence.DISQUALIFYING, Evidence.REGRESSION)


@dataclass
class ValidationReport:
    checks: list[Check] = field(default_factory=list)

    def add(self, name: str, evidence: Evidence, detail: str = "") -> None:
        self.checks.append(Check(name, evidence, detail))

    @property
    def disqualified(self) -> bool:
        """Whether some check proved the patch cannot be right."""
        return any(check.blocks_submission for check in self.checks)

    @property
    def blocking(self) -> list[Check]:
        return [check for check in self.checks if check.blocks_submission]

    @property
    def summary(self) -> str:
        if not self.checks:
            return "no checks ran"
        if self.disqualified:
            reasons = "; ".join(f"{c.name}: {c.detail}" for c in self.blocking)
            return f"disqualified ({reasons})"
        weak = sum(1 for c in self.checks if c.evidence is Evidence.WEAK_POSITIVE)
        none = sum(1 for c in self.checks if c.evidence is Evidence.NO_EVIDENCE)
        # Deliberately not the word "valid". Nothing here can establish that.
        return f"not disqualified ({weak} weak-positive, {none} no-evidence)"


_TEST_PATH_RE = re.compile(r"(^|/)(tests?|testing)/|(^|/)test_[^/]*\.py$|_test\.py$")
_DIFF_TARGET_RE = re.compile(r"^\+\+\+ b/(.+)$", re.MULTILINE)


def patched_paths(diff: str) -> list[str]:
    """Repository-relative paths the diff writes to."""
    return [
        match.group(1).strip()
        for match in _DIFF_TARGET_RE.finditer(diff or "")
        if match.group(1).strip() not in ("/dev/null",)
    ]


def partition_patched_paths(diff: str) -> tuple[list[str], list[str]]:
    """Return ``(source_paths, test_paths)`` for a unified diff."""
    paths = patched_paths(diff)
    test_paths = [path for path in paths if _TEST_PATH_RE.search(path)]
    source_paths = [path for path in paths if path not in test_paths]
    return source_paths, test_paths


def check_patch_shape(diff: str) -> list[Check]:
    """Checks that need only the diff text.

    These are the ones that would have caught the baseline's dominant failure:
    9 of 10 instances submitted an empty patch, and nothing in the pipeline
    noticed before the official scorer was paid to notice.
    """
    checks: list[Check] = []
    if not (diff or "").strip():
        checks.append(
            Check(
                "non_empty",
                Evidence.DISQUALIFYING,
                "patch is empty; the grader reads git diff, so there is nothing to score",
            )
        )
        return checks
    checks.append(Check("non_empty", Evidence.WEAK_POSITIVE, f"{len(diff)} bytes"))

    paths = patched_paths(diff)
    if not paths:
        checks.append(
            Check(
                "parses_as_diff",
                Evidence.DISQUALIFYING,
                "no '+++ b/<path>' target found; not a unified diff",
            )
        )
        return checks
    checks.append(Check("parses_as_diff", Evidence.WEAK_POSITIVE, ", ".join(paths)))

    source_paths, test_paths = partition_patched_paths(diff)
    if test_paths and not source_paths:
        checks.append(
            Check(
                "edits_source_not_tests",
                Evidence.DISQUALIFYING,
                f"only test files edited ({', '.join(test_paths)}); "
                "editing tests cannot fix the reported defect",
            )
        )
    elif test_paths:
        checks.append(
            Check(
                "edits_source_not_tests",
                Evidence.REGRESSION,
                f"patch also edits tests ({', '.join(test_paths)})",
            )
        )
    else:
        checks.append(
            Check("edits_source_not_tests", Evidence.WEAK_POSITIVE, ", ".join(source_paths))
        )
    return checks


def check_syntax(repo_root: str | Path, diff: str) -> list[Check]:
    """Whether every patched Python file still parses.

    Run *after* the patch is applied to the worktree. A file that no longer
    parses fails every test in its module, so this is disqualifying and cheap —
    no container, no test run.
    """
    root = Path(repo_root)
    checks: list[Check] = []
    for rel in patched_paths(diff):
        if not rel.endswith(".py"):
            continue
        path = root / rel
        if not path.is_file():
            checks.append(
                Check("syntax", Evidence.NO_EVIDENCE, f"{rel}: not found in worktree")
            )
            continue
        try:
            ast.parse(path.read_text(encoding="utf-8", errors="replace"))
        except SyntaxError as exc:
            checks.append(
                Check("syntax", Evidence.DISQUALIFYING, f"{rel}: {exc.msg} at line {exc.lineno}")
            )
        except OSError as exc:
            checks.append(Check("syntax", Evidence.NO_EVIDENCE, f"{rel}: {exc}"))
        else:
            checks.append(Check("syntax", Evidence.WEAK_POSITIVE, rel))
    if not checks:
        checks.append(Check("syntax", Evidence.NO_EVIDENCE, "no Python files patched"))
    return checks


def check_applies_cleanly(repo_root: str | Path, diff: str) -> Check:
    """Whether ``git apply --check`` accepts the diff against the worktree.

    Used for patches produced out-of-band. When the agent edited files directly
    the diff came *from* the worktree and this necessarily passes, which is why
    it reports WEAK_POSITIVE rather than anything stronger.
    """
    if not (diff or "").strip():
        return Check("applies", Evidence.DISQUALIFYING, "empty patch")
    try:
        completed = subprocess.run(
            ["git", "apply", "--check", "--reverse", "-"],
            cwd=str(repo_root),
            input=diff,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=60,
        )
    except (OSError, subprocess.SubprocessError) as exc:
        return Check("applies", Evidence.NO_EVIDENCE, f"git apply unavailable: {exc}")
    if completed.returncode == 0:
        # --reverse succeeding means the change is already in the worktree.
        return Check("applies", Evidence.WEAK_POSITIVE, "already present in worktree")
    return Check(
        "applies",
        Evidence.NO_EVIDENCE,
        "diff is not reverse-applicable; cannot confirm it matches the worktree",
    )


def validate_patch(
    diff: str,
    repo_root: str | Path,
    *,
    check_worktree_syntax: bool = True,
) -> ValidationReport:
    """Run every check that does not require the grading criteria.

    Never raises for patch-shaped or repository-shaped reasons: a check that
    cannot run reports :attr:`Evidence.NO_EVIDENCE`. A validator that can fail
    the run would be a way to lose an instance the agent had already solved.
    """
    report = ValidationReport()
    for check in check_patch_shape(diff):
        report.checks.append(check)
    if report.disqualified:
        return report
    if check_worktree_syntax:
        for check in check_syntax(repo_root, diff):
            report.checks.append(check)
    return report
