"""Derived experiment reports; never a Session, billing, or Receipt authority."""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import math
import sys
from collections import Counter, defaultdict
from itertools import combinations
from pathlib import Path
from typing import Any


def _validate(payload: Any) -> None:
    if not isinstance(payload, dict) or type(payload.get("schema_version")) is not int or payload["schema_version"] != 1 or not isinstance(payload.get("source_pin"), dict) or not isinstance(payload.get("trials"), list):
        raise ValueError("expected experiment schema v1 with source_pin and trials")
    if "planned_trials" in payload and (
        type(payload["planned_trials"]) is not int or payload["planned_trials"] != len(payload["trials"])
    ):
        raise ValueError("planned_trials must be an integer matching the number of trial rows")
    verification = payload.get("verification_record")
    if verification is not None and (
        not isinstance(verification, dict)
        or not isinstance(verification.get("path"), str) or not verification["path"].strip()
        or not isinstance(verification.get("sha256"), str) or len(verification["sha256"]) != 64
        or any(char not in "0123456789abcdef" for char in verification["sha256"])
    ):
        raise ValueError("invalid caller verification record reference")
    required = {
        "task_id", "trial_id", "experiment", "arm", "repeat", "status", "success",
        "tokens_in", "tokens_out", "cost", "cost_currency", "elapsed_seconds",
        "budget_tokens", "actually_interrupted", "recovered", "duplicate_effects", "ledger_valid", "receipt_path",
    }
    identities, plans = set(), set()
    for index, row in enumerate(payload["trials"]):
        if not isinstance(row, dict) or not required <= row.keys():
            raise ValueError(f"trial {index}: missing required fields")
        for field in ("task_id", "trial_id", "arm"):
            if not isinstance(row[field], str) or not row[field].strip():
                raise ValueError(f"trial {index}: invalid {field}")
        for field, allowed in (
            ("experiment", {"product_trial", "context_memory", "budget", "recovery"}),
            ("status", {"completed", "agent_failure", "infra_error", "scorer_error", "budget_exceeded", "not_run"}),
            ("evidence_kind", {"provider", "fixture", "unknown"}),
        ):
            value = row.get(field, "unknown")
            if not isinstance(value, str) or value not in allowed:
                raise ValueError(f"trial {index}: invalid {field}")
        for field in ("success", "recovered", "ledger_valid"):
            if row[field] is not None and type(row[field]) is not bool:
                raise ValueError(f"trial {index}: invalid {field}")
        if row["actually_interrupted"] is not None and type(row["actually_interrupted"]) is not bool:
            raise ValueError(f"trial {index}: invalid interruption flag")
        for field in ("repeat", "budget_tokens", "tokens_in", "tokens_out", "duplicate_effects"):
            value = row[field]
            nullable = field in {"tokens_in", "tokens_out", "duplicate_effects"}
            if value is None and nullable:
                continue
            if type(value) is not int or value < (1 if field == "budget_tokens" else 0):
                raise ValueError(f"trial {index}: invalid {field}")
        for field in ("cost", "elapsed_seconds"):
            value = row[field]
            if value is not None and (type(value) not in {int, float} or not math.isfinite(value) or value < 0):
                raise ValueError(f"trial {index}: invalid {field}")
        if row["cost_currency"] is not None and (not isinstance(row["cost_currency"], str) or not row["cost_currency"].strip()):
            raise ValueError(f"trial {index}: invalid cost currency")
        if not isinstance(row["receipt_path"], str):
            raise ValueError(f"trial {index}: invalid Receipt locator")
        if row.get("failure_window") is not None and not isinstance(row["failure_window"], str):
            raise ValueError(f"trial {index}: invalid failure window")
        if "recovery_mode" in row and (not isinstance(row["recovery_mode"], str) or row["recovery_mode"] not in {"none", "continue", "restart"}):
            raise ValueError(f"trial {index}: invalid recovery mode")
        if row.get("fault_event_count") is not None and (type(row["fault_event_count"]) is not int or row["fault_event_count"] < 0):
            raise ValueError(f"trial {index}: invalid fault event count")
        if "context_strategy" in row or "memory_read" in row:
            if row.get("context_strategy") not in ("retrieval_pre_read", "metadata_on_demand") or type(row.get("memory_read")) is not bool:
                raise ValueError(f"trial {index}: incomplete or invalid factorial metadata")
        if row["success"] is True and row["status"] != "completed" or row["status"] == "not_run" and row["success"] is not None:
            raise ValueError(f"trial {index}: outcome contradicts execution status")
        if row["recovered"] is True and not row["actually_interrupted"]:
            raise ValueError(f"trial {index}: recovery requires an actual interruption")
        plan = (row["experiment"], row["arm"], row["budget_tokens"], row.get("evidence_kind", "unknown"), row["task_id"], row["repeat"])
        if row["trial_id"] in identities or plan in plans:
            raise ValueError(f"trial {index}: duplicate trial identity or planned cell")
        identities.add(row["trial_id"])
        plans.add(plan)


def _usage_summary(rows: list[dict[str, Any]]) -> dict[str, Any]:
    return {
        field: {
            "known_total": sum(row[field] for row in rows if row[field] is not None) if any(row[field] is not None for row in rows) else None,
            "known_trials": sum(row[field] is not None for row in rows),
            "unknown_trials": sum(row[field] is None for row in rows),
        }
        for field in ("tokens_in", "tokens_out", "elapsed_seconds")
    }


def _summarize(rows: list[dict[str, Any]]) -> dict[str, Any]:
    tasks = {row["task_id"] for row in rows}
    successes = [row for row in rows if row["success"] is True]
    known = sum(row["success"] is not None for row in rows)
    successful_tasks = {row["task_id"] for row in successes}
    costs = {}
    for currency in sorted({row["cost_currency"] for row in rows if row["cost_currency"]}):
        amounts = [row["cost"] for row in rows if row["cost_currency"] == currency and row["cost"] is not None]
        total = sum(amounts) if amounts else None
        complete = len(amounts) == len(rows)
        costs[currency] = {
            "known_total": total,
            "known_trials": len(amounts),
            "unpriced_or_other_currency_trials": len(rows) - len(amounts),
            "per_successful_trial": total / len(successes) if complete and successes else None,
            "per_successful_task_any": total / len(successful_tasks) if complete and successful_tasks else None,
        }
    return {
        "planned_trials": len(rows),
        "independent_tasks": len(tasks),
        "successful_trials": len(successes),
        "known_outcomes": known,
        "unknown_outcomes": len(rows) - known,
        "success_fraction_of_plan": len(successes) / len(rows) if known else None,
        "success_rate_known_outcomes": len(successes) / known if known else None,
        "tasks_with_any_success": len(successful_tasks),
        "task_success_rule": "at_least_one_success_across_planned_repeats",
        "status_counts": dict(sorted(Counter(row["status"] for row in rows).items())),
        "costs": costs,
        "cost_unknown_trials": sum(row["cost"] is None or not row["cost_currency"] for row in rows),
        "recovery": _recovery_summary(rows),
        "usage": _usage_summary(rows),
    }


def _recovery_mode(row: dict[str, Any]) -> str:
    if "recovery_mode" in row:
        return row["recovery_mode"]
    return "none" if row.get("failure_window") == "no_fault" or row["arm"] == "no_fault" else "continue"


def _completion_summary(rows: list[dict[str, Any]]) -> dict[str, Any]:
    known = sum(row["success"] is not None for row in rows)
    completed = sum(row["success"] is True for row in rows)
    return {
        "planned_trials": len(rows),
        "actually_interrupted_trials": sum(row["actually_interrupted"] is True for row in rows),
        "interruption_unknown_trials": sum(row["actually_interrupted"] is None for row in rows),
        "completed_trials": completed,
        "known_completion_trials": known,
        "unknown_completion_trials": len(rows) - known,
        "completion_fraction_of_plan": completed / len(rows) if known else None,
        "usage": _usage_summary(rows),
    }


def _recovery_summary(rows: list[dict[str, Any]]) -> dict[str, Any]:
    interrupted = [row for row in rows if row["actually_interrupted"]]
    continuing = [row for row in rows if _recovery_mode(row) == "continue"]
    same_session = [row for row in continuing if row["actually_interrupted"]]
    restarting = [row for row in rows if _recovery_mode(row) == "restart"]
    controls = [row for row in rows if _recovery_mode(row) == "none"]
    recovered = sum(row["recovered"] is True for row in same_session)
    clean = sum(row["recovered"] is True and row["success"] is True and row["ledger_valid"] is True and row["duplicate_effects"] == 0 for row in same_session)
    known_clean = sum(
        row["recovered"] is False or row["success"] is False or row["ledger_valid"] is False
        or row["duplicate_effects"] is not None and row["duplicate_effects"] > 0
        or row["recovered"] is True and row["success"] is True and row["ledger_valid"] is True and row["duplicate_effects"] == 0
        for row in same_session
    )
    unknown = sum(row["recovered"] is None for row in same_session)
    faults = [row["fault_event_count"] for row in rows if row.get("fault_event_count") is not None]
    duplicates = [row["duplicate_effects"] for row in rows if row["duplicate_effects"] is not None]
    return {
        "planned_trials": len(rows),
        "successful_trials": sum(row["success"] is True for row in rows),
        "unknown_outcomes": sum(row["success"] is None for row in rows),
        "actually_interrupted_trials": len(interrupted),
        "actually_interrupted_tasks": len({row["task_id"] for row in interrupted}),
        "not_interrupted_trials": sum(row["actually_interrupted"] is False for row in rows),
        "interruption_unknown_trials": sum(row["actually_interrupted"] is None for row in rows),
        "same_session_planned_trials": len(continuing),
        "same_session_interrupted_trials": len(same_session),
        "legacy_assumed_continue_trials": sum("recovery_mode" not in row for row in continuing),
        "legacy_mode_rule": "missing recovery_mode means continue unless explicitly no_fault",
        "recovery_not_applicable_trials": len(restarting),
        "from_start": _completion_summary(restarting),
        "no_fault": _completion_summary(controls),
        "reported_recovered_trials": recovered,
        "clean_recovered_trials": clean,
        "unknown_recovery_trials": unknown,
        "unknown_clean_recovery_trials": len(same_session) - known_clean,
        "reported_recovery_fraction": recovered / len(same_session) if same_session and unknown < len(same_session) else None,
        "clean_recovery_fraction": clean / len(same_session) if known_clean else None,
        "rate_denominator": "same_session_interrupted_trials",
        "state_checks_denominator": "all_planned_trials",
        "duplicate_effects": {"known_total": sum(duplicates) if duplicates else None, "unknown_trials": len(rows) - len(duplicates)},
        "invalid_ledger_trials": sum(row["ledger_valid"] is False for row in rows),
        "unknown_ledger_trials": sum(row["ledger_valid"] is None for row in rows),
        "fault_events": {"known_total": sum(faults) if faults else None, "unknown_trials": len(rows) - len(faults)},
    }


def _recovery_windows(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    windows: dict[tuple, list[dict]] = defaultdict(list)
    for row in rows:
        if row["experiment"] == "recovery":
            windows[(row["arm"], row.get("failure_window") or "unspecified", row.get("evidence_kind", "unknown"), row["budget_tokens"])].append(row)
    return [
        {"arm": key[0], "failure_window": key[1], "evidence_kind": key[2], "budget_tokens": key[3], "planned_trials": len(items), **_recovery_summary(items)}
        for key, items in sorted(windows.items())
    ]


def _paired_comparisons(grouped: dict) -> list[dict[str, Any]]:
    cohorts: dict[tuple, dict[str, list[dict]]] = defaultdict(dict)
    for (experiment, arm, budget, kind), rows in grouped.items():
        cohorts[(experiment, budget, kind)][arm] = rows
    comparisons = []
    for (experiment, budget, kind), arms in sorted(cohorts.items()):
        for arm_a, arm_b in combinations(sorted(arms), 2):
            left = {(row["task_id"], row["repeat"]): row for row in arms[arm_a]}
            right = {(row["task_id"], row["repeat"]): row for row in arms[arm_b]}
            differences: dict[str, list[int]] = defaultdict(list)
            wins = losses = ties = 0
            for key in left.keys() & right.keys():
                if left[key]["success"] is None or right[key]["success"] is None:
                    continue
                delta = int(right[key]["success"]) - int(left[key]["success"])
                differences[key[0]].append(delta)
                wins += delta > 0
                losses += delta < 0
                ties += delta == 0
            known = wins + losses + ties
            comparisons.append({
                "experiment": experiment, "budget_tokens": budget, "evidence_kind": kind,
                "arm_a": arm_a, "arm_b": arm_b,
                "planned_pairs": len(left.keys() | right.keys()),
                "known_pairs": known,
                "unknown_or_missing_pairs": len(left.keys() | right.keys()) - known,
                "independent_paired_tasks": len(differences),
                "wins_b": wins, "losses_b": losses, "ties": ties,
                "mean_task_success_delta_b_minus_a": (
                    sum(sum(values) / len(values) for values in differences.values()) / len(differences)
                    if differences else None
                ),
            })
    return comparisons


def _factorial_contrasts(rows: list[dict[str, Any]]) -> list[dict[str, Any]]:
    cohorts: dict[tuple, list[dict]] = defaultdict(list)
    for row in rows:
        if row["experiment"] == "context_memory":
            cohorts[(row["budget_tokens"], row.get("evidence_kind", "unknown"))].append(row)
    reports = []
    for (budget, kind), cohort in sorted(cohorts.items()):
        quartets: dict[tuple, dict[tuple, list[dict]]] = {}
        missing_factors = 0
        unclassified = set()
        for row in cohort:
            cells = quartets.setdefault((row["task_id"], row["repeat"]), {})
            if "context_strategy" not in row or "memory_read" not in row:
                missing_factors += 1
                unclassified.add((row["task_id"], row["repeat"]))
                continue
            cells.setdefault((row["context_strategy"], row["memory_read"]), []).append(row)
        task_results: dict[str, list[dict[str, float]]] = defaultdict(list)
        ambiguous = 0
        for (task, _repeat), cells in quartets.items():
            if (task, _repeat) in unclassified:
                continue
            if any(len(values) != 1 for values in cells.values()):
                ambiguous += 1
                continue
            if len(cells) != 4 or any(values[0]["success"] is None for values in cells.values()):
                continue
            r0 = int(cells[("retrieval_pre_read", False)][0]["success"])
            r1 = int(cells[("retrieval_pre_read", True)][0]["success"])
            m0 = int(cells[("metadata_on_demand", False)][0]["success"])
            m1 = int(cells[("metadata_on_demand", True)][0]["success"])
            task_results[task].append({
                "context_main_effect": ((m0 - r0) + (m1 - r1)) / 2,
                "memory_main_effect": ((r1 - r0) + (m1 - m0)) / 2,
                "context_at_memory_off": float(m0 - r0),
                "context_at_memory_on": float(m1 - r1),
                "interaction": float((m1 - r1) - (m0 - r0)),
            })
        per_task: list[dict[str, Any]] = [
            {"task_id": task, "known_complete_quartets": len(values), "success_contrasts": {
                name: sum(value[name] for value in values) / len(values) for name in values[0]
            }}
            for task, values in sorted(task_results.items())
        ]
        complete = sum(len(values) for values in task_results.values())
        reports.append({
            "budget_tokens": budget, "evidence_kind": kind,
            "status": "descriptive_only" if per_task else "insufficient_data",
            "planned_quartets": len(quartets), "known_complete_quartets": complete,
            "unknown_or_incomplete_quartets": len(quartets) - complete,
            "ambiguous_quartets": ambiguous, "rows_without_factor_metadata": missing_factors,
            "planned_independent_tasks": len({task for task, _repeat in quartets}),
            "independent_paired_tasks": len(per_task),
            "success_contrasts": {
                name: sum(task["success_contrasts"][name] for task in per_task) / len(per_task)
                for name in per_task[0]["success_contrasts"]
            } if per_task else None,
            "per_task": per_task,
            "weighting": "average paired repeats within task, then weight independent tasks equally",
            "definitions": "context=metadata minus retrieval; memory=on minus off; interaction=context_on minus context_off; binary success differences",
            "significance_claim": False,
        })
    return reports


def _write_figures(report: dict[str, Any], output: Path) -> list[dict[str, Any]]:
    from matplotlib.backends.backend_agg import FigureCanvasAgg
    from matplotlib.figure import Figure

    figures = output / "figures"
    figures.mkdir(exist_ok=True)
    scope = ", ".join(sorted({group["evidence_kind"] for group in report["groups"]})) or "no evidence"
    footer = f"DERIVED rows | Receipt verification separate | evidence: {scope} | pilot; no significance claim"
    if report["verification_record"]:
        footer += f" | caller record: {report['verification_record']['path']}"
    manifests: list[dict[str, Any]] = []

    def save(figure, name: str, status: str, panels=None):
        FigureCanvasAgg(figure)
        figure.text(0.02, 0.015, footer, fontsize=8, color="#555555", wrap=True)
        figure.savefig(figures / name, dpi=150, bbox_inches="tight")
        entry: dict[str, Any] = {"path": "figures/" + name, "status": status, "evidence_scope": scope}
        if panels is not None:
            entry["panels"] = panels
        manifests.append(entry)

    def unavailable(axis, text="No valid measurements; unknown values are not plotted."):
        axis.text(0.5, 0.5, text, ha="center", va="center", transform=axis.transAxes, wrap=True)
        axis.set_axis_off()

    module = [group for group in report["groups"] if group["experiment"] == "context_memory"]
    figure = Figure(figsize=(11, max(4, len(module) * 0.65 + 2.5)))
    axis = figure.add_subplot(1, 1, 1)
    axis.set_title("Context / Memory: confirmed outcomes over the full planned denominator")
    if module:
        labels = [f"{group['arm']} [{group['evidence_kind']}] B={group['budget_tokens']}\nn={group['planned_trials']}, tasks={group['independent_tasks']}" for group in module]
        good = [group["successful_trials"] / group["planned_trials"] for group in module]
        bad = [(group["known_outcomes"] - group["successful_trials"]) / group["planned_trials"] for group in module]
        missing = [group["unknown_outcomes"] / group["planned_trials"] for group in module]
        axis.barh(labels, good, label="Confirmed success", color="#0072B2")
        axis.barh(labels, bad, left=good, label="Known unsuccessful", color="#D55E00")
        axis.barh(labels, missing, left=[a + b for a, b in zip(good, bad)], label="Unknown / not run", color="#BBBBBB", hatch="//")
        axis.set_xlim(0, 1)
        axis.set_xlabel("Fraction of planned trials; unknown is not a measured failure")
        axis.legend(loc="upper center", bbox_to_anchor=(0.5, -0.15), ncol=3)
    else:
        unavailable(axis, "Context / Memory experiment not run.")
    figure.subplots_adjust(left=0.36, bottom=0.28)
    save(figure, "module-comparison.png", "measured" if any(group["known_outcomes"] for group in module) else "insufficient_data" if module else "not_run")

    budget = [group for group in report["groups"] if group["experiment"] == "budget"]
    currencies = sorted({currency for group in budget for currency, cost in group["costs"].items() if cost["known_total"] is not None})
    metrics = [("admitted_tokens", None), ("confirmed_total_tokens", None), ("elapsed_seconds", None)] + [("cost_per_successful_trial", currency) for currency in (currencies or [None])]
    panel_rows = math.ceil(len(metrics) / 2)
    figure = Figure(figsize=(13, 5 * panel_rows))
    panels = []
    for index, (metric, currency) in enumerate(metrics):
        axis = figure.add_subplot(panel_rows, 2, index + 1)
        titles = {
            "admitted_tokens": "Quality vs admitted token ceiling",
            "confirmed_total_tokens": "Quality vs actual confirmed tokens",
            "elapsed_seconds": "Quality vs actual end-to-end latency",
            "cost_per_successful_trial": f"Cost per successful trial ({currency or 'unavailable'})",
        }
        units = {
            "admitted_tokens": "admitted_token_ceiling_per_trial",
            "confirmed_total_tokens": "confirmed_input_plus_output_tokens_per_trial_not_money",
            "elapsed_seconds": "mean_end_to_end_seconds_per_trial",
            "cost_per_successful_trial": currency or "monetary_price_unknown",
        }
        axis.set_title(titles[metric])
        points: list[dict[str, Any]] = []
        for group in budget:
            x, y = group["budget_tokens"], group["success_fraction_of_plan"]
            if metric in {"confirmed_total_tokens", "elapsed_seconds"}:
                fields = ("tokens_in", "tokens_out") if metric == "confirmed_total_tokens" else ("elapsed_seconds",)
                if group["unknown_outcomes"] or any(group["usage"][field]["known_trials"] != group["planned_trials"] for field in fields):
                    continue
                x = sum(group["usage"][field]["known_total"] for field in fields) / group["planned_trials"]
            elif metric == "cost_per_successful_trial":
                y = group["costs"].get(currency, {}).get("per_successful_trial")
            if y is None or not math.isfinite(x):
                continue
            points.append({key: group[key] for key in ("arm", "evidence_kind", "budget_tokens", "planned_trials", "independent_tasks")} | {"x": x, "y": y})
            axis.scatter(x, y, s=50, label=f"{group['arm']} [{group['evidence_kind']}] B={group['budget_tokens']}\nn={group['planned_trials']}, tasks={group['independent_tasks']}")
        if points:
            axis.set_xlabel({
                "admitted_tokens": "Admitted token ceiling per trial",
                "confirmed_total_tokens": "Mean confirmed input + output tokens / trial (not money)",
                "elapsed_seconds": "Mean end-to-end elapsed seconds / trial",
                "cost_per_successful_trial": "Admitted token ceiling per trial",
            }[metric])
            axis.set_ylabel(f"{currency} / successful trial; all costs known" if metric == "cost_per_successful_trial" else "Confirmed successes / planned trials")
            if metric != "cost_per_successful_trial":
                axis.set_ylim(-0.03, 1.03)
            axis.legend(fontsize=7)
            axis.text(0, -0.23, f"Groups omitted for missing required measurements/outcomes: {len(budget) - len(points)}", transform=axis.transAxes, fontsize=8)
        else:
            unavailable(axis, "Budget experiment not run." if not budget else "No valid measurements; cost/usage unknown is not zero.")
        panels.append({"metric": metric, "unit": units[metric], "points": points, "omitted_groups": len(budget) - len(points)})
    figure.subplots_adjust(bottom=0.12, hspace=0.55, wspace=0.35)
    save(figure, "budget-success-cost.png", "measured" if any(group["known_outcomes"] for group in budget) else "insufficient_data" if budget else "not_run", panels=panels)

    windows = report["recovery_windows"]
    figure = Figure(figsize=(22, max(4, len(windows) * 0.65 + 2.8)))
    axis = figure.add_subplot(1, 1, 1)
    axis.set_title("Failure windows: same-Session recovery and from-start completion have separate denominators", pad=18)
    axis.set_axis_off()
    if windows:
        cells = []
        for window in windows:
            duplicate = window["duplicate_effects"]
            same_count = window["same_session_interrupted_trials"]
            restart = window["from_start"]
            restart_count = restart["planned_trials"]
            restart_usage = restart["usage"]
            tokens_complete = restart_count and all(restart_usage[field]["unknown_trials"] == 0 for field in ("tokens_in", "tokens_out"))
            tokens = f"{sum(restart_usage[field]['known_total'] for field in ('tokens_in', 'tokens_out')) / restart_count:g}" if tokens_complete else "?" if restart_count else "N/A"
            seconds = f"{restart_usage['elapsed_seconds']['known_total'] / restart_count:g}" if restart_count and not restart_usage["elapsed_seconds"]["unknown_trials"] else "?" if restart_count else "N/A"
            cells.append([
                window["failure_window"], f"{window['arm']} [{window['evidence_kind']}] B={window['budget_tokens']}",
                str(window["planned_trials"]), str(window["actually_interrupted_trials"]),
                f"{window['successful_trials']}/{window['planned_trials']}; ?={window['unknown_outcomes']}",
                f"{window['clean_recovered_trials']}/{same_count}; ?={window['unknown_clean_recovery_trials']}" if same_count else "N/A",
                f"{restart['completed_trials']}/{restart_count}; ?={restart['unknown_completion_trials']}" if restart_count else "N/A",
                tokens, seconds,
                f"{duplicate['known_total'] if duplicate['known_total'] is not None else '?'}; ?={duplicate['unknown_trials']}",
                f"{window['invalid_ledger_trials']}; ?={window['unknown_ledger_trials']}",
            ])
        table = axis.table(cellText=cells, colLabels=["Window", "Arm / evidence", "Plan", "All faults", "Task OK", "Same Session clean", "From-start OK", "Restart tokens/trial", "Restart sec/trial", "Duplicates", "Ledger invalid"], loc="center", cellLoc="center", colWidths=[0.10, 0.21, 0.035, 0.05, 0.08, 0.10, 0.08, 0.09, 0.085, 0.08, 0.08])
        table.auto_set_font_size(False)
        table.set_fontsize(8)
        table.scale(1, 2)
    else:
        unavailable(axis, "Recovery experiment not run.")
    figure.text(0.04, 0.07, "? = unknown. N/A = not applicable. Same-Session rates use interrupted continue trials only; restart is a completion control.\nRestart token/elapsed means require complete measurements and include both attempts; tokens are not money. Clean requires task success, valid Ledger and zero duplicates.", fontsize=8)
    figure.subplots_adjust(left=0.04, right=0.96, bottom=0.16)
    save(figure, "recovery-windows.png", "measured" if any(window["actually_interrupted_trials"] for window in windows) else "insufficient_data" if windows else "not_run")
    return manifests


def _oral(report: dict[str, Any]) -> str:
    rows = report["planned_trials"]
    scopes = ", ".join(sorted({group["evidence_kind"] for group in report["groups"]})) or "unknown"
    lines = [
        "# 实验口述稿（约两分钟）", "",
        f"这份报告面向 Agent 产品链路中的 Context／Memory、固定预算执行策略，以及故障窗口恢复。报告保留了 {rows} 个计划尝试，重复运行按 task_id 聚合，不能当成额外的独立任务。",
        f"当前输入证据标签为 {scopes}。标签和收据来源在本报告内未核验；fixture 只验证工程与分析链路，不能代表真实模型能力，provider 标签也不能代替独立 Receipt 核验。", "",
    ]
    if report["verification_record"]:
        reference = report["verification_record"]
        lines.append(f"[调用方独立验证记录]({reference['path']})；输入引用的记录 SHA-256：`{reference['sha256']}`。本报告仅链接该记录，不以输入中的 true 标志升级证据。")
    for experiment, label in (("product_trial", "产品链路首试"), ("context_memory", "Context／Memory"), ("budget", "预算策略"), ("recovery", "故障恢复")):
        experiment_groups = [group for group in report["groups"] if group["experiment"] == experiment]
        if not experiment_groups:
            lines.append(f"{label}实验未运行，没有可报告的实际对照。")
            continue
        for kind in sorted({group["evidence_kind"] for group in experiment_groups}):
            groups = [group for group in experiment_groups if group["evidence_kind"] == kind]
            known = sum(group["known_outcomes"] for group in groups)
            planned = sum(group["planned_trials"] for group in groups)
            successes = sum(group["successful_trials"] for group in groups)
            if not known:
                lines.append(f"{label} [{kind}] 计划 {planned} 次，目前无有效结果；未知或未运行项仍留在分母中。")
                if experiment != "recovery":
                    continue
            else:
                lines.append(f"{label} [{kind}] 记录 {planned} 次计划尝试，确认成功 {successes} 次，结果未知 {planned - known} 次；实验臂与重复结果见图表。")
            if experiment == "budget":
                if not any(cost["per_successful_trial"] is not None for group in groups for cost in group["costs"].values()):
                    lines.append("费用记录不完整，不能给出每成功任务金额；没有用模型公开标价替代账单。")
            if experiment == "recovery":
                all_faults = sum(group["recovery"]["actually_interrupted_trials"] for group in groups)
                no_fault = sum(group["recovery"]["no_fault"]["planned_trials"] for group in groups)
                interrupted = sum(group["recovery"]["same_session_interrupted_trials"] for group in groups)
                clean = sum(group["recovery"]["clean_recovered_trials"] for group in groups)
                unknown = sum(group["recovery"]["unknown_clean_recovery_trials"] for group in groups)
                lines.append(f"全部实际中断 {all_faults} 次；无故障对照保留 {no_fault} 次计划尝试。")
                if not interrupted:
                    lines.append("没有实际中断的同 Session 续行尝试，因此没有同 Session 恢复率。")
                elif unknown == interrupted:
                    lines.append(f"同 Session 续行实际中断 {interrupted} 次，尚不能确认无重复副作用、Ledger 有效且任务成功的恢复结果。")
                else:
                    lines.append(f"同 Session 已确认干净恢复 {clean}/{interrupted}，另有 {unknown} 次尚不能确认；分母仅实际中断的 continue 尝试。")
                for group in groups:
                    restart = group["recovery"]["from_start"]
                    if restart["planned_trials"]:
                        usage = restart["usage"]
                        totals = {field: value["known_total"] if value["known_total"] is not None else "unknown" for field, value in usage.items()}
                        lines.append(f"从头重做对照完成 {restart['completed_trials']}/{restart['planned_trials']} [{group['arm']}, B={group['budget_tokens']}]，结果未知 {restart['unknown_completion_trials']} 次；恢复字段为不适用，未计入同 Session 恢复分母。已确认输入／输出 token 合计 {totals['tokens_in']}／{totals['tokens_out']}，用量未知条数 {usage['tokens_in']['unknown_trials']}／{usage['tokens_out']['unknown_trials']}；端到端已知耗时合计 {totals['elapsed_seconds']} 秒，耗时未知 {usage['elapsed_seconds']['unknown_trials']} 条。这里的行计量包含原尝试与新 Session，不把 token 当金额。")
    lines += ["", "同任务比较保留配对缺口，未知费用和用量不按零处理，币种分别核算。每成功独立任务成本采用至少一次成功的明确规则，同时保留每成功尝试成本。这是小样本试验，不能据此宣称显著提升，也没有由此完成完整发布验收。", ""]
    if report["factorial_contrasts"]:
        lines.append("Context／Memory 四格交互仅使用显式因素且结果已知的完整配对，先在任务内平均重复，再按独立任务等权汇总；缺格与未知项保留计数，不按实验臂名字猜因素，也不作显著性结论。")
    return "\n\n".join(line for line in lines if line) + "\n"


def write_report(payload: dict[str, Any], output_dir: str | Path) -> dict[str, Any]:
    """Aggregate supplied trial rows without upgrading their evidence status."""
    _validate(payload)
    canonical_input = json.dumps(payload, sort_keys=True, ensure_ascii=False, allow_nan=False)
    grouped: dict[tuple, list[dict[str, Any]]] = defaultdict(list)
    for row in payload["trials"]:
        grouped[(row["experiment"], row["arm"], row["budget_tokens"], row.get("evidence_kind", "unknown"))].append(row)
    recovery_rows = [row for row in payload["trials"] if row["experiment"] == "recovery"]
    recovery = _recovery_summary(recovery_rows)
    kinds = sorted({row.get("evidence_kind", "unknown") for row in recovery_rows})
    recovery["by_evidence"] = {kind: _recovery_summary([row for row in recovery_rows if row.get("evidence_kind", "unknown") == kind]) for kind in kinds}
    recovery["evidence_scope"] = kinds
    if len(kinds) > 1:
        recovery["reported_recovery_fraction"] = recovery["clean_recovery_fraction"] = None
        recovery["from_start"]["completion_fraction_of_plan"] = recovery["no_fault"]["completion_fraction_of_plan"] = None
        recovery["rate_unavailable_reason"] = "mixed_evidence_not_pooled"
    report = {
        "schema_version": 1,
        "source_pin": payload["source_pin"],
        "input_sha256": hashlib.sha256(canonical_input.encode("utf-8")).hexdigest(),
        "provenance_verified": False,
        "provenance_validation": "not_performed_by_report",
        "verification_record": payload.get("verification_record"),
        "evidence_note": "Derived rows; Receipt verification is separate. verification_record references the caller's independent evidence without promoting its claims.",
        "planned_trials": len(payload["trials"]),
        "paired_comparisons": _paired_comparisons(grouped),
        "factorial_contrasts": _factorial_contrasts(payload["trials"]),
        "recovery": recovery,
        "recovery_windows": _recovery_windows(payload["trials"]),
        "groups": [
            {"experiment": key[0], "arm": key[1], "budget_tokens": key[2], "evidence_kind": key[3], **_summarize(rows)}
            for key, rows in sorted(grouped.items())
        ],
    }
    output = Path(output_dir)
    output.mkdir(parents=True, exist_ok=True)
    report["figures"] = _write_figures(report, output)
    (output / "oral.md").write_text(_oral(report), encoding="utf-8")
    (output / "summary.json").write_text(json.dumps(report, ensure_ascii=False, indent=2, allow_nan=False) + "\n", encoding="utf-8")
    fields = sorted({key for row in payload["trials"] for key in row})
    with (output / "trials.csv").open("w", encoding="utf-8-sig", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=fields)
        writer.writeheader()
        writer.writerows(payload["trials"])
    return report


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args(argv)
    try:
        write_report(json.loads(args.input.read_text(encoding="utf-8-sig")), args.output)
    except (OSError, ValueError) as exc:
        print(f"experiment report: {exc}", file=sys.stderr)
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
