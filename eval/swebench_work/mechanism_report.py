"""Why the score moved, measured rather than asserted.

``compare_arms.py`` answers "how much". This answers "because of what", from the
same artifacts. It exists because the four changes in the optimized arm are not
equally interesting: two are repairs to basic engineering defects (a turn budget
below the floor for the task, a task description that never said what the grader
reads), and only one is an actual retrieval component. A total that went up is
consistent with any mix of the three, so a total alone would let the write-up
credit whichever part sounds best.

The three questions it answers per arm:

1. **Where did the turns go?** Search calls versus edit calls, from the trace
   spans. The baseline ran 96 searches against 2 edits; if the optimized arm has
   not moved that ratio, localization did not do what it was added to do,
   whatever happened to the score.
2. **Did the agent deliver at all?** Empty-patch count. This is the delivery
   contract's target, and it is measurable independently of correctness.
3. **Did localization point at the right file?** Whether the patch touched a file
   localization ranked, and at what rank. This is the only question that can
   isolate the retrieval component, and it is answerable only because the ranking
   is persisted in ``instances.jsonl`` alongside the patch.

Usage:
    python eval/swebench_work/mechanism_report.py
    python eval/swebench_work/mechanism_report.py --json out.json
"""

from __future__ import annotations

import argparse
import json
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

REPO_ROOT = Path(__file__).resolve().parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from eval.harness.validate import patched_paths  # noqa: E402

EXPERIMENT_DIR = REPO_ROOT / "eval_results" / "harness-uplift-20260810"
ARMS = {"baseline": "arm-a-baseline", "optimized": "arm-b-optimized"}

SEARCH_TOOLS = frozenset({"Grep", "Read", "Glob", "Bash", "LS"})
EDIT_TOOLS = frozenset({"Edit", "Write", "MultiEdit", "str_replace_editor"})


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
    #: instance_id -> rank of the first patched file in the localization list,
    #: or -1 when localization ranked files but the patch touched none of them.
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


def _latest_run(arm_dir: Path) -> Path | None:
    runs = [p for p in arm_dir.glob("*") if (p / "predictions.jsonl").is_file()]
    return max(runs, key=lambda p: p.stat().st_mtime) if runs else None


def _read_jsonl(path: Path) -> list[dict[str, Any]]:
    if not path.is_file():
        return []
    rows = []
    for line in path.read_text(encoding="utf-8").splitlines():
        if line.strip():
            try:
                rows.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    return rows


def load_arm(arm: str) -> ArmMechanism:
    mechanism = ArmMechanism(arm=arm)
    arm_dir = EXPERIMENT_DIR / ARMS[arm]
    if not arm_dir.is_dir():
        return mechanism
    run_dir = _latest_run(arm_dir)
    if run_dir is None:
        return mechanism
    mechanism.run_id = run_dir.name

    # --- turns, from the trace spans ---
    summary_path = run_dir / "traces" / "trace-summary.json"
    if summary_path.is_file():
        try:
            spans = json.loads(summary_path.read_text(encoding="utf-8")).get("spans", [])
        except (json.JSONDecodeError, OSError):
            spans = []
        for span in spans:
            attributes = span.get("attributes", {}) or {}
            instance_id = attributes.get("eval.instance_id") or ""
            if instance_id:
                mechanism.instances_seen.add(instance_id)
            tool = attributes.get("tool.name")
            if tool:
                mechanism.tool_counts[tool] = mechanism.tool_counts.get(tool, 0) + 1
                if tool in EDIT_TOOLS:
                    mechanism.edit_calls += 1
                    if instance_id:
                        mechanism.instances_with_edits.add(instance_id)
                elif tool in SEARCH_TOOLS:
                    mechanism.search_calls += 1
            if span.get("name") == "chat" and instance_id:
                mechanism.chat_rounds[instance_id] = (
                    mechanism.chat_rounds.get(instance_id, 0) + 1
                )

    # --- delivery, from the predictions ---
    patches: dict[str, str] = {}
    for row in _read_jsonl(run_dir / "predictions.jsonl"):
        instance_id = row.get("instance_id") or ""
        if not instance_id:
            continue
        patch = row.get("model_patch") or ""
        patches[instance_id] = patch
        if patch.strip():
            mechanism.non_empty_patches.add(instance_id)
        else:
            mechanism.empty_patches.add(instance_id)

    # --- localization accuracy, from the recorded ranking + the patch ---
    for row in _read_jsonl(run_dir / "instances.jsonl"):
        instance_id = row.get("instance_id") or ""
        ranking = ((row.get("metadata") or {}).get("localization") or {}).get("files")
        if not instance_id or not ranking:
            continue
        mechanism.localization_available.add(instance_id)
        touched = set(patched_paths(patches.get(instance_id, "")))
        if not touched:
            continue  # no patch: says nothing about the ranking either way
        rank = next(
            (i for i, path in enumerate(ranking, 1) if path in touched), -1
        )
        mechanism.localization_hit_rank[instance_id] = rank
    return mechanism


def summarise(mechanism: ArmMechanism) -> dict[str, Any]:
    ranks = [r for r in mechanism.localization_hit_rank.values() if r > 0]
    misses = [i for i, r in mechanism.localization_hit_rank.items() if r == -1]
    return {
        "run_id": mechanism.run_id,
        "search_calls": mechanism.search_calls,
        "edit_calls": mechanism.edit_calls,
        "search_to_edit": mechanism.search_to_edit,
        "tool_counts": dict(sorted(mechanism.tool_counts.items())),
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
            "hit_at_1": sum(1 for r in ranks if r == 1),
            "hit_at_3": sum(1 for r in ranks if r <= 3),
            "mean_rank_of_hits": (round(sum(ranks) / len(ranks), 2) if ranks else None),
        },
    }


def render(arms: dict[str, dict[str, Any]]) -> str:
    lines = [
        "Mechanism report — why the score moved, not just whether",
        "=" * 62,
        "",
    ]
    for arm, data in arms.items():
        if not data["run_id"]:
            lines += [f"{arm}: MISSING", ""]
            continue
        loc = data["localization"]
        lines += [
            f"{arm}  ({data['run_id']})",
            f"  turns      search={data['search_calls']} edit={data['edit_calls']} "
            f"ratio={data['search_to_edit']}  max chat round={data['max_chat_round']}",
            f"  tools      {data['tool_counts']}",
            f"  delivery   non-empty patches={data['non_empty_patches']} "
            f"empty={data['empty_patches']}  "
            f"instances with an edit={data['instances_with_edits']}"
            f"/{data['instances_traced']}",
        ]
        if loc["instances_with_a_ranking"]:
            lines.append(
                f"  localize   ranked {loc['instances_with_a_ranking']} instance(s); "
                f"of {loc['instances_judgeable']} with a patch to judge: "
                f"{loc['hits']} hit, {loc['misses']} missed "
                f"(hit@1={loc['hit_at_1']}, hit@3={loc['hit_at_3']}, "
                f"mean rank of hits={loc['mean_rank_of_hits']})"
            )
            if loc["missed_instances"]:
                lines.append(
                    f"             missed: {', '.join(loc['missed_instances'])}"
                )
        elif arm == "baseline":
            lines.append("  localize   no ranking recorded (expected: the baseline arm has no localizer)")
        else:
            # Do not reassure here. The optimized arm is the one that runs the
            # localizer, so a missing ranking means either the uplift was off or
            # the record never reached the artifact -- and in both cases the
            # arm's gain cannot be attributed to retrieval at all.
            lines.append(
                "  localize   NO RANKING RECORDED -- unexpected in this arm. "
                "Either the uplift was off or the record did not reach the "
                "artifact; localization cannot be credited either way."
            )
        lines.append("")

    baseline = arms.get("baseline", {})
    optimized = arms.get("optimized", {})
    if baseline.get("run_id") and optimized.get("run_id"):
        lines += [
            "Attribution notes",
            "-" * 62,
            f"  Turn allocation moved {baseline['search_to_edit']} -> "
            f"{optimized['search_to_edit']} (search:edit).",
            f"  Empty patches went {baseline['empty_patches']} -> "
            f"{optimized['empty_patches']}.",
            "",
            "  A higher total in the optimized arm is consistent with three",
            "  changes at once. Read the rows above before crediting any one of",
            "  them: an unchanged search:edit ratio means localization did not do",
            "  its job regardless of the score, and an unchanged empty-patch",
            "  count means the same for the delivery contract. Localization's own",
            "  hit rate is the only figure here that isolates the retrieval",
            "  component; the other two are repairs to basic defects.",
        ]
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--json", type=Path, help="also write the report as JSON")
    args = parser.parse_args()

    arms = {arm: summarise(load_arm(arm)) for arm in ARMS}
    print(render(arms))
    if args.json:
        args.json.parent.mkdir(parents=True, exist_ok=True)
        args.json.write_text(
            json.dumps(arms, indent=2, ensure_ascii=False), encoding="utf-8"
        )
        print(f"\nwrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
