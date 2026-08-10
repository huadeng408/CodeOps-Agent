"""Tests for the mechanism report.

The point of this report is to stop a total from being credited to whichever
component sounds best, so the tests focus on the distinctions that make it
capable of that: a localization miss must be visible, an instance with no patch
must count as neither hit nor miss, and the baseline arm must show no ranking
rather than a ranking of zero.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.swebench_work import mechanism_report as M


PATCH_A = (
    "diff --git a/pkg/separable.py b/pkg/separable.py\n"
    "--- a/pkg/separable.py\n+++ b/pkg/separable.py\n@@ -1 +1,2 @@\n def f():\n+    pass\n"
)
PATCH_ELSEWHERE = (
    "diff --git a/pkg/other.py b/pkg/other.py\n"
    "--- a/pkg/other.py\n+++ b/pkg/other.py\n@@ -1 +1,2 @@\n def g():\n+    pass\n"
)


def _write_run(
    root: Path,
    arm_dir: str,
    run_id: str,
    *,
    predictions: list[dict],
    instances: list[dict] | None = None,
    spans: list[dict] | None = None,
) -> Path:
    run = root / arm_dir / run_id
    run.mkdir(parents=True, exist_ok=True)
    with (run / "predictions.jsonl").open("w", encoding="utf-8") as handle:
        for row in predictions:
            handle.write(json.dumps(row) + "\n")
    if instances is not None:
        with (run / "instances.jsonl").open("w", encoding="utf-8") as handle:
            for row in instances:
                handle.write(json.dumps(row) + "\n")
    if spans is not None:
        (run / "traces").mkdir(exist_ok=True)
        (run / "traces" / "trace-summary.json").write_text(
            json.dumps({"spans": spans}), encoding="utf-8"
        )
    return run


def _tool_span(instance_id: str, tool: str) -> dict:
    return {
        "name": f"execute_tool {tool}",
        "attributes": {"tool.name": tool, "eval.instance_id": instance_id},
    }


def _chat_span(instance_id: str) -> dict:
    return {"name": "chat", "attributes": {"eval.instance_id": instance_id}}


@pytest.fixture()
def experiment(tmp_path: Path, monkeypatch):
    monkeypatch.setattr(M, "EXPERIMENT_DIR", tmp_path)
    return tmp_path


# ------------------------------------------------------------------------ turns


def test_search_and_edit_calls_are_counted_separately(experiment: Path):
    spans = (
        [_tool_span("i-1", "Grep")] * 5
        + [_tool_span("i-1", "Read")] * 3
        + [_tool_span("i-1", "Edit")]
    )
    _write_run(
        experiment, "arm-a-baseline", "run", predictions=[], spans=spans
    )
    arm = M.load_arm("baseline")
    assert arm.search_calls == 8
    assert arm.edit_calls == 1
    assert arm.search_to_edit == "8.0:1"


def test_search_to_edit_handles_zero_edits(experiment: Path):
    """The baseline's actual shape: searches with no edit at all."""
    _write_run(
        experiment, "arm-a-baseline", "run",
        predictions=[], spans=[_tool_span("i-1", "Grep")] * 9,
    )
    assert M.load_arm("baseline").search_to_edit == "9:0"


def test_instances_with_edits_are_tracked(experiment: Path):
    spans = [_tool_span("i-1", "Edit"), _tool_span("i-2", "Grep")]
    _write_run(experiment, "arm-a-baseline", "run", predictions=[], spans=spans)
    arm = M.load_arm("baseline")
    assert arm.instances_with_edits == {"i-1"}
    assert arm.instances_seen == {"i-1", "i-2"}


def test_chat_rounds_are_counted_per_instance(experiment: Path):
    spans = [_chat_span("i-1")] * 9 + [_chat_span("i-2")] * 3
    _write_run(experiment, "arm-a-baseline", "run", predictions=[], spans=spans)
    arm = M.load_arm("baseline")
    assert arm.chat_rounds == {"i-1": 9, "i-2": 3}
    assert arm.max_chat_round == 9


def test_missing_traces_yield_zero_not_an_error(experiment: Path):
    """Traces are written at run end, so a live run has none yet."""
    _write_run(experiment, "arm-a-baseline", "run", predictions=[])
    arm = M.load_arm("baseline")
    assert arm.search_calls == 0
    assert arm.edit_calls == 0


# --------------------------------------------------------------------- delivery


def test_empty_and_non_empty_patches_are_split(experiment: Path):
    _write_run(
        experiment, "arm-a-baseline", "run",
        predictions=[
            {"instance_id": "i-1", "model_patch": PATCH_A},
            {"instance_id": "i-2", "model_patch": ""},
            {"instance_id": "i-3", "model_patch": "   \n"},
        ],
    )
    arm = M.load_arm("baseline")
    assert arm.non_empty_patches == {"i-1"}
    assert arm.empty_patches == {"i-2", "i-3"}


# ---------------------------------------------------------------- localization


def _instance_row(instance_id: str, files: list[str]) -> dict:
    return {
        "instance_id": instance_id,
        "metadata": {"localization": {"files": files}},
    }


def test_localization_hit_records_the_rank(experiment: Path):
    _write_run(
        experiment, "arm-b-optimized", "run",
        predictions=[{"instance_id": "i-1", "model_patch": PATCH_A}],
        instances=[_instance_row("i-1", ["pkg/other.py", "pkg/separable.py"])],
    )
    arm = M.load_arm("optimized")
    assert arm.localization_hit_rank == {"i-1": 2}


def test_localization_hit_at_rank_one(experiment: Path):
    _write_run(
        experiment, "arm-b-optimized", "run",
        predictions=[{"instance_id": "i-1", "model_patch": PATCH_A}],
        instances=[_instance_row("i-1", ["pkg/separable.py", "pkg/other.py"])],
    )
    data = M.summarise(M.load_arm("optimized"))
    assert data["localization"]["hit_at_1"] == 1
    assert data["localization"]["mean_rank_of_hits"] == 1.0


def test_localization_miss_is_visible(experiment: Path):
    """A ranking that sent the agent to the wrong file must be reported."""
    _write_run(
        experiment, "arm-b-optimized", "run",
        predictions=[{"instance_id": "i-1", "model_patch": PATCH_ELSEWHERE}],
        instances=[_instance_row("i-1", ["pkg/separable.py"])],
    )
    arm = M.load_arm("optimized")
    assert arm.localization_hit_rank == {"i-1": -1}
    data = M.summarise(arm)
    assert data["localization"]["misses"] == 1
    assert data["localization"]["missed_instances"] == ["i-1"]


def test_no_patch_is_neither_hit_nor_miss(experiment: Path):
    """An empty patch says nothing about the ranking's quality."""
    _write_run(
        experiment, "arm-b-optimized", "run",
        predictions=[{"instance_id": "i-1", "model_patch": ""}],
        instances=[_instance_row("i-1", ["pkg/separable.py"])],
    )
    arm = M.load_arm("optimized")
    assert arm.localization_available == {"i-1"}
    assert arm.localization_hit_rank == {}
    data = M.summarise(arm)
    assert data["localization"]["instances_with_a_ranking"] == 1
    assert data["localization"]["instances_judgeable"] == 0


def test_baseline_arm_reports_no_ranking_rather_than_zero(experiment: Path):
    _write_run(
        experiment, "arm-a-baseline", "run",
        predictions=[{"instance_id": "i-1", "model_patch": PATCH_A}],
        instances=[{"instance_id": "i-1", "metadata": {}}],
    )
    data = M.summarise(M.load_arm("baseline"))
    assert data["localization"]["instances_with_a_ranking"] == 0
    assert data["localization"]["mean_rank_of_hits"] is None


def test_mean_rank_is_none_when_there_are_no_hits(experiment: Path):
    _write_run(
        experiment, "arm-b-optimized", "run",
        predictions=[{"instance_id": "i-1", "model_patch": PATCH_ELSEWHERE}],
        instances=[_instance_row("i-1", ["pkg/separable.py"])],
    )
    assert M.summarise(M.load_arm("optimized"))["localization"]["mean_rank_of_hits"] is None


# -------------------------------------------------------------------- rendering


def test_render_marks_a_missing_arm(experiment: Path):
    _write_run(experiment, "arm-a-baseline", "run", predictions=[])
    arms = {arm: M.summarise(M.load_arm(arm)) for arm in M.ARMS}
    text = M.render(arms)
    assert "optimized: MISSING" in text


def test_render_states_the_attribution_caveat(experiment: Path):
    for arm_dir in ("arm-a-baseline", "arm-b-optimized"):
        _write_run(
            experiment, arm_dir, "run",
            predictions=[{"instance_id": "i-1", "model_patch": PATCH_A}],
        )
    arms = {arm: M.summarise(M.load_arm(arm)) for arm in M.ARMS}
    text = M.render(arms)
    assert "consistent with three" in text
    assert "repairs to basic defects" in text
    assert "search:edit" in text


def test_render_reports_the_ratio_shift(experiment: Path):
    _write_run(
        experiment, "arm-a-baseline", "run",
        predictions=[{"instance_id": "i-1", "model_patch": ""}],
        spans=[_tool_span("i-1", "Grep")] * 8,
    )
    _write_run(
        experiment, "arm-b-optimized", "run",
        predictions=[{"instance_id": "i-1", "model_patch": PATCH_A}],
        spans=[_tool_span("i-1", "Grep"), _tool_span("i-1", "Edit")],
    )
    arms = {arm: M.summarise(M.load_arm(arm)) for arm in M.ARMS}
    text = M.render(arms)
    assert "8:0" in text and "1.0:1" in text
    assert "Empty patches went 1 -> 0" in text


def test_bash_counts_as_search_not_edit(experiment: Path):
    """Bash was 15 of the baseline's 96 search calls; it must not read as an edit."""
    _write_run(
        experiment, "arm-a-baseline", "run",
        predictions=[], spans=[_tool_span("i-1", "Bash")] * 4,
    )
    arm = M.load_arm("baseline")
    assert arm.search_calls == 4
    assert arm.edit_calls == 0
