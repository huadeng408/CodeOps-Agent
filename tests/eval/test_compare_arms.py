"""Tests for the paired-arm comparison.

Most of these check that the script refuses to say things the data does not
support: no verdict from one arm, no averaging over a mismatched instance set, no
reuse of a contaminated instance, and never the phrase "SWE-bench Verified".
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.swebench_work import compare_arms as C


def _write_run(root: Path, arm_dir: str, run_id: str, rows: list[dict]) -> Path:
    run = root / arm_dir / run_id
    run.mkdir(parents=True, exist_ok=True)
    with (run / "predictions.jsonl").open("w", encoding="utf-8") as handle:
        for row in rows:
            handle.write(json.dumps(row) + "\n")
    return run


def _row(instance_id: str, resolved: bool, patch: str = "diff --git a/x b/x\n") -> dict:
    return {
        "instance_id": instance_id,
        "model_patch": patch,
        "resolved": resolved,
        "scorer_status": f"official: resolved={resolved} (…)",
        "error": "",
    }


@pytest.fixture()
def experiment(tmp_path: Path, monkeypatch):
    monkeypatch.setattr(C, "EXPERIMENT_DIR", tmp_path)
    return tmp_path


# --------------------------------------------------------------------- loading


def test_missing_arm_loads_empty(experiment: Path):
    arm = C.load_arm("baseline")
    assert arm.measured == set()
    assert arm.run_id == ""


def test_verdicts_are_read(experiment: Path):
    _write_run(experiment, "arm-a-baseline", "run-1", [_row("i-1", True), _row("i-2", False)])
    arm = C.load_arm("baseline")
    assert arm.verdicts == {"i-1": True, "i-2": False}
    assert arm.resolved == {"i-1"}


def test_empty_patches_are_tracked(experiment: Path):
    _write_run(
        experiment, "arm-a-baseline", "run-1",
        [_row("i-1", False, patch=""), _row("i-2", True)],
    )
    arm = C.load_arm("baseline")
    assert arm.empty_patches == {"i-1"}


def test_infra_failure_without_a_verdict_is_excluded_from_verdicts(experiment: Path):
    """An unmeasured instance must not enter the verdict table as a failure."""
    _write_run(
        experiment, "arm-a-baseline", "run-1",
        [
            {
                "instance_id": "i-1",
                "model_patch": "",
                "error": "RuntimeError: OpenAI HTTP 401",
                "scorer_status": "",
                "resolved": False,
            },
            _row("i-2", True),
        ],
    )
    arm = C.load_arm("baseline")
    assert "i-1" not in arm.verdicts
    assert "i-1" in arm.infra_failures
    assert arm.measured == {"i-2"}


def test_latest_run_dir_is_chosen(experiment: Path):
    import os
    import time

    first = _write_run(experiment, "arm-a-baseline", "run-old", [_row("i-1", False)])
    time.sleep(0.01)
    second = _write_run(experiment, "arm-a-baseline", "run-new", [_row("i-1", True)])
    os.utime(first, (1, 1))
    assert C.load_arm("baseline").run_id == second.name


# ------------------------------------------------------------------- comparison


def test_pairs_only_instances_both_arms_measured(experiment: Path):
    _write_run(experiment, "arm-a-baseline", "a", [_row("i-1", False), _row("i-2", False)])
    _write_run(experiment, "arm-b-optimized", "b", [_row("i-1", True)])
    result = C.compare(C.load_arm("baseline"), C.load_arm("optimized"))
    assert result["paired_instances"] == 1
    assert result["unpaired"]["baseline_only"] == ["i-2"]


def test_discordant_pairs_are_counted_in_both_directions(experiment: Path):
    _write_run(
        experiment, "arm-a-baseline", "a",
        [_row("i-1", True), _row("i-2", False), _row("i-3", False)],
    )
    _write_run(
        experiment, "arm-b-optimized", "b",
        [_row("i-1", False), _row("i-2", True), _row("i-3", False)],
    )
    result = C.compare(C.load_arm("baseline"), C.load_arm("optimized"))
    assert result["discordant"]["baseline_only_resolved"] == ["i-1"]
    assert result["discordant"]["optimized_only_resolved"] == ["i-2"]
    assert result["neither_resolved"] == ["i-3"]


def test_contaminated_instance_is_excluded_from_the_pairing(experiment: Path):
    contaminated = next(iter(C.CONTAMINATED))
    _write_run(
        experiment, "arm-a-baseline", "a", [_row(contaminated, True), _row("i-2", False)]
    )
    _write_run(
        experiment, "arm-b-optimized", "b", [_row(contaminated, True), _row("i-2", True)]
    )
    result = C.compare(C.load_arm("baseline"), C.load_arm("optimized"))
    assert result["paired_instances"] == 1
    assert contaminated in result["excluded_contaminated"]
    assert result["baseline_resolved"] == 0


def test_contaminated_list_is_documented():
    """Every exclusion must carry a reason, or it looks like cherry-picking."""
    assert C.CONTAMINATED
    for instance, reason in C.CONTAMINATED.items():
        assert instance.startswith("astropy__")
        assert len(reason) > 10


# -------------------------------------------------------------------- rendering


def test_render_blocks_when_one_arm_is_missing(experiment: Path):
    _write_run(experiment, "arm-a-baseline", "a", [_row("i-1", True)])
    a, b = C.load_arm("baseline"), C.load_arm("optimized")
    text = C.render(a, b, C.compare(a, b))
    assert "BLOCKED" in text
    assert "not a comparison" in text
    assert "/20" not in text, "no score may be printed from one arm"


def test_render_never_says_swe_bench_verified(experiment: Path):
    _write_run(experiment, "arm-a-baseline", "a", [_row("i-1", False)])
    _write_run(experiment, "arm-b-optimized", "b", [_row("i-1", True)])
    a, b = C.load_arm("baseline"), C.load_arm("optimized")
    text = C.render(a, b, C.compare(a, b))
    assert "NOT a SWE-bench Verified score" in text
    assert "astropy-20 subset" in text


def test_render_reports_no_p_value(experiment: Path):
    _write_run(experiment, "arm-a-baseline", "a", [_row("i-1", False)])
    _write_run(experiment, "arm-b-optimized", "b", [_row("i-1", True)])
    a, b = C.load_arm("baseline"), C.load_arm("optimized")
    text = C.render(a, b, C.compare(a, b))
    assert "p-value" in text  # mentioned only to say it is withheld
    assert "p =" not in text
    assert "p<" not in text


def test_render_warns_on_mismatched_instance_sets(experiment: Path):
    _write_run(experiment, "arm-a-baseline", "a", [_row("i-1", False), _row("i-9", False)])
    _write_run(experiment, "arm-b-optimized", "b", [_row("i-1", True)])
    a, b = C.load_arm("baseline"), C.load_arm("optimized")
    text = C.render(a, b, C.compare(a, b))
    assert "did not measure the same instance set" in text


def test_render_includes_the_mechanism_counts(experiment: Path):
    _write_run(experiment, "arm-a-baseline", "a", [_row("i-1", False, patch="")])
    _write_run(experiment, "arm-b-optimized", "b", [_row("i-1", True)])
    a, b = C.load_arm("baseline"), C.load_arm("optimized")
    text = C.render(a, b, C.compare(a, b))
    assert "empty patches" in text
    assert "instances w/ edits" in text
