"""Explain the frozen harness-uplift result from receipt-bound artifacts."""

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

from eval.harness.validate import patched_paths  # noqa: E402
from eval.swebench_work.report_gate import (  # noqa: E402
    ARMS,
    DEFAULT_RECEIPT,
    FinalizedRun,
    ReportGateError,
    load_verified_runs,
)

SEARCH_TOOLS = frozenset({"Grep", "Read", "Glob", "Bash", "LS", "SearchKnowledge"})
EDIT_TOOLS = frozenset({"Edit", "Write", "MultiEdit", "str_replace_editor"})
# Control-plane tools are counted for auditability but do not represent
# repository search or edits when reporting the mechanism ratio.
AUXILIARY_TOOLS = frozenset({"Skill"})


@dataclass
class ArmMechanism:
    arm: str
    run_id: str = ""
    search_calls: int = 0
    edit_calls: int = 0
    tool_counts: dict[str, int] = field(default_factory=dict)
    chat_rounds: dict[str, int] = field(default_factory=dict)
    instances_with_edits: set[str] = field(default_factory=set)
    instances_seen: set[str] = field(default_factory=set)
    empty_patches: set[str] = field(default_factory=set)
    non_empty_patches: set[str] = field(default_factory=set)
    localization_hit_rank: dict[str, int] = field(default_factory=dict)
    localization_available: set[str] = field(default_factory=set)

    @property
    def search_to_edit(self) -> str:
        if self.edit_calls == 0:
            return f"{self.search_calls}:0"
        return f"{self.search_calls / self.edit_calls:.1f}:1"

    @property
    def max_chat_round(self) -> int:
        return max(self.chat_rounds.values(), default=0)


def _source_id(row: Mapping[str, Any]) -> str:
    return str(row.get("instance_id", row.get("source_id", "")))


def load_arm(run: FinalizedRun) -> ArmMechanism:
    """Aggregate mechanism evidence only for the run's eligible cohort."""
    mechanism = ArmMechanism(arm=run.arm, run_id=run.run_id)
    eligible = set(run.eligible_ids)

    spans = run.trace_summary.get("spans")
    if not isinstance(spans, (list, tuple)):
        raise ReportGateError(f"trace-summary.json spans are invalid: {run.arm}")
    for span in spans:
        if not isinstance(span, Mapping):
            continue
        attributes_value = span.get("attributes")
        attributes = attributes_value if isinstance(attributes_value, Mapping) else {}
        instance_id = str(attributes.get("eval.instance_id") or "")
        if instance_id not in eligible:
            continue
        mechanism.instances_seen.add(instance_id)
        tool = attributes.get("tool.name")
        if isinstance(tool, str) and tool:
            mechanism.tool_counts[tool] = mechanism.tool_counts.get(tool, 0) + 1
            if tool in EDIT_TOOLS:
                mechanism.edit_calls += 1
                mechanism.instances_with_edits.add(instance_id)
            elif tool in SEARCH_TOOLS:
                mechanism.search_calls += 1
        if span.get("name") == "chat":
            mechanism.chat_rounds[instance_id] = mechanism.chat_rounds.get(instance_id, 0) + 1

    patches: dict[str, str] = {}
    for row in run.predictions:
        instance_id = _source_id(row)
        if instance_id not in eligible:
            continue
        patch = str(row.get("model_patch") or "")
        patches[instance_id] = patch
        if patch.strip():
            mechanism.non_empty_patches.add(instance_id)
        else:
            mechanism.empty_patches.add(instance_id)

    for row in run.instances:
        instance_id = _source_id(row)
        if instance_id not in eligible:
            continue
        metadata_value = row.get("metadata")
        metadata = metadata_value if isinstance(metadata_value, Mapping) else {}
        localization_value = metadata.get("localization")
        localization = localization_value if isinstance(localization_value, Mapping) else {}
        ranking = localization.get("files")
        if not isinstance(ranking, (list, tuple)) or not ranking:
            continue
        mechanism.localization_available.add(instance_id)
        touched = set(patched_paths(patches.get(instance_id, "")))
        if not touched:
            continue
        mechanism.localization_hit_rank[instance_id] = next(
            (index for index, path in enumerate(ranking, 1) if path in touched),
            -1,
        )
    return mechanism


def summarise(mechanism: ArmMechanism) -> dict[str, Any]:
    ranks = [rank for rank in mechanism.localization_hit_rank.values() if rank > 0]
    misses = [
        instance_id
        for instance_id, rank in mechanism.localization_hit_rank.items()
        if rank == -1
    ]
    return {
        "run_id": mechanism.run_id,
        "search_calls": mechanism.search_calls,
        "edit_calls": mechanism.edit_calls,
        "search_to_edit": mechanism.search_to_edit,
        "tool_counts": dict(sorted(mechanism.tool_counts.items())),
        "auxiliary_tool_counts": {
            tool: count
            for tool, count in sorted(mechanism.tool_counts.items())
            if tool not in SEARCH_TOOLS and tool not in EDIT_TOOLS
        },
        "instances_traced": len(mechanism.instances_seen),
        "instances_with_edits": len(mechanism.instances_with_edits),
        "max_chat_round": mechanism.max_chat_round,
        "empty_patches": len(mechanism.empty_patches),
        "non_empty_patches": len(mechanism.non_empty_patches),
        "localization": {
            "instances_with_a_ranking": len(mechanism.localization_available),
            "instances_judgeable": len(mechanism.localization_hit_rank),
            "hits": len(ranks),
            "misses": len(misses),
            "missed_instances": sorted(misses),
            "hit_at_1": sum(1 for rank in ranks if rank == 1),
            "hit_at_3": sum(1 for rank in ranks if rank <= 3),
            "mean_rank_of_hits": round(sum(ranks) / len(ranks), 2) if ranks else None,
        },
    }


def render(arms: Mapping[str, Mapping[str, Any]]) -> str:
    lines = [
        "Mechanism report — eligible astropy-20 evidence only",
        "=" * 62,
        "",
        "The contaminated source instance is excluded from every metric below.",
        "",
    ]
    for arm in ARMS:
        data = arms[arm]
        localization = data["localization"]
        lines.extend(
            [
                f"{arm}  ({data['run_id']})",
                f"  turns      search={data['search_calls']} edit={data['edit_calls']} "
                f"ratio={data['search_to_edit']}  max chat round={data['max_chat_round']}",
                f"  tools      {data['tool_counts']}",
                f"  auxiliary  {data['auxiliary_tool_counts']} "
                "(reported separately; excluded from agent search:edit)",
                f"  delivery   non-empty patches={data['non_empty_patches']} "
                f"empty={data['empty_patches']}  instances with an edit="
                f"{data['instances_with_edits']}/{data['instances_traced']}",
            ]
        )
        if localization["instances_with_a_ranking"]:
            lines.append(
                f"  localize   ranked {localization['instances_with_a_ranking']} instance(s); "
                f"of {localization['instances_judgeable']} with a patch to judge: "
                f"{localization['hits']} hit, {localization['misses']} missed "
                f"(hit@1={localization['hit_at_1']}, hit@3={localization['hit_at_3']}, "
                f"mean rank of hits={localization['mean_rank_of_hits']})"
            )
            if localization["missed_instances"]:
                lines.append(
                    f"             missed: {', '.join(localization['missed_instances'])}"
                )
        elif arm == "baseline":
            lines.append("  localize   no ranking recorded (expected for the baseline arm)")
        else:
            lines.append(
                "  localize   NO RANKING RECORDED -- localization cannot be credited."
            )
        lines.append("")

    baseline = arms["baseline"]
    optimized = arms["optimized"]
    lines.extend(
        [
            "Attribution notes",
            "-" * 62,
            f"  Turn allocation moved {baseline['search_to_edit']} -> "
            f"{optimized['search_to_edit']} (search:edit).",
            f"  Empty patches went {baseline['empty_patches']} -> "
            f"{optimized['empty_patches']}.",
            "",
            "  A higher total is consistent with three simultaneous changes. The",
            "  localization hit rate is the only metric here that directly tests",
            "  the retrieval component; the other rows describe delivery behavior.",
        ]
    )
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verified-arms", type=Path, default=DEFAULT_RECEIPT)
    parser.add_argument("--json", type=Path, help="also write the report as JSON")
    args = parser.parse_args()

    try:
        runs = load_verified_runs(args.verified_arms)
        arms = {arm: summarise(load_arm(runs[arm])) for arm in ARMS}
        payload = {
            "verified_arms": str(args.verified_arms),
            "runs": {
                arm: {
                    "run_id": runs[arm].run_id,
                    "manifest_sha256": runs[arm].manifest_sha256,
                    "checksums_sha256": runs[arm].checksums_sha256,
                    "eligible_total": len(runs[arm].eligible_ids),
                }
                for arm in ARMS
            },
            "mechanism": arms,
        }
    except ReportGateError as exc:
        print(f"BLOCKED: {exc}")
        return 2

    print(render(arms))
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
