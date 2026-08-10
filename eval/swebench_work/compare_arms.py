"""Compare the two arms of the harness-uplift experiment.

Reads both arms' artifacts and reports a paired comparison. Deliberately
opinionated about what it refuses to print:

- **No SWE-bench Verified score.** The subset is 20 astropy instances from the
  dev side, one repository, and it contains an instance already recorded as
  contaminated. "k/20 on the astropy-20 subset" is the only honest phrasing.
- **No p-value.** With N=20 and single-digit discordant pairs, a p-value is
  decoration that invites a claim the data cannot support. The discordant pair
  counts themselves are the finding, so they are printed directly.
- **No verdict when the arms did not run the same instances.** An unequal
  instance set is a different experiment, not a comparison, and it is reported
  as a blocker rather than averaged over.

Usage:
    python eval/swebench_work/compare_arms.py
    python eval/swebench_work/compare_arms.py --json out.json
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

EXPERIMENT_DIR = REPO_ROOT / "eval_results" / "harness-uplift-20260810"
ARMS = {"baseline": "arm-a-baseline", "optimized": "arm-b-optimized"}

#: Instances excluded from the pairing, with the reason each is excluded.
#:
#: astropy-12907 was used for debugging during harness development (design map
#: §31.8 item 4), so it sits on the dev side and its outcome partly reflects
#: work done while looking at it. Note what this is *not*: under the fixed
#: scorer it produces a real 506-byte patch and a real verdict. The exclusion is
#: about provenance, not about that verdict being fake — the earlier
#: contaminated ``resolved=True`` came from the cross-run report bug and is a
#: separate matter, now fixed.
CONTAMINATED = {
    "astropy__astropy-12907": "dev-side: used for harness debugging, outcome not independent"
}


@dataclass
class ArmResult:
    arm: str
    run_id: str = ""
    run_dir: Path | None = None
    verdicts: dict[str, bool] = field(default_factory=dict)
    empty_patches: set[str] = field(default_factory=set)
    infra_failures: dict[str, str] = field(default_factory=dict)
    edits_by_instance: dict[str, int] = field(default_factory=dict)

    @property
    def resolved(self) -> set[str]:
        return {k for k, v in self.verdicts.items() if v}

    @property
    def measured(self) -> set[str]:
        return set(self.verdicts)


def _latest_run_dir(arm_dir: Path) -> Path | None:
    candidates = [p for p in arm_dir.glob("*") if p.is_dir() and (p / "predictions.jsonl").is_file()]
    if not candidates:
        return None
    return max(candidates, key=lambda p: p.stat().st_mtime)


def load_arm(arm: str) -> ArmResult:
    result = ArmResult(arm=arm)
    arm_dir = EXPERIMENT_DIR / ARMS[arm]
    if not arm_dir.is_dir():
        return result
    run_dir = _latest_run_dir(arm_dir)
    if run_dir is None:
        return result
    result.run_dir = run_dir
    result.run_id = run_dir.name

    for line in (run_dir / "predictions.jsonl").read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        row = json.loads(line)
        instance_id = row.get("instance_id") or ""
        if not instance_id:
            continue
        patch = row.get("model_patch") or ""
        if not patch.strip():
            result.empty_patches.add(instance_id)
        error = (row.get("error") or "").strip()
        if error and "resolved" not in str(row.get("scorer_status", "")):
            # An error with no official verdict attached: infrastructure, not a
            # measurement of the agent. Kept out of the verdict table entirely.
            result.infra_failures[instance_id] = error.splitlines()[0][:120]
            continue
        if row.get("resolved") is None:
            continue
        result.verdicts[instance_id] = bool(row.get("resolved"))

    failures = run_dir / "failures.jsonl"
    if failures.is_file():
        for line in failures.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            row = json.loads(line)
            instance_id = row.get("instance_id") or ""
            if instance_id and instance_id not in result.verdicts:
                result.infra_failures[instance_id] = (
                    f"{row.get('category', '?')}: {(row.get('message') or '')[:100]}"
                )

    summary = run_dir / "traces" / "trace-summary.json"
    if summary.is_file():
        try:
            spans = json.loads(summary.read_text(encoding="utf-8")).get("spans", [])
        except (json.JSONDecodeError, OSError):
            spans = []
        for span in spans:
            attributes = span.get("attributes", {}) or {}
            name = attributes.get("tool.name")
            instance_id = attributes.get("eval.instance_id")
            if name in ("Edit", "Write", "MultiEdit") and instance_id:
                result.edits_by_instance[instance_id] = (
                    result.edits_by_instance.get(instance_id, 0) + 1
                )
    return result


def compare(a: ArmResult, b: ArmResult) -> dict[str, Any]:
    """Paired comparison over the instances both arms actually measured."""
    paired = sorted((a.measured & b.measured) - set(CONTAMINATED))
    a_only_wins = [i for i in paired if a.verdicts[i] and not b.verdicts[i]]
    b_only_wins = [i for i in paired if b.verdicts[i] and not a.verdicts[i]]
    both = [i for i in paired if a.verdicts[i] and b.verdicts[i]]
    neither = [i for i in paired if not a.verdicts[i] and not b.verdicts[i]]
    return {
        "paired_instances": len(paired),
        "excluded_contaminated": sorted(set(CONTAMINATED) & (a.measured | b.measured)),
        "unpaired": {
            "baseline_only": sorted(a.measured - b.measured),
            "optimized_only": sorted(b.measured - a.measured),
        },
        "baseline_resolved": sum(1 for i in paired if a.verdicts[i]),
        "optimized_resolved": sum(1 for i in paired if b.verdicts[i]),
        "both_resolved": both,
        "neither_resolved": neither,
        "discordant": {
            "baseline_only_resolved": a_only_wins,
            "optimized_only_resolved": b_only_wins,
        },
        "empty_patch_counts": {
            "baseline": len(a.empty_patches),
            "optimized": len(b.empty_patches),
        },
        "instances_with_edits": {
            "baseline": len([k for k, v in a.edits_by_instance.items() if v]),
            "optimized": len([k for k, v in b.edits_by_instance.items() if v]),
        },
        "infra_failures": {
            "baseline": a.infra_failures,
            "optimized": b.infra_failures,
        },
    }


def render(a: ArmResult, b: ArmResult, result: dict[str, Any]) -> str:
    lines: list[str] = [
        "Harness uplift — paired comparison on the astropy-20 subset",
        "=" * 62,
        "",
        f"  arm A (baseline) : {a.run_id or 'MISSING'}",
        f"  arm B (optimized): {b.run_id or 'MISSING'}",
        "",
    ]
    if not a.measured or not b.measured:
        missing = "arm A" if not a.measured else "arm B"
        lines += [
            f"BLOCKED: {missing} has no official verdicts yet.",
            "A single-arm number is not a comparison; nothing is reported.",
            "",
        ]
        return "\n".join(lines)

    paired = result["paired_instances"]
    lines += [
        f"Paired instances (both arms measured): {paired}",
        f"  arm A resolved: {result['baseline_resolved']}/{paired}",
        f"  arm B resolved: {result['optimized_resolved']}/{paired}",
        "",
        "Discordant pairs (the whole finding at this N):",
        f"  B fixed what A missed: {len(result['discordant']['optimized_only_resolved'])}",
    ]
    for instance in result["discordant"]["optimized_only_resolved"]:
        lines.append(f"    + {instance}")
    lines.append(
        f"  A fixed what B missed: {len(result['discordant']['baseline_only_resolved'])}"
    )
    for instance in result["discordant"]["baseline_only_resolved"]:
        lines.append(f"    - {instance}")
    lines += [
        "",
        "Mechanism check (why, not just how much):",
        f"  empty patches      A={result['empty_patch_counts']['baseline']}  "
        f"B={result['empty_patch_counts']['optimized']}",
        f"  instances w/ edits A={result['instances_with_edits']['baseline']}  "
        f"B={result['instances_with_edits']['optimized']}",
        "",
    ]
    if result["excluded_contaminated"]:
        lines.append("Excluded as contaminated (never counted either way):")
        for instance in result["excluded_contaminated"]:
            lines.append(f"  ! {instance} — {CONTAMINATED[instance]}")
        lines.append("")
    unpaired = result["unpaired"]
    if unpaired["baseline_only"] or unpaired["optimized_only"]:
        lines += [
            "WARNING: the arms did not measure the same instance set.",
            f"  only in A: {unpaired['baseline_only']}",
            f"  only in B: {unpaired['optimized_only']}",
            "  Those instances are excluded from the pairing above.",
            "",
        ]
    for arm in ("baseline", "optimized"):
        failures = result["infra_failures"][arm]
        if failures:
            lines.append(f"Infrastructure failures in {arm} (not agent failures):")
            for instance, detail in sorted(failures.items()):
                lines.append(f"  {instance}: {detail}")
            lines.append("")
    lines += [
        "Reporting constraint: this is 'k/20 on the astropy-20 subset', a single-",
        "repository dev-side slice. It is NOT a SWE-bench Verified score and must",
        "never be written as one. No p-value is reported: at this N it would be",
        "decoration over single-digit discordant counts.",
    ]
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--json", type=Path, help="also write the comparison as JSON")
    args = parser.parse_args()

    a = load_arm("baseline")
    b = load_arm("optimized")
    result = compare(a, b)
    print(render(a, b, result))

    if args.json:
        payload = {
            "baseline": {"run_id": a.run_id, "verdicts": a.verdicts},
            "optimized": {"run_id": b.run_id, "verdicts": b.verdicts},
            "comparison": result,
        }
        args.json.parent.mkdir(parents=True, exist_ok=True)
        args.json.write_text(
            json.dumps(payload, indent=2, ensure_ascii=False), encoding="utf-8"
        )
        print(f"\nwrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
