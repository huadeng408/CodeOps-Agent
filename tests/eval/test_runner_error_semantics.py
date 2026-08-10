"""``error`` must mean an error, not "the model kept using tools".

``ConversationRunner`` emits ``_done(True)`` in exactly two places: when the
model returns a response with no tool calls, and in the fallback path.  Every
other exit -- tool-round-limit exhaustion included -- emits ``_done(False)``.
So ``done.success`` answers *"did the model end its own turn cleanly?"*, not
*"did the task succeed?"*.

The driver recorded that as ``error``, which produced artifacts that
contradicted themselves.  The real H5 run
(``eval_results/h5-full-20260810/swebench-deepseek-v4-pro-3b0569f2``) carried::

    error    = 'runner completed with done.success=False'
    resolved = True          # official scorer, tests actually ran
    ok       = 1

and the patch in that same row was the correct 506-byte astropy fix.

It also corrupted a scorer rather than merely looking odd:
``eval/swebench_work/run_swebench_honest_10.py`` counts

    resolved = sum(1 for r in results if r.get("model_patch") and not r.get("error"))

so a correct patch was tallied as **unresolved**, and line 257 labelled it
``ERROR``.  A field name that lies is not a cosmetic problem.

The rule pinned here: report an error only when the runner ended without
success *and* left nothing usable behind.
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))


def _error_for(success: bool, patch: str, answer: str) -> str:
    """The shipped rule, mirrored from eval/driver_headless.py.

    Kept in step with the source by ``test_source_implements_this_rule`` below,
    which asserts the real module contains the same predicate -- so this helper
    cannot drift into testing itself.
    """
    produced_output = bool(patch.strip() or answer.strip())
    if success or produced_output:
        return ""
    return "runner ended without success and produced no patch or answer"


REAL_H5_PATCH = (
    "diff --git a/astropy/modeling/separable.py b/astropy/modeling/separable.py\n"
    "@@ -242,7 +242,7 @@ def _cstack(left, right):\n"
    "-        cright[-right.shape[0]:, -right.shape[1]:] = 1\n"
    "+        cright[-right.shape[0]:, -right.shape[1]:] = right\n"
)


class TestNoFalseError:
    def test_correct_patch_with_done_false_is_not_an_error(self):
        """The exact H5 case: right answer, done(False), must not say error."""
        assert _error_for(success=False, patch=REAL_H5_PATCH, answer="") == ""

    def test_answer_only_with_done_false_is_not_an_error(self):
        """Function-level tasks produce an answer, not a patch."""
        assert _error_for(success=False, patch="", answer="def f(): return 1") == ""

    def test_clean_finish_is_never_an_error(self):
        assert _error_for(success=True, patch="", answer="") == ""

    def test_clean_finish_with_output_is_never_an_error(self):
        assert _error_for(success=True, patch=REAL_H5_PATCH, answer="text") == ""


class TestRealErrorStillReported:
    def test_no_output_and_no_success_is_an_error(self):
        """Fail-closed: nothing produced and no clean finish is a real fault."""
        assert _error_for(success=False, patch="", answer="") != ""

    def test_whitespace_only_output_does_not_suppress_the_error(self):
        """A blank patch must not launder a failure into a success."""
        assert _error_for(success=False, patch="   \n", answer="\t\n") != ""

    def test_error_text_names_both_conditions(self):
        msg = _error_for(success=False, patch="", answer="")
        assert "without success" in msg
        assert "no patch or answer" in msg


class TestScorerNoLongerMiscounts:
    """The downstream consequence, expressed as the scorer's own predicate."""

    @staticmethod
    def _honest_10_resolved(rows: list[dict]) -> int:
        # Verbatim shape of run_swebench_honest_10.py:246.
        return sum(1 for r in rows if r.get("model_patch") and not r.get("error"))

    def test_correct_patch_now_counts_as_resolved(self):
        row = {
            "model_patch": REAL_H5_PATCH,
            "error": _error_for(success=False, patch=REAL_H5_PATCH, answer=""),
        }
        assert self._honest_10_resolved([row]) == 1

    def test_old_behaviour_would_have_miscounted_it(self):
        """Guard against silent regression: the old string breaks the count."""
        row = {
            "model_patch": REAL_H5_PATCH,
            "error": "runner completed with done.success=False",
        }
        assert self._honest_10_resolved([row]) == 0

    def test_empty_patch_run_is_not_credited(self):
        row = {"model_patch": "", "error": _error_for(False, "", "")}
        assert self._honest_10_resolved([row]) == 0


def test_source_implements_this_rule():
    """Pin the helper to the shipped source so this file cannot drift."""
    src = (PROJECT_ROOT / "eval" / "driver_headless.py").read_text(encoding="utf-8")
    assert "produced_output = bool(model_patch.strip() or final_text.strip())" in src, (
        "driver no longer computes produced_output as this test assumes"
    )
    assert "if success or produced_output:" in src
    assert "runner ended without success and produced no patch or answer" in src


def test_old_misleading_string_is_gone():
    src = (PROJECT_ROOT / "eval" / "driver_headless.py").read_text(encoding="utf-8")
    assignments = [
        line for line in src.splitlines()
        if "error=" in line and "done.success=False" in line
    ]
    assert not assignments, (
        "the driver still assigns the old self-contradicting error string: "
        f"{assignments}"
    )
