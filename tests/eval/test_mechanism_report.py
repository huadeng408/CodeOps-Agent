from __future__ import annotations

import json
import sys
from pathlib import Path
from types import MappingProxyType
from typing import Any

import pytest

from eval.swebench_work import mechanism_report as M
from eval.swebench_work.report_gate import FinalizedRun, ReportGateError


CONTAMINATED = "astropy__astropy-12907"
SOURCE_IDS = (CONTAMINATED, *(f"repo__case-{index}" for index in range(1, 20)))
PATCH_A = (
    "diff --git a/pkg/separable.py b/pkg/separable.py\n"
    "--- a/pkg/separable.py\n+++ b/pkg/separable.py\n@@ -1 +1,2 @@\n def f():\n+    pass\n"
)
PATCH_ELSEWHERE = (
    "diff --git a/pkg/other.py b/pkg/other.py\n"
    "--- a/pkg/other.py\n+++ b/pkg/other.py\n@@ -1 +1,2 @@\n def g():\n+    pass\n"
)


def _run(
    arm: str,
    *,
    predictions: list[dict[str, Any]] | None = None,
    instances: list[dict[str, Any]] | None = None,
    spans: list[dict[str, Any]] | None = None,
) -> FinalizedRun:
    empty = MappingProxyType({})
    return FinalizedRun(
        arm=arm,
        run_id=f"{arm}-run",
        run_dir=Path(arm),
        manifest=empty,
        summary=empty,
        manifest_sha256=f"{arm}-manifest",
        checksums_sha256=f"{arm}-checksums",
        cohort_path=Path("cohort.json"),
        cohort_sha256="cohort",
        source_ids=SOURCE_IDS,
        eligible_ids=frozenset(SOURCE_IDS) - {CONTAMINATED},
        instances=tuple(instances or ()),
        predictions=tuple(predictions or ()),
        events=(),
        failures=(),
        trace_summary={"spans": tuple(spans or ())},
        span_assertion=empty,
        official_verdicts=empty,
        outcomes=empty,
        unmeasured_by_reason=empty,
        failures_by_reason=empty,
        excluded_contaminated={CONTAMINATED: "known benchmark contamination"},
        scorer_evidence_scope="last-invocation-only",
    )


def _tool_span(instance_id: str, tool: str) -> dict[str, Any]:
    return {
        "name": f"execute_tool {tool}",
        "attributes": {"tool.name": tool, "eval.instance_id": instance_id},
    }


def _chat_span(instance_id: str) -> dict[str, Any]:
    return {"name": "chat", "attributes": {"eval.instance_id": instance_id}}


def _instance_row(instance_id: str, files: list[str]) -> dict[str, Any]:
    return {"instance_id": instance_id, "metadata": {"localization": {"files": files}}}


def test_search_edit_and_chat_are_counted_for_eligible_ids() -> None:
    instance_id = SOURCE_IDS[1]
    spans = (
        [_tool_span(instance_id, "Grep")] * 5
        + [_tool_span(instance_id, "Read")] * 3
        + [_tool_span(instance_id, "Edit")]
        + [_chat_span(instance_id)] * 4
    )
    arm = M.load_arm(_run("baseline", spans=spans))
    assert arm.search_calls == 8
    assert arm.edit_calls == 1
    assert arm.search_to_edit == "8.0:1"
    assert arm.chat_rounds == {instance_id: 4}
    assert arm.instances_with_edits == {instance_id}


def test_zero_edit_ratio_is_explicit() -> None:
    arm = M.load_arm(
        _run("baseline", spans=[_tool_span(SOURCE_IDS[1], "Grep")] * 9)
    )
    assert arm.search_to_edit == "9:0"


def test_empty_and_non_empty_patches_are_split() -> None:
    arm = M.load_arm(
        _run(
            "baseline",
            predictions=[
                {"instance_id": SOURCE_IDS[1], "model_patch": PATCH_A},
                {"instance_id": SOURCE_IDS[2], "model_patch": ""},
                {"instance_id": SOURCE_IDS[3], "model_patch": "  \n"},
            ],
        )
    )
    assert arm.non_empty_patches == {SOURCE_IDS[1]}
    assert arm.empty_patches == {SOURCE_IDS[2], SOURCE_IDS[3]}


def test_localization_hit_and_miss_are_visible() -> None:
    arm = M.load_arm(
        _run(
            "optimized",
            predictions=[
                {"instance_id": SOURCE_IDS[1], "model_patch": PATCH_A},
                {"instance_id": SOURCE_IDS[2], "model_patch": PATCH_ELSEWHERE},
            ],
            instances=[
                _instance_row(SOURCE_IDS[1], ["pkg/other.py", "pkg/separable.py"]),
                _instance_row(SOURCE_IDS[2], ["pkg/separable.py"]),
            ],
        )
    )
    data = M.summarise(arm)["localization"]
    assert arm.localization_hit_rank == {SOURCE_IDS[1]: 2, SOURCE_IDS[2]: -1}
    assert data["hits"] == 1
    assert data["misses"] == 1
    assert data["hit_at_3"] == 1
    assert data["mean_rank_of_hits"] == 2.0


def test_empty_patch_is_neither_localization_hit_nor_miss() -> None:
    instance_id = SOURCE_IDS[1]
    arm = M.load_arm(
        _run(
            "optimized",
            predictions=[{"instance_id": instance_id, "model_patch": ""}],
            instances=[_instance_row(instance_id, ["pkg/separable.py"])],
        )
    )
    assert arm.localization_available == {instance_id}
    assert arm.localization_hit_rank == {}


def test_contaminated_id_cannot_affect_any_mechanism_metric() -> None:
    arm = M.load_arm(
        _run(
            "optimized",
            predictions=[{"instance_id": CONTAMINATED, "model_patch": ""}],
            instances=[_instance_row(CONTAMINATED, ["pkg/separable.py"])],
            spans=[
                _tool_span(CONTAMINATED, "Grep"),
                _tool_span(CONTAMINATED, "Edit"),
                _chat_span(CONTAMINATED),
            ],
        )
    )
    assert M.summarise(arm) == {
        "run_id": "optimized-run",
        "search_calls": 0,
        "edit_calls": 0,
        "search_to_edit": "0:0",
        "tool_counts": {},
        "auxiliary_tool_counts": {},
        "instances_traced": 0,
        "instances_with_edits": 0,
        "max_chat_round": 0,
        "empty_patches": 0,
        "non_empty_patches": 0,
        "localization": {
            "instances_with_a_ranking": 0,
            "instances_judgeable": 0,
            "hits": 0,
            "misses": 0,
            "missed_instances": [],
            "hit_at_1": 0,
            "hit_at_3": 0,
            "mean_rank_of_hits": None,
        },
    }


def test_auxiliary_validation_tools_are_separate_from_search_edit_ratio() -> None:
    instance_id = SOURCE_IDS[1]
    data = M.summarise(
        M.load_arm(
            _run(
                "optimized",
                spans=[
                    _tool_span(instance_id, "Grep"),
                    _tool_span(instance_id, "Edit"),
                    _tool_span(instance_id, "Git"),
                ],
            )
        )
    )
    assert data["search_calls"] == 1
    assert data["edit_calls"] == 1
    assert data["auxiliary_tool_counts"] == {"Git": 1}


def test_render_distinguishes_baseline_missing_ranking() -> None:
    arms = {
        "baseline": M.summarise(M.load_arm(_run("baseline"))),
        "optimized": M.summarise(M.load_arm(_run("optimized"))),
    }
    text = M.render(arms)
    assert "expected for the baseline arm" in text
    assert "NO RANKING RECORDED -- localization cannot be credited" in text
    assert "consistent with three simultaneous changes" in text


def test_every_driver_tool_is_classified() -> None:
    from eval.driver_headless import _TOOL_HANDLERS

    assert set(_TOOL_HANDLERS) <= M.SEARCH_TOOLS | M.EDIT_TOOLS
    assert not (M.SEARCH_TOOLS & M.EDIT_TOOLS)


def test_search_knowledge_is_counted_as_search() -> None:
    instance_id = SOURCE_IDS[1]
    arm = M.load_arm(
        _run("optimized", spans=[_tool_span(instance_id, "SearchKnowledge")])
    )
    assert M.summarise(arm)["search_calls"] == 1
    assert M.summarise(arm)["auxiliary_tool_counts"] == {}


def test_blocked_main_does_not_render_or_overwrite_json(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    output = tmp_path / "mechanism.json"
    output.write_bytes(b"keep")
    monkeypatch.setattr(M, "load_verified_runs", lambda path: (_ for _ in ()).throw(ReportGateError("stale receipt")))
    monkeypatch.setattr(sys, "argv", ["mechanism_report.py", "--json", str(output)])
    assert M.main() == 2
    assert capsys.readouterr().out == "BLOCKED: stale receipt\n"
    assert output.read_bytes() == b"keep"


def test_valid_main_writes_receipt_and_eligible_identity(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    output = tmp_path / "mechanism.json"
    runs = {"baseline": _run("baseline"), "optimized": _run("optimized")}
    monkeypatch.setattr(M, "load_verified_runs", lambda path: runs)
    monkeypatch.setattr(
        sys,
        "argv",
        ["mechanism_report.py", "--verified-arms", "receipt.json", "--json", str(output)],
    )
    assert M.main() == 0
    payload = json.loads(output.read_text(encoding="utf-8"))
    assert payload["verified_arms"] == "receipt.json"
    assert payload["runs"]["optimized"]["eligible_total"] == 19
    assert payload["mechanism"]["optimized"]["run_id"] == "optimized-run"
