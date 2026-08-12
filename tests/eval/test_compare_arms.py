from __future__ import annotations

import json
import sys
from pathlib import Path
from types import MappingProxyType
from typing import Any

import pytest

from eval.swebench_work import compare_arms as C
from eval.swebench_work.report_gate import FinalizedRun, ReportGateError


CONTAMINATED = "astropy__astropy-12907"
SOURCE_IDS = (CONTAMINATED, *(f"repo__case-{index}" for index in range(1, 20)))


def _run(
    arm: str,
    *,
    outcomes: dict[str, bool] | None = None,
    official: dict[str, bool] | None = None,
    unmeasured: dict[str, dict[str, dict[str, str]]] | None = None,
    failures: dict[str, dict[str, dict[str, str]]] | None = None,
    predictions: list[dict[str, Any]] | None = None,
    spans: list[dict[str, Any]] | None = None,
) -> FinalizedRun:
    empty = MappingProxyType({})
    if outcomes is None:
        outcomes = {source_id: False for source_id in SOURCE_IDS}
    if official is None:
        official = dict(outcomes)
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
        instances=(),
        predictions=tuple(predictions or ()),
        events=(),
        failures=(),
        trace_summary={"spans": tuple(spans or ())},
        span_assertion=empty,
        official_verdicts=official,
        outcomes=outcomes,
        unmeasured_by_reason=unmeasured or {},
        failures_by_reason=failures or {},
        excluded_contaminated={CONTAMINATED: "known benchmark contamination"},
        scorer_evidence_scope="last-invocation-only",
    )


def _edit_span(instance_id: str, tool: str = "Edit") -> dict[str, Any]:
    return {
        "name": f"execute_tool {tool}",
        "attributes": {"tool.name": tool, "eval.instance_id": instance_id},
    }


def test_load_arm_uses_only_supplied_finalized_run() -> None:
    run = _run("baseline", outcomes={SOURCE_IDS[1]: True})
    arm = C.load_arm(run)
    assert arm.run_id == "baseline-run"
    assert arm.outcomes == {SOURCE_IDS[1]: True}


def test_contaminated_id_is_excluded_from_empty_patch_and_edit_counts() -> None:
    run = _run(
        "baseline",
        predictions=[
            {"instance_id": CONTAMINATED, "model_patch": ""},
            {"instance_id": SOURCE_IDS[1], "model_patch": ""},
        ],
        spans=[_edit_span(CONTAMINATED), _edit_span(SOURCE_IDS[1])],
    )
    arm = C.load_arm(run)
    assert arm.empty_patches == {SOURCE_IDS[1]}
    assert arm.edits_by_instance == {SOURCE_IDS[1]: 1}


def test_scorer_unmeasured_is_absent_from_paired_outcomes() -> None:
    missing = SOURCE_IDS[2]
    a_outcomes = {source_id: False for source_id in SOURCE_IDS if source_id != missing}
    b_outcomes = {source_id: False for source_id in SOURCE_IDS}
    record = {"bucket": "scorer", "raw_category": "scorer", "detail": "failed-scorer;x"}
    a = C.load_arm(
        _run(
            "baseline",
            outcomes=a_outcomes,
            official=a_outcomes,
            unmeasured={"scorer": {missing: record}},
            failures={"scorer": {missing: record}},
        )
    )
    b = C.load_arm(_run("optimized", outcomes=b_outcomes))
    result = C.compare(a, b)
    assert result["paired_instances"] == 18
    assert result["unpaired"]["optimized_only"] == [missing]
    assert result["arms"]["baseline"]["scorer_unmeasured_source"] == 1
    assert result["arms"]["baseline"]["scorer_unmeasured_eligible"] == 1


def test_non_scorer_failure_is_false_and_remains_paired() -> None:
    failed = SOURCE_IDS[3]
    record = {"bucket": "agent", "raw_category": "agent", "detail": "failed-agent;x"}
    a_outcomes = {source_id: False for source_id in SOURCE_IDS}
    b_outcomes = dict(a_outcomes)
    b_outcomes[failed] = True
    a = C.load_arm(
        _run(
            "baseline",
            outcomes=a_outcomes,
            official={key: value for key, value in a_outcomes.items() if key != failed},
            failures={"agent": {failed: record}},
        )
    )
    b = C.load_arm(_run("optimized", outcomes=b_outcomes))
    result = C.compare(a, b)
    assert result["paired_instances"] == 19
    assert failed in result["discordant"]["optimized_only_resolved"]
    assert result["arms"]["baseline"]["failure_counts"] == {"agent": 1}


def test_failure_categories_remain_distinct() -> None:
    failures = {
        "agent": {SOURCE_IDS[1]: {"bucket": "agent", "raw_category": "agent", "detail": "a"}},
        "timeout": {SOURCE_IDS[2]: {"bucket": "timeout", "raw_category": "timeout", "detail": "t"}},
    }
    arm = C.load_arm(_run("baseline", failures=failures))
    summary = C.compare(arm, C.load_arm(_run("optimized")))["arms"]["baseline"]
    assert summary["failure_counts"] == {"agent": 1, "timeout": 1}


def test_headline_excludes_contaminated_numerator_but_keeps_twenty_denominator() -> None:
    outcomes = {source_id: False for source_id in SOURCE_IDS}
    outcomes[CONTAMINATED] = True
    outcomes[SOURCE_IDS[1]] = True
    a = C.load_arm(_run("baseline", outcomes=outcomes))
    result = C.compare(a, C.load_arm(_run("optimized", outcomes=outcomes)))
    summary = result["arms"]["baseline"]
    assert summary["headline_resolved"] == 1
    assert summary["headline_denominator"] == 20
    assert summary["resolved_eligible"] == 1
    assert summary["eligible_total"] == 19


def test_render_is_honest_and_reports_only_discordant_counts() -> None:
    a_outcomes = {source_id: False for source_id in SOURCE_IDS}
    b_outcomes = dict(a_outcomes)
    b_outcomes[SOURCE_IDS[1]] = True
    a = C.load_arm(_run("baseline", outcomes=a_outcomes))
    b = C.load_arm(_run("optimized", outcomes=b_outcomes))
    text = C.render(a, b, C.compare(a, b))
    assert "arm A resolved: 0/20" in text
    assert "arm B resolved: 1/20" in text
    assert "Paired eligible outcomes" in text
    assert "B fixed what A missed: 1" in text
    assert "not a\nSWE-bench Verified score" in text
    assert "no statistical p-value" in text
    assert "p =" not in text and "p<" not in text


def test_edit_tools_are_shared_with_mechanism_report() -> None:
    from eval.swebench_work.mechanism_report import EDIT_TOOLS

    assert C.EDIT_TOOLS is EDIT_TOOLS


def test_blocked_main_does_not_overwrite_json(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    capsys: pytest.CaptureFixture[str],
) -> None:
    output = tmp_path / "comparison.json"
    output.write_bytes(b"keep")
    monkeypatch.setattr(C, "load_verified_runs", lambda path: (_ for _ in ()).throw(ReportGateError("bad receipt")))
    monkeypatch.setattr(sys, "argv", ["compare_arms.py", "--json", str(output)])
    assert C.main() == 2
    assert capsys.readouterr().out == "BLOCKED: bad receipt\n"
    assert output.read_bytes() == b"keep"


def test_valid_main_writes_receipt_and_run_identity(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    output = tmp_path / "comparison.json"
    runs = {"baseline": _run("baseline"), "optimized": _run("optimized")}
    monkeypatch.setattr(C, "load_verified_runs", lambda path: runs)
    monkeypatch.setattr(
        sys,
        "argv",
        ["compare_arms.py", "--verified-arms", "receipt.json", "--json", str(output)],
    )
    assert C.main() == 0
    payload = json.loads(output.read_text(encoding="utf-8"))
    assert payload["verified_arms"] == "receipt.json"
    assert payload["runs"]["baseline"]["run_id"] == "baseline-run"
    assert payload["comparison"]["cohort"] == {"eligible_total": 19, "source_total": 20}
