"""Tests for the arm verification gate.

This gate exists because both of its failure modes are silent: an arm B that ran
with the uplift off, and an arm A that ran with it on, each produce a complete
artifact tree that the comparison tools read happily. So the tests here are
mostly about the gate *failing* when it should — a verifier that cannot fail is
the defect it was written to catch.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.swebench_work import verify_arms as V


MANDATE = "## How this task is graded\n\nThe grader reads `git diff`."
RANKING = {"files": ["pkg/separable.py"], "file_scores": {"pkg/separable.py": 4.2}}


def _write_arm(
    root: Path,
    arm_dir: str,
    *,
    uplift: bool | None,
    instances: list[dict],
    run_id: str = "run",
) -> Path:
    run = root / arm_dir / run_id
    run.mkdir(parents=True, exist_ok=True)
    if uplift is not None:
        run.joinpath("run-manifest.json").write_text(
            json.dumps(
                {
                    "harness_uplift": {
                        "enabled": uplift,
                        "components": ["localization", "edit_mandate"] if uplift else [],
                        "tool_rounds": 24 if uplift else 8,
                    }
                }
            ),
            encoding="utf-8",
        )
    with run.joinpath("instances.jsonl").open("w", encoding="utf-8") as handle:
        for row in instances:
            handle.write(json.dumps(row) + "\n")
    return run


def _instance(
    instance_id: str,
    *,
    localization: dict | None = None,
    mandate: bool = False,
    metadata: dict | None = None,
) -> dict:
    meta = dict(metadata or {})
    if localization is not None:
        meta["localization"] = localization
    task = "Separability matrix is wrong"
    if mandate:
        task = f"{task}\n\n{MANDATE}"
    return {"instance_id": instance_id, "task_description": task, "metadata": meta}


def _baseline(root: Path, **kwargs) -> Path:
    return _write_arm(
        root, "arm-a-baseline", uplift=False,
        instances=[_instance("i-1")], **kwargs
    )


def _optimized(root: Path, **kwargs) -> Path:
    return _write_arm(
        root, "arm-b-optimized", uplift=True,
        instances=[_instance("i-1", localization=RANKING, mandate=True)], **kwargs
    )


@pytest.fixture()
def experiment(tmp_path: Path, monkeypatch):
    monkeypatch.setattr(V, "EXPERIMENT_DIR", tmp_path)
    return tmp_path


# ------------------------------------------------------------------ happy path


def test_correctly_configured_arms_pass(experiment: Path):
    _baseline(experiment)
    _optimized(experiment)
    audits = {arm: V.audit(V.load_evidence(arm)) for arm in V.ARMS}
    assert audits["baseline"]["ok"]
    assert audits["optimized"]["ok"]
    assert "may be read" in V.render(audits)


def test_manifest_details_are_surfaced(experiment: Path):
    _optimized(experiment)
    data = V.audit(V.load_evidence("optimized"))
    assert data["manifest_tool_rounds"] == 24
    assert "localization" in data["manifest_components"]


# ------------------------------------------------- the two silent failure modes


def test_optimized_arm_without_the_uplift_fails(experiment: Path):
    """Arm B as a second baseline: the comparison would measure noise."""
    _baseline(experiment)
    _write_arm(
        experiment, "arm-b-optimized", uplift=False, instances=[_instance("i-1")]
    )
    audits = {arm: V.audit(V.load_evidence(arm)) for arm in V.ARMS}
    assert not audits["optimized"]["ok"]
    assert V.audit(V.load_evidence("optimized"))["mismatches"]
    assert "DO NOT READ THE COMPARISON" in V.render(audits)


def test_baseline_arm_with_the_uplift_fails(experiment: Path):
    """A contaminated baseline understates the gain by an unknown amount."""
    _write_arm(
        experiment, "arm-a-baseline", uplift=True,
        instances=[_instance("i-1", localization=RANKING, mandate=True)],
    )
    _optimized(experiment)
    audits = {arm: V.audit(V.load_evidence(arm)) for arm in V.ARMS}
    assert not audits["baseline"]["ok"]
    assert "DO NOT READ THE COMPARISON" in V.render(audits)


# ----------------------------------------------- intent vs effect disagreement


def test_manifest_enabled_but_no_localization_record_fails(experiment: Path):
    """The manifest records intent; the instances record effect.

    This is the state a broken metadata stash would produce: the run believed
    the uplift was on, and nothing downstream can prove the localizer ran.
    """
    _write_arm(
        experiment, "arm-b-optimized", uplift=True,
        instances=[_instance("i-1", mandate=True)],
    )
    data = V.audit(V.load_evidence("optimized"))
    assert not data["ok"]
    assert any("no instance carries a localization record" in p
               for p in data["inconsistencies"])


def test_manifest_disabled_but_localization_present_fails(experiment: Path):
    _write_arm(
        experiment, "arm-a-baseline", uplift=False,
        instances=[_instance("i-1", localization=RANKING)],
    )
    data = V.audit(V.load_evidence("baseline"))
    assert not data["ok"]
    assert any("uplift was disabled" in p for p in data["inconsistencies"])


# ------------------------------------------------------------- answer leakage


def test_answer_field_inside_a_localization_record_fails(experiment: Path):
    leaky = dict(RANKING)
    leaky["test_patch"] = "diff --git a/tests/test_x.py ..."
    _write_arm(
        experiment, "arm-b-optimized", uplift=True,
        instances=[_instance("i-1", localization=leaky, mandate=True)],
    )
    data = V.audit(V.load_evidence("optimized"))
    assert not data["ok"]
    assert data["answer_field_leaks"] == ["i-1: test_patch"]


def test_answer_field_value_copied_under_an_innocuous_key_fails(experiment: Path):
    """The leak a key check misses: the value, filed under a benign name."""
    gold = (
        "diff --git a/astropy/modeling/separable.py "
        "b/astropy/modeling/separable.py\n@@ -242,7 +242,7 @@\n-    cright[-1:]"
    )
    leaky = dict(RANKING)
    leaky["notes"] = gold  # innocuous key, answer value
    _write_arm(
        experiment, "arm-b-optimized", uplift=True,
        instances=[
            _instance(
                "i-1", localization=leaky, mandate=True,
                metadata={"test_patch": gold},
            )
        ],
    )
    data = V.audit(V.load_evidence("optimized"))
    assert not data["ok"]
    assert data["answer_field_leaks"] == ["i-1: test_patch"]


def test_a_filename_containing_patch_is_not_a_leak(experiment: Path):
    """Substring matching on field names failed a valid experiment here.

    ``"patch" in blob`` fires on any ranked path spelled like patches.py, and a
    guard whose false positives block correct runs gets switched off — which
    leaves the real check unrun.
    """
    _write_arm(
        experiment, "arm-b-optimized", uplift=True,
        instances=[
            _instance(
                "i-1",
                localization={
                    "files": ["astropy/utils/patches.py", "astropy/io/misc/patch.py"],
                    "file_scores": {"astropy/utils/patches.py": 3.1},
                },
                mandate=True,
                metadata={"test_patch": "diff --git a/tests/test_x.py ..."},
            )
        ],
    )
    data = V.audit(V.load_evidence("optimized"))
    assert data["answer_field_leaks"] == []
    assert data["ok"]


def test_leak_is_attributed_to_the_right_field(experiment: Path):
    """test_patch present must not also be reported as patch."""
    leaky = dict(RANKING)
    leaky["test_patch"] = "diff --git a/tests/test_x.py ..."
    _write_arm(
        experiment, "arm-b-optimized", uplift=True,
        instances=[_instance("i-1", localization=leaky, mandate=True)],
    )
    data = V.audit(V.load_evidence("optimized"))
    assert data["answer_field_leaks"] == ["i-1: test_patch"]


def test_leak_is_found_at_any_nesting_depth(experiment: Path):
    leaky = {
        "files": ["pkg/x.py"],
        "functions": [{"path": "pkg/x.py", "hints_text": "look at _coord_matrix"}],
    }
    _write_arm(
        experiment, "arm-b-optimized", uplift=True,
        instances=[_instance("i-1", localization=leaky, mandate=True)],
    )
    data = V.audit(V.load_evidence("optimized"))
    assert data["answer_field_leaks"] == ["i-1: hints_text"]


def test_answer_fields_in_metadata_alone_are_fine(experiment: Path):
    """instances.jsonl legitimately holds the dataset's answer fields."""
    _write_arm(
        experiment, "arm-b-optimized", uplift=True,
        instances=[
            _instance(
                "i-1", localization=RANKING, mandate=True,
                metadata={
                    "test_patch": "diff --git a/tests/test_sampled.py ...",
                    "hints_text": "look at _coord_matrix",
                },
            )
        ],
    )
    data = V.audit(V.load_evidence("optimized"))
    assert data["ok"], data["answer_field_leaks"]


# ----------------------------------------------------------------- absent arms


def test_absent_arm_is_not_ok(experiment: Path):
    """Absence must not read as success — that is the fail-open shape."""
    _baseline(experiment)
    audits = {arm: V.audit(V.load_evidence(arm)) for arm in V.ARMS}
    assert not audits["optimized"]["ok"]
    assert "NOT PRESENT" in V.render(audits)


def test_missing_manifest_does_not_pass_the_optimized_arm(experiment: Path):
    """The manifest lands at run end, so a live run has none yet.

    Absent evidence is not evidence of the uplift, so arm B must not pass on the
    localization records alone.
    """
    _write_arm(
        experiment, "arm-b-optimized", uplift=None,
        instances=[_instance("i-1", localization=RANKING, mandate=True)],
    )
    data = V.audit(V.load_evidence("optimized"))
    assert data["manifest_uplift"] is None
    assert not data["ok"]


def test_missing_manifest_still_lets_the_baseline_pass(experiment: Path):
    """For arm A the expectation is "off", and no manifest is consistent with it."""
    _write_arm(
        experiment, "arm-a-baseline", uplift=None, instances=[_instance("i-1")]
    )
    assert V.audit(V.load_evidence("baseline"))["ok"]


# ---------------------------------------------------------------- exit status


def test_render_never_claims_readability_when_an_arm_failed(experiment: Path):
    _baseline(experiment)
    _write_arm(
        experiment, "arm-b-optimized", uplift=False, instances=[_instance("i-1")]
    )
    text = V.render({arm: V.audit(V.load_evidence(arm)) for arm in V.ARMS})
    assert "may be read" not in text


def test_mandate_marker_survives_a_reflowed_prompt(experiment: Path):
    """Matched on a short phrase so rewrapping the block is not a false alarm."""
    row = _instance("i-1", localization=RANKING)
    row["task_description"] = "Bug\n\n## How this task is graded\n\nwrapped\ndifferently"
    _write_arm(experiment, "arm-b-optimized", uplift=True, instances=[row])
    assert V.audit(V.load_evidence("optimized"))["with_mandate"] == 1
