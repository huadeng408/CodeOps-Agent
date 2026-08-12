"""Compare receipt-bound frozen arms on the astropy-20 subset."""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Mapping

REPO_ROOT = Path(__file__).resolve().parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from eval.swebench_work.mechanism_report import EDIT_TOOLS  # noqa: E402
from eval.swebench_work.report_gate import (  # noqa: E402
    ARMS,
    DEFAULT_RECEIPT,
    FinalizedRun,
    ReportGateError,
    load_verified_runs,
)


@dataclass
class ArmResult:
    arm: str
    run_id: str
    source_ids: tuple[str, ...]
    eligible_ids: set[str]
    official_verdicts: dict[str, bool]
    outcomes: dict[str, bool]
    unmeasured_by_reason: dict[str, dict[str, dict[str, str]]]
    failures_by_reason: dict[str, dict[str, dict[str, str]]]
    excluded_contaminated: dict[str, str]
    empty_patches: set[str] = field(default_factory=set)
    edits_by_instance: dict[str, int] = field(default_factory=dict)

    @property
    def resolved(self) -> set[str]:
        return {instance_id for instance_id, resolved in self.outcomes.items() if resolved}

    @property
    def measured(self) -> set[str]:
        return set(self.outcomes)


def _thaw_mapping(value: Mapping[str, Any]) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, item in value.items():
        if isinstance(item, Mapping):
            result[str(key)] = _thaw_mapping(item)
        else:
            result[str(key)] = item
    return result


def _source_id(row: Mapping[str, Any]) -> str:
    return str(row.get("instance_id", row.get("source_id", "")))


def load_arm(run: FinalizedRun) -> ArmResult:
    """Build comparison data only from a supplied, validated run."""
    eligible = set(run.eligible_ids)
    result = ArmResult(
        arm=run.arm,
        run_id=run.run_id,
        source_ids=run.source_ids,
        eligible_ids=eligible,
        official_verdicts=dict(run.official_verdicts),
        outcomes=dict(run.outcomes),
        unmeasured_by_reason=_thaw_mapping(run.unmeasured_by_reason),
        failures_by_reason=_thaw_mapping(run.failures_by_reason),
        excluded_contaminated=dict(run.excluded_contaminated),
    )

    for row in run.predictions:
        instance_id = _source_id(row)
        if instance_id not in eligible:
            continue
        patch = str(row.get("model_patch") or "")
        if not patch.strip():
            result.empty_patches.add(instance_id)

    spans = run.trace_summary.get("spans")
    if not isinstance(spans, (list, tuple)):
        raise ReportGateError(f"trace-summary.json spans are invalid: {run.arm}")
    for span in spans:
        if not isinstance(span, Mapping):
            continue
        attributes_value = span.get("attributes")
        attributes = attributes_value if isinstance(attributes_value, Mapping) else {}
        instance_id = str(attributes.get("eval.instance_id") or "")
        tool = attributes.get("tool.name")
        if instance_id in eligible and tool in EDIT_TOOLS:
            result.edits_by_instance[instance_id] = (
                result.edits_by_instance.get(instance_id, 0) + 1
            )
    return result


def _arm_summary(arm: ArmResult) -> dict[str, Any]:
    eligible_outcomes = set(arm.outcomes) & arm.eligible_ids
    scorer_unmeasured = arm.unmeasured_by_reason.get("scorer", {})
    scorer_unmeasured_source = len(scorer_unmeasured)
    scorer_unmeasured_eligible = len(set(scorer_unmeasured) & arm.eligible_ids)
    failure_counts = {
        bucket: len(records)
        for bucket, records in sorted(arm.failures_by_reason.items())
    }
    resolved_eligible = sum(bool(arm.outcomes[instance_id]) for instance_id in eligible_outcomes)
    return {
        "source_total": len(arm.source_ids),
        "eligible_total": len(arm.eligible_ids),
        "headline_resolved": resolved_eligible,
        "headline_denominator": len(arm.source_ids),
        "outcome_eligible": len(eligible_outcomes),
        "resolved_eligible": resolved_eligible,
        "official_measured_eligible": len(set(arm.official_verdicts) & arm.eligible_ids),
        "scorer_unmeasured_source": scorer_unmeasured_source,
        "scorer_unmeasured_eligible": scorer_unmeasured_eligible,
        "failure_counts": failure_counts,
        "raw_diagnostics": {
            "official_measured_source": len(arm.official_verdicts),
            "outcome_source": len(arm.outcomes),
            "resolved_source": sum(bool(value) for value in arm.outcomes.values()),
        },
        "excluded_contaminated": dict(arm.excluded_contaminated),
        "unmeasured_by_reason": arm.unmeasured_by_reason,
        "failures_by_reason": arm.failures_by_reason,
    }


def compare(a: ArmResult, b: ArmResult) -> dict[str, Any]:
    """Pair failure-inclusive outcomes over the shared eligible cohort."""
    shared_eligible = a.eligible_ids & b.eligible_ids
    paired_ids = sorted(set(a.outcomes) & set(b.outcomes) & shared_eligible)
    baseline_only = sorted((set(a.outcomes) & shared_eligible) - set(b.outcomes))
    optimized_only = sorted((set(b.outcomes) & shared_eligible) - set(a.outcomes))
    a_only_wins = [
        instance_id
        for instance_id in paired_ids
        if a.outcomes[instance_id] and not b.outcomes[instance_id]
    ]
    b_only_wins = [
        instance_id
        for instance_id in paired_ids
        if b.outcomes[instance_id] and not a.outcomes[instance_id]
    ]
    both = [
        instance_id
        for instance_id in paired_ids
        if a.outcomes[instance_id] and b.outcomes[instance_id]
    ]
    neither = [
        instance_id
        for instance_id in paired_ids
        if not a.outcomes[instance_id] and not b.outcomes[instance_id]
    ]
    official_paired = sorted(
        set(a.official_verdicts) & set(b.official_verdicts) & shared_eligible
    )
    return {
        "cohort": {
            "source_total": len(a.source_ids),
            "eligible_total": len(shared_eligible),
        },
        "arms": {
            "baseline": _arm_summary(a),
            "optimized": _arm_summary(b),
        },
        "paired_instances": len(paired_ids),
        "paired_ids": paired_ids,
        "excluded_contaminated": sorted(
            set(a.excluded_contaminated) | set(b.excluded_contaminated)
        ),
        "unpaired": {
            "baseline_only": baseline_only,
            "optimized_only": optimized_only,
        },
        "baseline_resolved": sum(bool(a.outcomes[instance_id]) for instance_id in paired_ids),
        "optimized_resolved": sum(bool(b.outcomes[instance_id]) for instance_id in paired_ids),
        "both_resolved": both,
        "neither_resolved": neither,
        "discordant": {
            "baseline_only_resolved": a_only_wins,
            "optimized_only_resolved": b_only_wins,
        },
        "official_verdict_only_diagnostic": {
            "paired_instances": len(official_paired),
            "baseline_resolved": sum(
                bool(a.official_verdicts[instance_id]) for instance_id in official_paired
            ),
            "optimized_resolved": sum(
                bool(b.official_verdicts[instance_id]) for instance_id in official_paired
            ),
        },
        "empty_patch_counts": {
            "baseline": len(a.empty_patches),
            "optimized": len(b.empty_patches),
        },
        "instances_with_edits": {
            "baseline": len(a.edits_by_instance),
            "optimized": len(b.edits_by_instance),
        },
    }


def render(a: ArmResult, b: ArmResult, result: Mapping[str, Any]) -> str:
    baseline = result["arms"]["baseline"]
    optimized = result["arms"]["optimized"]
    paired = result["paired_instances"]
    lines = [
        "Harness uplift — receipt-bound comparison on the astropy-20 subset",
        "=" * 70,
        "",
        f"  arm A (baseline) : {a.run_id}",
        f"  arm B (optimized): {b.run_id}",
        "",
        "Declared-source headline (contaminated IDs never enter the numerator):",
        f"  arm A resolved: {baseline['headline_resolved']}/20; "
        f"excluded contaminated={len(baseline['excluded_contaminated'])}; "
        f"scorer-unmeasured source={baseline['scorer_unmeasured_source']} "
        f"(eligible={baseline['scorer_unmeasured_eligible']})",
        f"  arm B resolved: {optimized['headline_resolved']}/20; "
        f"excluded contaminated={len(optimized['excluded_contaminated'])}; "
        f"scorer-unmeasured source={optimized['scorer_unmeasured_source']} "
        f"(eligible={optimized['scorer_unmeasured_eligible']})",
        "",
        "Eligible cohort (fixed denominator; unmeasured disclosed, not treated false):",
        f"  arm A resolved: {baseline['resolved_eligible']}/19; "
        f"outcomes available={baseline['outcome_eligible']}/19",
        f"  arm B resolved: {optimized['resolved_eligible']}/19; "
        f"outcomes available={optimized['outcome_eligible']}/19",
        "",
        f"Paired eligible outcomes (both arms have an outcome): {paired}",
        f"  arm A resolved: {result['baseline_resolved']}/{paired}",
        f"  arm B resolved: {result['optimized_resolved']}/{paired}",
        "",
        "Discordant pairs (no p-value):",
        f"  B fixed what A missed: {len(result['discordant']['optimized_only_resolved'])}",
    ]
    for instance_id in result["discordant"]["optimized_only_resolved"]:
        lines.append(f"    + {instance_id}")
    lines.append(
        f"  A fixed what B missed: {len(result['discordant']['baseline_only_resolved'])}"
    )
    for instance_id in result["discordant"]["baseline_only_resolved"]:
        lines.append(f"    - {instance_id}")

    lines.extend(
        [
            "",
            "Mechanism check (eligible IDs only):",
            f"  empty patches      A={result['empty_patch_counts']['baseline']}  "
            f"B={result['empty_patch_counts']['optimized']}",
            f"  instances w/ edits A={result['instances_with_edits']['baseline']}  "
            f"B={result['instances_with_edits']['optimized']}",
            "",
            f"Current failure categories A={baseline['failure_counts']}",
            f"Current failure categories B={optimized['failure_counts']}",
        ]
    )
    if result["unpaired"]["baseline_only"] or result["unpaired"]["optimized_only"]:
        lines.extend(
            [
                "",
                "Scorer-unmeasured IDs excluded from the paired intersection:",
                f"  only outcome in A: {result['unpaired']['baseline_only']}",
                f"  only outcome in B: {result['unpaired']['optimized_only']}",
            ]
        )
    if result["excluded_contaminated"]:
        lines.extend(["", "Excluded as contaminated from every reported metric:"])
        reasons = {**a.excluded_contaminated, **b.excluded_contaminated}
        for instance_id in result["excluded_contaminated"]:
            lines.append(f"  ! {instance_id} — {reasons[instance_id]}")
    lines.extend(
        [
            "",
            "Reporting constraint: these are astropy-20 subset results, not a",
            "SWE-bench Verified score. This report intentionally gives the two",
            "discordant counts and no statistical p-value.",
        ]
    )
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verified-arms", type=Path, default=DEFAULT_RECEIPT)
    parser.add_argument("--json", type=Path, help="also write the comparison as JSON")
    args = parser.parse_args()

    try:
        runs = load_verified_runs(args.verified_arms)
        a = load_arm(runs["baseline"])
        b = load_arm(runs["optimized"])
        result = compare(a, b)
        payload = {
            "verified_arms": str(args.verified_arms),
            "runs": {
                arm: {
                    "run_id": runs[arm].run_id,
                    "manifest_sha256": runs[arm].manifest_sha256,
                    "checksums_sha256": runs[arm].checksums_sha256,
                    "official_verdicts": dict(runs[arm].official_verdicts),
                    "outcomes": dict(runs[arm].outcomes),
                    "unmeasured_by_reason": _thaw_mapping(runs[arm].unmeasured_by_reason),
                    "failures_by_reason": _thaw_mapping(runs[arm].failures_by_reason),
                    "scorer_evidence_scope": runs[arm].scorer_evidence_scope,
                }
                for arm in ARMS
            },
            "comparison": result,
        }
    except ReportGateError as exc:
        print(f"BLOCKED: {exc}")
        return 2

    print(render(a, b, result))
    if args.json:
        args.json.parent.mkdir(parents=True, exist_ok=True)
        args.json.write_text(
            json.dumps(payload, indent=2, ensure_ascii=False, sort_keys=True) + "\n",
            encoding="utf-8",
        )
        print(f"\nwrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
