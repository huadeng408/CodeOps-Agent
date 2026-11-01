from __future__ import annotations

import csv
import json
from pathlib import Path

import pytest

from eval.experiment_report import main


def trial(task_id: str, repeat: int, success: bool | None, cost: float | None, **changes):
    return {
        "task_id": task_id,
        "trial_id": f"{task_id}-{repeat}-{changes.get('arm', 'single')}",
        "experiment": "budget",
        "arm": "single",
        "repeat": repeat,
        "status": "completed",
        "success": success,
        "tokens_in": 100,
        "tokens_out": 20,
        "cost": cost,
        "cost_currency": "USD",
        "elapsed_seconds": 5.0,
        "budget_tokens": 1000,
        "actually_interrupted": False,
        "recovered": None,
        "duplicate_effects": None,
        "ledger_valid": None,
        "receipt_path": "fixture-receipt.json",
        "evidence_kind": "fixture",
        **changes,
    }


def run_report(tmp_path: Path, rows: list[dict], **extra):
    source = tmp_path / "input.json"
    source.write_text(json.dumps({
        "schema_version": 1,
        "source_pin": {"git_sha": "fixture-source"},
        "trials": rows,
        **extra,
    }), encoding="utf-8")
    output = tmp_path / "report"
    assert main(["--input", str(source), "--output", str(output)]) == 0
    return output, json.loads((output / "summary.json").read_text(encoding="utf-8"))


def test_public_report_keeps_unconfirmed_window_outside_recovery_denominator(tmp_path: Path):
    _, report = run_report(tmp_path, [
        trial("unproved", 0, None, None, experiment="recovery", status="infra_error", actually_interrupted=None,
              recovered=None, duplicate_effects=None, ledger_valid=True, failure_window="after_record", fault_event_count=None,
              physical_termination_confirmed=True),
        trial("confirmed", 0, True, None, experiment="recovery", actually_interrupted=True, recovered=True,
              duplicate_effects=0, ledger_valid=True, failure_window="after_record", fault_event_count=1),
        trial("control", 0, True, None, experiment="recovery", actually_interrupted=False, recovery_mode="none",
              duplicate_effects=0, ledger_valid=True, failure_window="no_fault", fault_event_count=0),
    ])
    recovery = report["recovery"]
    assert recovery["planned_trials"] == 3
    assert recovery["actually_interrupted_trials"] == 1
    assert recovery["interruption_unknown_trials"] == 1
    assert recovery["not_interrupted_trials"] == 1
    assert recovery["fault_events"] == {"known_total": 1, "unknown_trials": 1}
    assert recovery["same_session_interrupted_trials"] == 1


def test_public_command_exports_trial_and_independent_task_costs(tmp_path: Path):
    output, report = run_report(tmp_path, [
        trial("a", 0, True, 2.0),
        trial("a", 1, False, 3.0),
        trial("b", 0, True, 4.0),
        trial("b", 1, True, 5.0),
    ], planned_trials=4)

    group = report["groups"][0]
    assert group["planned_trials"] == 4
    assert group["independent_tasks"] == 2
    assert group["successful_trials"] == 3
    assert group["tasks_with_any_success"] == 2
    assert group["costs"]["USD"]["known_total"] == 14.0
    assert group["costs"]["USD"]["per_successful_trial"] == pytest.approx(14 / 3)
    assert group["costs"]["USD"]["per_successful_task_any"] == 7.0
    assert report["source_pin"] == {"git_sha": "fixture-source"}
    assert report["provenance_verified"] is False
    with (output / "trials.csv").open(encoding="utf-8-sig", newline="") as handle:
        assert len(list(csv.DictReader(handle))) == 4


def test_budget_figure_shows_actual_tokens_and_latency_without_pricing_unknowns(tmp_path: Path):
    output, report = run_report(tmp_path, [
        trial("a", 0, True, None, cost_currency=None, tokens_in=100, tokens_out=20, elapsed_seconds=5),
        trial("b", 0, False, None, cost_currency=None, tokens_in=200, tokens_out=80, elapsed_seconds=15),
        trial("c", 0, False, None, arm="verify_retry", cost_currency=None, tokens_in=None, elapsed_seconds=None),
        trial("d", 0, None, None, arm="subagent", status="infra_error", cost_currency=None),
    ])

    figure = next(item for item in report["figures"] if item["path"] == "figures/budget-success-cost.png")
    panels = {panel["metric"]: panel for panel in figure["panels"]}
    tokens = panels["confirmed_total_tokens"]
    latency = panels["elapsed_seconds"]
    assert len(tokens["points"]) == len(latency["points"]) == 1
    assert tokens["points"][0] == {
        "arm": "single", "evidence_kind": "fixture", "budget_tokens": 1000,
        "planned_trials": 2, "independent_tasks": 2, "x": 200, "y": 0.5,
    }
    assert latency["points"][0]["x"] == 10 and latency["points"][0]["y"] == 0.5
    assert tokens["omitted_groups"] == latency["omitted_groups"] == 2
    assert tokens["unit"] == "confirmed_input_plus_output_tokens_per_trial_not_money"
    assert panels["cost_per_successful_trial"]["points"] == []
    assert panels["cost_per_successful_trial"]["omitted_groups"] == 3
    assert all(group["cost_unknown_trials"] == group["planned_trials"] for group in report["groups"])
    assert (output / figure["path"]).read_bytes().startswith(b"\x89PNG\r\n\x1a\n")


def test_unknown_trials_stay_in_plan_and_comparisons_pair_by_task(tmp_path: Path):
    rows = [
        trial("a", 0, False, 1, experiment="context_memory", arm="baseline"),
        trial("a", 1, True, None, experiment="context_memory", arm="baseline"),
        trial("b", 0, True, 2, experiment="context_memory", arm="baseline"),
        trial("b", 1, None, None, experiment="context_memory", arm="baseline", status="scorer_error", tokens_in=None),
        trial("a", 0, True, 1, experiment="context_memory", arm="optimized"),
        trial("a", 1, True, 1, experiment="context_memory", arm="optimized"),
        trial("b", 0, False, 1, experiment="context_memory", arm="optimized"),
        trial("b", 1, None, None, experiment="context_memory", arm="optimized", status="not_run", tokens_in=None),
        trial("c", 0, None, None, experiment="context_memory", arm="optimized", status="not_run", evidence_kind="provider"),
    ]
    _, report = run_report(tmp_path, rows)

    baseline = next(group for group in report["groups"] if group["arm"] == "baseline")
    assert baseline["planned_trials"] == 4
    assert baseline["unknown_outcomes"] == 1
    assert baseline["success_fraction_of_plan"] == 0.5
    assert baseline["costs"]["USD"]["known_total"] == 3
    assert baseline["costs"]["USD"]["per_successful_trial"] is None
    assert baseline["usage"]["tokens_in"] == {"known_total": 300, "known_trials": 3, "unknown_trials": 1}
    comparison = report["paired_comparisons"][0]
    assert (comparison["arm_a"], comparison["arm_b"]) == ("baseline", "optimized")
    assert comparison["planned_pairs"] == 4
    assert comparison["known_pairs"] == 3
    assert comparison["independent_paired_tasks"] == 2
    assert comparison["unknown_or_missing_pairs"] == 1
    assert comparison["mean_task_success_delta_b_minus_a"] == -0.25
    assert comparison["wins_b"] == comparison["losses_b"] == comparison["ties"] == 1
    provider = next(group for group in report["groups"] if group["evidence_kind"] == "provider")
    assert provider["planned_trials"] == 1
    assert provider["success_fraction_of_plan"] is None


@pytest.mark.parametrize("invalid", ["duplicate", "not_run_success", "negative_usage", "infinite_cost", "missing_field", "recovery_without_fault", "invalid_enum"])
def test_invalid_input_cannot_overwrite_an_existing_report(tmp_path: Path, invalid: str):
    rows = [trial("a", 0, True, 1)]
    if invalid == "duplicate":
        rows.append(dict(rows[0]))
    elif invalid == "not_run_success":
        rows[0]["status"] = "not_run"
    elif invalid == "negative_usage":
        rows[0]["tokens_in"] = -1
    elif invalid == "infinite_cost":
        rows[0]["cost"] = float("inf")
    elif invalid == "missing_field":
        rows[0].pop("tokens_out")
    elif invalid == "invalid_enum":
        rows[0]["experiment"] = []
    else:
        rows[0]["recovered"] = True
    source = tmp_path / "invalid.json"
    source.write_text(json.dumps({"schema_version": 1, "source_pin": {}, "trials": rows}), encoding="utf-8")
    output = tmp_path / "report"
    output.mkdir()
    sentinel = output / "summary.json"
    sentinel.write_text("previous report", encoding="utf-8")

    assert main(["--input", str(source), "--output", str(output)]) == 2
    assert sentinel.read_text(encoding="utf-8") == "previous report"


@pytest.mark.parametrize("planned_trials", [8, 1.0, True, "1", None])
def test_planned_trials_must_be_an_integer_matching_all_rows(tmp_path: Path, planned_trials, capsys):
    source = tmp_path / "input.json"
    source.write_text(json.dumps({
        "schema_version": 1,
        "source_pin": {},
        "planned_trials": planned_trials,
        "trials": [trial("only-retained-success", 0, True, 1)],
    }), encoding="utf-8")
    output = tmp_path / "report"
    output.mkdir()
    sentinel = output / "summary.json"
    sentinel.write_text("previous complete report", encoding="utf-8")

    assert main(["--input", str(source), "--output", str(output)]) == 2
    assert "planned_trials" in capsys.readouterr().err
    assert sentinel.read_text(encoding="utf-8") == "previous complete report"


def test_recovery_uses_actual_interruptions_and_exports_labeled_figures(tmp_path: Path):
    output, report = run_report(tmp_path, [
        trial("good", 0, True, None, experiment="recovery", actually_interrupted=True, recovered=True,
              duplicate_effects=0, ledger_valid=True, failure_window="after_durable_result", fault_event_count=1),
        trial("unknown", 0, None, None, experiment="recovery", status="infra_error", actually_interrupted=True,
              ledger_valid=True, failure_window="effect_before_result", fault_event_count=1),
        trial("failed", 0, False, None, experiment="recovery", status="agent_failure", actually_interrupted=True,
              recovered=False, duplicate_effects=1, ledger_valid=False, failure_window="before_effect", fault_event_count=1),
        trial("control-a", 0, True, None, experiment="recovery", duplicate_effects=0, ledger_valid=True,
              failure_window="no_fault", fault_event_count=0),
        trial("control-b", 0, True, None, experiment="recovery", duplicate_effects=0, ledger_valid=True,
              failure_window="no_fault", fault_event_count=0),
    ])

    recovery = report["groups"][0]["recovery"]
    assert recovery["actually_interrupted_trials"] == 3
    assert recovery["reported_recovered_trials"] == recovery["clean_recovered_trials"] == 1
    assert recovery["reported_recovery_fraction"] == pytest.approx(1 / 3)
    assert recovery["clean_recovery_fraction"] == pytest.approx(1 / 3)
    assert recovery["unknown_recovery_trials"] == 1
    assert recovery["fault_events"]["known_total"] == 3
    assert report["groups"][0]["success_fraction_of_plan"] == 0.6
    assert len(report["recovery_windows"]) == 4
    assert {figure["path"] for figure in report["figures"]} == {
        "figures/module-comparison.png", "figures/budget-success-cost.png", "figures/recovery-windows.png",
    }
    for figure in report["figures"]:
        data = (output / figure["path"]).read_bytes()
        assert data.startswith(b"\x89PNG\r\n\x1a\n") and len(data) > 5000
    oral = (output / "oral.md").read_text(encoding="utf-8")
    assert "fixture" in oral and "未核验" in oral
    assert "1/3" in oral and "未运行" in oral
    assert "不能据此宣称显著提升" in oral


def test_recovery_separates_same_session_rate_from_from_start_controls(tmp_path: Path):
    output, report = run_report(tmp_path, [
        trial("task", 0, True, None, experiment="recovery", arm="continue_after_record",
              recovery_mode="continue", actually_interrupted=True, recovered=True,
              duplicate_effects=0, ledger_valid=True, failure_window="after_record", fault_event_count=1),
        trial("task", 1, None, None, experiment="recovery", arm="continue_after_record",
              recovery_mode="continue", status="not_run", tokens_in=None, tokens_out=None, elapsed_seconds=None,
              failure_window="after_record", fault_event_count=0),
        trial("task", 0, True, None, experiment="recovery", arm="restart_after_record",
              recovery_mode="restart", actually_interrupted=True, recovered=None, completed_from_start=True,
              tokens_in=1600, tokens_out=400, elapsed_seconds=11,
              ledger_valid=True, failure_window="after_record", fault_event_count=1),
        trial("task", 1, None, None, experiment="recovery", arm="restart_after_record",
              recovery_mode="restart", actually_interrupted=True, status="infra_error",
              tokens_in=None, tokens_out=None, elapsed_seconds=None,
              failure_window="after_record", fault_event_count=1),
        trial("task", 0, True, None, experiment="recovery", arm="no_fault", recovery_mode="none",
              failure_window="no_fault", fault_event_count=0),
    ], planned_trials=5)

    recovery = report["recovery"]
    assert recovery["planned_trials"] == 5 and recovery["actually_interrupted_trials"] == 3
    assert recovery["same_session_planned_trials"] == 2
    assert recovery["same_session_interrupted_trials"] == 1
    assert recovery["reported_recovery_fraction"] == recovery["clean_recovery_fraction"] == 1
    assert recovery["unknown_recovery_trials"] == recovery["unknown_clean_recovery_trials"] == 0
    assert recovery["rate_denominator"] == "same_session_interrupted_trials"
    assert recovery["recovery_not_applicable_trials"] == 2
    assert recovery["no_fault"]["planned_trials"] == 1
    assert recovery["no_fault"]["completed_trials"] == 1
    restart = recovery["from_start"]
    assert restart["planned_trials"] == 2 and restart["actually_interrupted_trials"] == 2
    assert restart["completed_trials"] == 1 and restart["unknown_completion_trials"] == 1
    assert restart["completion_fraction_of_plan"] == 0.5
    assert restart["usage"]["tokens_in"] == {"known_total": 1600, "known_trials": 1, "unknown_trials": 1}
    assert restart["usage"]["tokens_out"]["known_total"] == 400
    assert restart["usage"]["elapsed_seconds"]["known_total"] == 11
    window = next(item for item in report["recovery_windows"] if item["arm"] == "restart_after_record")
    assert window["same_session_interrupted_trials"] == 0 and window["reported_recovery_fraction"] is None
    assert window["unknown_recovery_trials"] == 0
    oral = (output / "oral.md").read_text(encoding="utf-8")
    assert "全部实际中断 3 次" in oral and "同 Session" in oral and "干净恢复 1/1" in oral
    assert "从头重做对照完成 1/2" in oral and "恢复字段为不适用" in oral
    assert "1600" in oral and "400" in oral and "11" in oral


def test_recovery_keeps_mixed_evidence_rates_separate(tmp_path: Path):
    _, report = run_report(tmp_path, [
        trial("fixture", 0, True, None, experiment="recovery", recovery_mode="continue",
              actually_interrupted=True, recovered=True, duplicate_effects=0, ledger_valid=True),
        trial("provider", 0, False, None, experiment="recovery", recovery_mode="continue", evidence_kind="provider",
              actually_interrupted=True, recovered=False, duplicate_effects=1, ledger_valid=False),
    ])

    recovery = report["recovery"]
    assert recovery["actually_interrupted_trials"] == 2
    assert recovery["reported_recovery_fraction"] is None and recovery["clean_recovery_fraction"] is None
    assert recovery["by_evidence"]["fixture"]["reported_recovery_fraction"] == 1
    assert recovery["by_evidence"]["provider"]["reported_recovery_fraction"] == 0


def test_unmeasured_and_mixed_currency_data_cannot_produce_zero_cost_or_clean_recovery(tmp_path: Path):
    output, report = run_report(tmp_path, [
        trial("usd", 0, True, 1, cost_currency="USD"),
        trial("cny", 0, True, 7, cost_currency="CNY"),
        trial("unmeasured", 0, None, None, status="not_run", evidence_kind="provider",
              cost_currency=None, tokens_in=None, tokens_out=None, elapsed_seconds=None),
        trial("unconfirmed", 0, True, None, experiment="recovery", actually_interrupted=True,
              recovered=True, evidence_kind="provider", failure_window="effect_before_result"),
    ])

    mixed = next(group for group in report["groups"] if group["experiment"] == "budget" and group["evidence_kind"] == "fixture")
    assert mixed["costs"]["USD"]["known_total"] == 1
    assert mixed["costs"]["CNY"]["known_total"] == 7
    assert mixed["costs"]["USD"]["per_successful_trial"] is None
    assert mixed["costs"]["CNY"]["per_successful_task_any"] is None
    unknown = next(group for group in report["groups"] if group["experiment"] == "budget" and group["evidence_kind"] == "provider")
    assert unknown["usage"]["tokens_in"]["known_total"] is None
    assert unknown["cost_unknown_trials"] == 1
    recovery = next(group for group in report["groups"] if group["experiment"] == "recovery")["recovery"]
    assert recovery["reported_recovery_fraction"] == 1
    assert recovery["clean_recovery_fraction"] is None
    assert recovery["unknown_clean_recovery_trials"] == 1
    oral = (output / "oral.md").read_text(encoding="utf-8")
    assert "预算策略 [fixture]" in oral and "预算策略 [provider]" in oral
    assert "尚不能确认" in oral


def test_empty_plan_exports_explicitly_unrun_artifacts(tmp_path: Path):
    output, report = run_report(tmp_path, [])
    assert report["planned_trials"] == 0
    assert report["groups"] == report["paired_comparisons"] == []
    assert all(figure["status"] == "not_run" for figure in report["figures"])
    assert "未运行" in (output / "oral.md").read_text(encoding="utf-8")


def test_foundation_trial_links_separate_validation_without_completing_experiments(tmp_path: Path):
    validation = {"path": "../receipt-validation.json", "sha256": "a" * 64}
    output, report = run_report(tmp_path, [trial("foundation", 0, True, None, experiment="product_trial")],
                                verification_record=validation, provenance_verified=True)

    assert report["groups"][0]["experiment"] == "product_trial"
    assert report["groups"][0]["successful_trials"] == 1
    assert all(figure["status"] == "not_run" for figure in report["figures"])
    assert report["verification_record"] == validation
    assert report["provenance_verified"] is False
    assert report["provenance_validation"] == "not_performed_by_report"
    oral = (output / "oral.md").read_text(encoding="utf-8")
    assert "产品链路首试 [fixture]" in oral
    assert "[调用方独立验证记录](../receipt-validation.json)" in oral


def test_factorial_contrasts_average_within_task_and_retain_unknown_quartets(tmp_path: Path):
    cells = [
        ("A", "retrieval_pre_read", False),
        ("B", "retrieval_pre_read", True),
        ("C", "metadata_on_demand", False),
        ("D", "metadata_on_demand", True),
    ]
    rows = []
    for task_id, repeat, outcomes in [
        ("task-a", 0, [False, True, False, True]),
        ("task-a", 1, [False, True, False, True]),
        ("task-b", 0, [False, False, False, True]),
        ("task-c", 0, [False, True, False, None]),
    ]:
        for (arm, strategy, memory_read), success in zip(cells, outcomes, strict=True):
            rows.append(trial(task_id, repeat, success, None, arm=arm, experiment="context_memory",
                              status="completed" if success is not None else "scorer_error",
                              context_strategy=strategy, memory_read=memory_read))
    _, report = run_report(tmp_path, rows)

    result = report["factorial_contrasts"][0]
    assert result["planned_quartets"] == 4
    assert result["known_complete_quartets"] == 3
    assert result["unknown_or_incomplete_quartets"] == 1
    assert result["independent_paired_tasks"] == 2
    assert result["success_contrasts"] == {
        "context_main_effect": 0.25,
        "memory_main_effect": 0.75,
        "context_at_memory_off": 0.0,
        "context_at_memory_on": 0.5,
        "interaction": 0.5,
    }
    assert result["status"] == "descriptive_only"
    assert result["significance_claim"] is False


def test_factorial_does_not_select_four_labeled_rows_and_hide_an_unclassified_cell(tmp_path: Path):
    rows = [trial("task", 0, True, None, experiment="context_memory", arm=f"arm-{index}",
                  context_strategy=strategy, memory_read=memory)
            for index, (strategy, memory) in enumerate([
                ("retrieval_pre_read", False), ("retrieval_pre_read", True),
                ("metadata_on_demand", False), ("metadata_on_demand", True),
            ])]
    rows.append(trial("task", 0, None, None, experiment="context_memory", arm="unclassified", status="not_run"))
    _, report = run_report(tmp_path, rows)

    result = report["factorial_contrasts"][0]
    assert result["planned_quartets"] == result["unknown_or_incomplete_quartets"] == 1
    assert result["rows_without_factor_metadata"] == 1
    assert result["known_complete_quartets"] == 0
    assert result["success_contrasts"] is None
