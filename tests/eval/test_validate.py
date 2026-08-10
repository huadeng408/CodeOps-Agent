"""Tests for patch validation.

The design constraint under test is that this module reports *evidence with a
direction*, never a verdict it cannot support. So there are as many tests
asserting that a check stays weak as there are asserting that it fires.
"""

from __future__ import annotations

import inspect
from pathlib import Path

import pytest

from eval.harness import validate as V
from eval.harness.validate import Evidence


DIFF = """\
diff --git a/pkg/table.py b/pkg/table.py
--- a/pkg/table.py
+++ b/pkg/table.py
@@ -1,3 +1,4 @@
 def add_column(data):
+    data = data.view(NdarrayMixin)
     return data
"""

TEST_ONLY_DIFF = """\
diff --git a/pkg/tests/test_table.py b/pkg/tests/test_table.py
--- a/pkg/tests/test_table.py
+++ b/pkg/tests/test_table.py
@@ -1,2 +1,2 @@
-assert broken()
+assert True
"""


# --------------------------------------------------------------- patched paths


def test_patched_paths_extracts_targets():
    assert V.patched_paths(DIFF) == ["pkg/table.py"]


def test_patched_paths_on_empty_diff():
    assert V.patched_paths("") == []
    assert V.patched_paths(None) == []  # type: ignore[arg-type]


def test_patched_paths_handles_multiple_files():
    combined = DIFF + TEST_ONLY_DIFF
    assert set(V.patched_paths(combined)) == {"pkg/table.py", "pkg/tests/test_table.py"}


# ------------------------------------------------------------------ patch shape


def test_empty_patch_is_disqualifying():
    checks = V.check_patch_shape("")
    assert checks[0].evidence is Evidence.DISQUALIFYING
    assert checks[0].blocks_submission
    assert "git diff" in checks[0].detail


def test_whitespace_only_patch_is_disqualifying():
    assert V.check_patch_shape("   \n\t\n")[0].evidence is Evidence.DISQUALIFYING


def test_non_diff_text_is_disqualifying():
    """The baseline's failure mode: a code block instead of a diff."""
    checks = V.check_patch_shape("def add_column(data):\n    return data.view(X)\n")
    assert any(c.evidence is Evidence.DISQUALIFYING for c in checks)


def test_real_patch_is_only_weak_positive():
    """A patch that applies is not a patch that works."""
    checks = V.check_patch_shape(DIFF)
    assert all(c.evidence is Evidence.WEAK_POSITIVE for c in checks)
    assert not any(c.blocks_submission for c in checks)


def test_test_only_patch_is_disqualifying():
    checks = V.check_patch_shape(TEST_ONLY_DIFF)
    offending = [c for c in checks if c.name == "edits_source_not_tests"]
    assert offending and offending[0].evidence is Evidence.DISQUALIFYING


def test_patch_touching_tests_and_source_is_a_regression_signal():
    checks = V.check_patch_shape(DIFF + TEST_ONLY_DIFF)
    offending = [c for c in checks if c.name == "edits_source_not_tests"]
    assert offending and offending[0].evidence is Evidence.REGRESSION
    assert offending[0].blocks_submission


# ----------------------------------------------------------------------- syntax


def test_syntax_check_passes_for_valid_file(tmp_path: Path):
    (tmp_path / "pkg").mkdir()
    (tmp_path / "pkg" / "table.py").write_text("def f():\n    return 1\n", encoding="utf-8")
    checks = V.check_syntax(tmp_path, DIFF)
    assert checks[0].evidence is Evidence.WEAK_POSITIVE


def test_syntax_check_disqualifies_broken_file(tmp_path: Path):
    (tmp_path / "pkg").mkdir()
    (tmp_path / "pkg" / "table.py").write_text("def f(:\n", encoding="utf-8")
    checks = V.check_syntax(tmp_path, DIFF)
    assert checks[0].evidence is Evidence.DISQUALIFYING
    assert "line" in checks[0].detail


def test_missing_file_is_no_evidence_not_failure(tmp_path: Path):
    checks = V.check_syntax(tmp_path, DIFF)
    assert checks[0].evidence is Evidence.NO_EVIDENCE


def test_non_python_patch_yields_no_evidence(tmp_path: Path):
    diff = DIFF.replace("pkg/table.py", "README.rst")
    checks = V.check_syntax(tmp_path, diff)
    assert checks[0].evidence is Evidence.NO_EVIDENCE


# ------------------------------------------------------------------- full report


def test_report_disqualifies_empty_patch(tmp_path: Path):
    report = V.validate_patch("", tmp_path)
    assert report.disqualified
    assert "disqualified" in report.summary


def test_report_short_circuits_after_disqualification(tmp_path: Path):
    """No point running syntax checks on a patch that cannot pass."""
    report = V.validate_patch("", tmp_path)
    assert all(c.name != "syntax" for c in report.checks)


def test_report_never_claims_the_patch_is_valid(tmp_path: Path):
    (tmp_path / "pkg").mkdir()
    (tmp_path / "pkg" / "table.py").write_text("def f():\n    return 1\n", encoding="utf-8")
    report = V.validate_patch(DIFF, tmp_path)
    assert not report.disqualified
    assert "not disqualified" in report.summary
    assert "valid" not in report.summary


def test_report_with_no_checks_says_so():
    assert V.ValidationReport().summary == "no checks ran"


def test_validate_never_raises_on_hostile_input(tmp_path: Path):
    for bad in ("", "\x00\x01", "+++ b/\n", "diff --git", "@@ -1 +1 @@"):
        report = V.validate_patch(bad, tmp_path)
        assert isinstance(report, V.ValidationReport)


def test_validate_never_raises_for_missing_repo():
    report = V.validate_patch(DIFF, Path("does-not-exist-anywhere"))
    assert isinstance(report, V.ValidationReport)


# ----------------------------------------------------------------- apply check


def test_applies_cleanly_reports_no_evidence_outside_a_repo(tmp_path: Path):
    check = V.check_applies_cleanly(tmp_path, DIFF)
    assert check.evidence is Evidence.NO_EVIDENCE


def test_applies_cleanly_disqualifies_empty(tmp_path: Path):
    assert V.check_applies_cleanly(tmp_path, "").evidence is Evidence.DISQUALIFYING


# ------------------------------------------------------- anti-leakage guardrail


def test_validate_source_never_mentions_the_grading_criteria():
    source = inspect.getsource(V)
    body = source.split('"""', 2)[-1]  # the docstring explains why they are absent
    for field in ("test_patch", "FAIL_TO_PASS", "PASS_TO_PASS", "hints_text", "gold_patch"):
        assert field not in body, f"{field} must not be reachable from the validator"


def test_evidence_levels_are_distinct():
    """A vacuous enum would make every direction assertion above meaningless."""
    assert len({e.value for e in Evidence}) == 4
    assert Check_blocking() == {Evidence.DISQUALIFYING, Evidence.REGRESSION}


def Check_blocking() -> set[Evidence]:
    return {
        e
        for e in Evidence
        if V.Check("x", e).blocks_submission
    }
