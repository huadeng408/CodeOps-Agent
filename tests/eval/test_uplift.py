"""Tests for the optimized arm's configuration gate.

The property that matters most is the negative one: with the switch off, arm A
must get byte-identical prompts and the baseline turn budget. A paired
experiment whose control arm drifted is not a comparison.
"""

from __future__ import annotations

import inspect
import subprocess
from pathlib import Path

import pytest

from eval.harness import uplift


@pytest.fixture(autouse=True)
def _clean_env(monkeypatch):
    for name in (
        uplift.UPLIFT_ENV,
        "SWEBENCH_UPLIFT_LOCALIZATION",
        "SWEBENCH_UPLIFT_EDIT_MANDATE",
        "SWEBENCH_UPLIFT_VALIDATION",
        "SWEBENCH_UPLIFT_SELECTION",
        "SWEBENCH_UPLIFT_TOOL_ROUNDS",
    ):
        monkeypatch.delenv(name, raising=False)


@pytest.fixture()
def repo(tmp_path: Path) -> Path:
    (tmp_path / "pkg").mkdir()
    (tmp_path / "pkg" / "table.py").write_text(
        '''"""Table construction."""


def add_column(data):
    """Add a column, converting structured arrays to NdarrayMixin."""
    return data
''',
        encoding="utf-8",
    )
    return tmp_path


# ------------------------------------------------------------------ arm A: off


def test_disabled_by_default():
    config = uplift.uplift_config()
    assert config.enabled is False
    assert config.localization is False
    assert config.edit_mandate is False
    assert config.components == ()


def test_baseline_tool_rounds_when_disabled():
    assert uplift.uplift_config().tool_rounds == uplift.BASELINE_TOOL_ROUNDS == 8


def test_task_description_is_untouched_when_disabled(repo: Path):
    original = "# Bug\n\nadd_column converts structured arrays unexpectedly."
    assert uplift.augment_task_description(original, str(repo)) == original


# ------------------------------------------------------------------- arm B: on


def test_enabled_by_the_single_switch(monkeypatch):
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    config = uplift.uplift_config()
    assert config.enabled is True
    assert config.localization is True
    assert config.edit_mandate is True
    assert config.tool_rounds == uplift.UPLIFT_TOOL_ROUNDS


def test_enabled_raises_the_turn_budget(monkeypatch):
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    assert uplift.uplift_config().tool_rounds > uplift.BASELINE_TOOL_ROUNDS


def test_components_are_named_for_the_manifest(monkeypatch):
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    config = uplift.uplift_config()
    assert "localization" in config.components
    assert "edit_mandate" in config.components
    assert "tool_rounds" in config.components
    block = config.as_manifest_block()
    assert block["enabled"] is True
    assert block["env_var"] == uplift.UPLIFT_ENV
    assert block["tool_rounds"] == config.tool_rounds


def test_augmented_task_contains_localization_and_mandate(monkeypatch, repo: Path):
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    original = "# add_column converts structured arrays to NdarrayMixin"
    out = uplift.augment_task_description(original, str(repo))
    assert out.startswith(original)
    assert "Candidate edit locations" in out
    assert "pkg/table.py" in out
    assert "git diff" in out


def test_mandate_states_that_replies_are_not_graded(monkeypatch, repo: Path):
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    out = uplift.augment_task_description("# anything at all", str(repo))
    # Normalised: the mandate is hard-wrapped, so the phrase spans a newline.
    flat = " ".join(out.split())
    assert "does not read your replies" in flat
    assert "scores zero" in flat


def test_mandate_forbids_editing_tests(monkeypatch, repo: Path):
    """The prompt must not invite the one edit that would fake a pass."""
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    out = uplift.augment_task_description("# anything", str(repo))
    assert "do not modify the tests" in out.lower()


# --------------------------------------------------------- per-component knobs


def test_localization_can_be_disabled_for_ablation(monkeypatch, repo: Path):
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    monkeypatch.setenv("SWEBENCH_UPLIFT_LOCALIZATION", "0")
    config = uplift.uplift_config()
    assert config.localization is False
    out = uplift.augment_task_description("# add_column bug", str(repo), config=config)
    assert "Candidate edit locations" not in out
    assert "git diff" in out  # the mandate is independent


def test_edit_mandate_can_be_disabled_for_ablation(monkeypatch, repo: Path):
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    monkeypatch.setenv("SWEBENCH_UPLIFT_EDIT_MANDATE", "0")
    config = uplift.uplift_config()
    assert config.edit_mandate is False
    out = uplift.augment_task_description("# add_column bug", str(repo), config=config)
    assert "git diff" not in out


def test_tool_rounds_override_is_clamped(monkeypatch):
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    monkeypatch.setenv("SWEBENCH_UPLIFT_TOOL_ROUNDS", "9999")
    assert uplift.uplift_config().tool_rounds <= 64
    monkeypatch.setenv("SWEBENCH_UPLIFT_TOOL_ROUNDS", "1")
    assert uplift.uplift_config().tool_rounds >= uplift.BASELINE_TOOL_ROUNDS


def test_tool_rounds_override_ignores_garbage(monkeypatch):
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    monkeypatch.setenv("SWEBENCH_UPLIFT_TOOL_ROUNDS", "not-a-number")
    assert uplift.uplift_config().tool_rounds == uplift.UPLIFT_TOOL_ROUNDS


@pytest.mark.parametrize("value", ["0", "false", "no", "off", ""])
def test_falsey_switch_values_keep_the_baseline(monkeypatch, value):
    monkeypatch.setenv(uplift.UPLIFT_ENV, value)
    assert uplift.uplift_config().enabled is False


# ------------------------------------------------------------- fail-soft rules


def test_localization_failure_does_not_break_the_task(monkeypatch, tmp_path: Path):
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    out = uplift.augment_task_description("# bug", str(tmp_path / "missing"))
    # Degrades to the mandate alone rather than raising.
    assert "# bug" in out
    assert "Candidate edit locations" not in out


def test_localization_exception_is_swallowed(monkeypatch, repo: Path):
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")

    def boom(*args, **kwargs):
        raise RuntimeError("index exploded")

    monkeypatch.setattr("eval.harness.localize.localize", boom)
    out = uplift.augment_task_description("# bug", str(repo))
    assert "# bug" in out


# ------------------------------------------------- validation retry (arm B only)


class _StubAdapter:
    """Records calls and returns queued patches."""

    def __init__(self, patches):
        self.patches = list(patches)
        self.calls = []

    def solve_instance(self, instance, working_dir, **kwargs):
        from eval.benchmarks.swebench import EvalResult

        self.calls.append(instance.task_description)
        patch = self.patches.pop(0) if self.patches else ""
        return EvalResult(instance_id=instance.instance_id, model_patch=patch)


GOOD_DIFF = """\
diff --git a/pkg/table.py b/pkg/table.py
--- a/pkg/table.py
+++ b/pkg/table.py
@@ -1,2 +1,3 @@
 def add_column(data):
+    data = data.view(X)
     return data
"""

TESTS_ONLY_DIFF = (
    "diff --git a/pkg/tests/test_x.py b/pkg/tests/test_x.py\n"
    "--- a/pkg/tests/test_x.py\n+++ b/pkg/tests/test_x.py\n@@ -1 +1 @@\n-a\n+b\n"
)


def _instance():
    from eval.benchmarks.swebench import EvalInstance

    return EvalInstance(instance_id="astropy__astropy-13236", task_description="# bug")


def test_no_retry_when_uplift_disabled(repo: Path):
    from eval.benchmarks.swebench import EvalResult, _retry_if_disqualified

    adapter = _StubAdapter([GOOD_DIFF])
    out = _retry_if_disqualified(
        _instance(), EvalResult(instance_id="x", model_patch=""), repo, adapter
    )
    assert adapter.calls == [], "arm A must never spend a second attempt"
    assert out.model_patch == ""


def test_retry_fires_on_empty_patch_when_enabled(monkeypatch, repo: Path):
    from eval.benchmarks.swebench import EvalResult, _retry_if_disqualified

    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    adapter = _StubAdapter([GOOD_DIFF])
    out = _retry_if_disqualified(
        _instance(), EvalResult(instance_id="x", model_patch=""), repo, adapter
    )
    assert len(adapter.calls) == 1
    assert out.model_patch == GOOD_DIFF


def test_retry_feedback_names_the_observed_problem(monkeypatch, repo: Path):
    from eval.benchmarks.swebench import EvalResult, _retry_if_disqualified

    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    adapter = _StubAdapter([GOOD_DIFF])
    _retry_if_disqualified(_instance(), EvalResult(instance_id="x", model_patch=""), repo, adapter)
    feedback = " ".join(adapter.calls[0].split())
    assert "not submittable" in feedback
    assert "git diff" in feedback
    assert "not whether your diagnosis was right" in feedback


def test_retry_feedback_carries_nothing_from_the_dataset(monkeypatch, repo: Path):
    from eval.benchmarks.swebench import EvalResult, _retry_if_disqualified

    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    adapter = _StubAdapter([GOOD_DIFF])
    _retry_if_disqualified(_instance(), EvalResult(instance_id="x", model_patch=""), repo, adapter)
    feedback = adapter.calls[0]
    for leaked in ("FAIL_TO_PASS", "PASS_TO_PASS", "test_patch", "hints"):
        assert leaked not in feedback


def test_no_retry_when_patch_is_fine(monkeypatch, repo: Path):
    from eval.benchmarks.swebench import EvalResult, _retry_if_disqualified

    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    adapter = _StubAdapter([""])
    out = _retry_if_disqualified(
        _instance(), EvalResult(instance_id="x", model_patch=GOOD_DIFF), repo, adapter
    )
    assert adapter.calls == []
    assert out.model_patch == GOOD_DIFF


def test_mixed_patch_uses_source_diff_without_retry(monkeypatch, repo: Path):
    """A useful source edit must not be discarded because tests were also edited."""
    from eval.benchmarks.swebench import (
        EvalResult,
        _capture_git_diff,
        _retry_if_disqualified,
    )
    from eval.harness.validate import patched_paths

    tests_dir = repo / "pkg" / "tests"
    tests_dir.mkdir()
    test_file = tests_dir / "test_x.py"
    test_file.write_text("assert True\n", encoding="utf-8")
    subprocess.run(["git", "init", "-q", str(repo)], check=True)
    subprocess.run(["git", "-C", str(repo), "add", "-A"], check=True)
    subprocess.run(
        [
            "git",
            "-C",
            str(repo),
            "-c",
            "user.email=eval@example.invalid",
            "-c",
            "user.name=SWE Eval",
            "commit",
            "-qm",
            "baseline",
        ],
        check=True,
    )

    source_file = repo / "pkg" / "table.py"
    source_file.write_text(
        source_file.read_text(encoding="utf-8").replace("return data", "return list(data)"),
        encoding="utf-8",
    )
    test_file.write_text("assert False\n", encoding="utf-8")
    mixed_patch = _capture_git_diff(str(repo))

    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    adapter = _StubAdapter([GOOD_DIFF])
    out = _retry_if_disqualified(
        _instance(), EvalResult(instance_id="x", model_patch=mixed_patch), repo, adapter
    )

    assert adapter.calls == []
    assert patched_paths(out.model_patch) == ["pkg/table.py"]
    assert test_file.read_text(encoding="utf-8") == "assert False\n"
    changed = subprocess.run(
        ["git", "-C", str(repo), "diff", "--name-only", "HEAD"],
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
    ).stdout.splitlines()
    assert set(changed) == {"pkg/table.py", "pkg/tests/test_x.py"}


def test_retry_only_happens_once(monkeypatch, repo: Path):
    from eval.benchmarks.swebench import EvalResult, _retry_if_disqualified

    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    adapter = _StubAdapter(["", ""])  # both attempts come back empty
    _retry_if_disqualified(_instance(), EvalResult(instance_id="x", model_patch=""), repo, adapter)
    assert len(adapter.calls) == 1, "one retry, not a loop"


def test_empty_retry_never_erases_a_non_empty_first_attempt(monkeypatch, repo: Path):
    """A retry is an attempt to improve, never a way to lose ground."""
    from eval.benchmarks.swebench import EvalResult, _retry_if_disqualified

    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    adapter = _StubAdapter([""])
    out = _retry_if_disqualified(
        _instance(), EvalResult(instance_id="x", model_patch=TESTS_ONLY_DIFF), repo, adapter
    )
    assert len(adapter.calls) == 1
    assert out.model_patch == TESTS_ONLY_DIFF


def test_validation_can_be_disabled_for_ablation(monkeypatch, repo: Path):
    from eval.benchmarks.swebench import EvalResult, _retry_if_disqualified

    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    monkeypatch.setenv("SWEBENCH_UPLIFT_VALIDATION", "0")
    adapter = _StubAdapter([GOOD_DIFF])
    _retry_if_disqualified(_instance(), EvalResult(instance_id="x", model_patch=""), repo, adapter)
    assert adapter.calls == []


# ------------------------------------------------------- anti-leakage guardrail


def test_uplift_source_never_mentions_answer_fields():
    source = inspect.getsource(uplift)
    for field in ("test_patch", "FAIL_TO_PASS", "PASS_TO_PASS", "hints_text", "gold_patch"):
        assert field not in source, f"{field} must not be reachable from the uplift path"


# ------------------------------------------------- manifest provenance (arm ID)


def test_manifest_block_records_the_baseline_arm():
    """An arm A manifest must say so, not merely omit the field."""
    from eval.harness.runner import _uplift_manifest_block

    block = _uplift_manifest_block()
    assert block["enabled"] is False
    assert block["components"] == []
    assert block["tool_rounds"] == uplift.BASELINE_TOOL_ROUNDS


def test_manifest_block_records_the_optimized_arm(monkeypatch):
    from eval.harness.runner import _uplift_manifest_block

    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    block = _uplift_manifest_block()
    assert block["enabled"] is True
    assert "localization" in block["components"]
    assert block["tool_rounds"] == uplift.UPLIFT_TOOL_ROUNDS


def test_manifest_block_distinguishes_the_two_arms(monkeypatch):
    """The property that makes an artifact attributable to an arm."""
    from eval.harness.runner import _uplift_manifest_block

    monkeypatch.delenv(uplift.UPLIFT_ENV, raising=False)
    arm_a = _uplift_manifest_block()
    monkeypatch.setenv(uplift.UPLIFT_ENV, "1")
    arm_b = _uplift_manifest_block()
    assert arm_a != arm_b


def test_manifest_block_degrades_instead_of_raising(monkeypatch):
    from eval.harness import runner

    def boom():
        raise RuntimeError("config unreadable")

    monkeypatch.setattr("eval.harness.uplift.uplift_config", boom)
    block = runner._uplift_manifest_block()
    assert block["enabled"] is False
    assert "unavailable" in block
