"""Check that the two arms differ in exactly the intended way, and no other.

Run this before reading any comparison. ``compare_arms.py`` answers "how much"
and ``mechanism_report.py`` answers "because of what"; both assume the arms are
what their directory names claim. Nothing checked that assumption, and its two
failure modes are silent:

- **Arm B ran with the uplift off.** Then arm B is a second baseline, the
  comparison measures run-to-run noise, and a difference in either direction
  looks like a finding.
- **Arm A ran with the uplift on.** Then the baseline is contaminated and the
  measured gain is understated by an unknown amount.

Neither raises. Both produce a complete, well-formed artifact tree, which is
exactly the shape of defect this project keeps finding: the harness runs, and
the numbers answer a different question than the one being asked.

The evidence is read from the artifacts, not from the environment of whatever
shell happens to be running this: the environment now is not the environment the
run had. Three independent signals per arm, because the manifest records
*intent* while the instances record *effect*, and a bug between them would leave
those two disagreeing:

1. ``run-manifest.json``'s ``harness_uplift`` block — what the run believed.
2. Localization records in ``instances.jsonl`` — whether the localizer ran.
3. Grading-contract text in the task descriptions — whether the prompt changed.

Usage:
    python eval/swebench_work/verify_arms.py
    python eval/swebench_work/verify_arms.py --json out.json
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

#: A phrase unique to the edit mandate. Matched rather than the whole block so a
#: reflow of the prompt text does not turn this check into a false alarm.
MANDATE_MARKER = "How this task is graded"

#: What each arm must look like. Written as data so the assertion is readable as
#: a spec rather than buried in branches.
EXPECTED = {
    "baseline": {"uplift": False, "localization": False, "mandate": False},
    "optimized": {"uplift": True, "localization": True, "mandate": True},
}


@dataclass
class ArmEvidence:
    arm: str
    run_id: str = ""
    present: bool = False
    #: None when the manifest is absent (it is written at run end).
    manifest_uplift: bool | None = None
    manifest_components: tuple[str, ...] = ()
    manifest_tool_rounds: int | None = None
    instances: int = 0
    with_localization: int = 0
    with_mandate: int = 0
    #: Answer fields must never appear in a localization record.
    answer_field_leaks: list[str] = field(default_factory=list)

    @property
    def observed(self) -> dict[str, bool]:
        return {
            "uplift": bool(self.manifest_uplift),
            "localization": self.with_localization > 0,
            "mandate": self.with_mandate > 0,
        }


#: Dataset fields that answer the task. instances.jsonl legitimately holds these
#: in metadata; a localization record must not echo them into the optimization
#: path. Checked here as well as in the unit tests because a guard that only runs
#: against fixtures has never seen a real run.
ANSWER_FIELDS = (
    "test_patch",
    "patch",
    "gold_patch",
    "FAIL_TO_PASS",
    "PASS_TO_PASS",
    "hints_text",
)


#: Shortest prefix of an answer value that counts as copied. Long enough that a
#: bare filename cannot reach it by coincidence.
VALUE_PREFIX = 60


def _dict_keys(value: Any) -> set[str]:
    """Every mapping key anywhere in *value*."""
    keys: set[str] = set()
    if isinstance(value, dict):
        for key, item in value.items():
            keys.add(str(key))
            keys |= _dict_keys(item)
    elif isinstance(value, list):
        for item in value:
            keys |= _dict_keys(item)
    return keys


def _leaked_fields(record: Any, metadata: dict[str, Any]) -> list[str]:
    """Which answer fields appear in *record*, by key or by copied value.

    Keys are matched exactly against the record's own mapping keys, not as
    substrings of the serialised record. Substring matching was the first
    version and it was wrong twice over: ``"patch" in blob`` fires on
    ``test_patch``, so it misattributes which field leaked, and a legitimate
    ranking of ``astropy/utils/patches.py`` would have failed a valid
    experiment. A guard whose false positives block correct runs gets switched
    off, which leaves the real check unrun.
    """
    present = _dict_keys(record)
    leaks = [name for name in ANSWER_FIELDS if name in present]

    # A value copied in under an innocuous key is the leak a key check misses.
    blob = json.dumps(record, ensure_ascii=False)
    for name in ANSWER_FIELDS:
        if name in leaks:
            continue
        value = metadata.get(name)
        if isinstance(value, str) and len(value) >= VALUE_PREFIX:
            if value[:VALUE_PREFIX] in blob:
                leaks.append(name)
    return leaks


def _latest_run(arm_dir: Path) -> Path | None:
    runs = [p for p in arm_dir.glob("*") if (p / "instances.jsonl").is_file()]
    return max(runs, key=lambda p: p.stat().st_mtime) if runs else None


def load_evidence(arm: str) -> ArmEvidence:
    evidence = ArmEvidence(arm=arm)
    arm_dir = EXPERIMENT_DIR / ARMS[arm]
    if not arm_dir.is_dir():
        return evidence
    run_dir = _latest_run(arm_dir)
    if run_dir is None:
        return evidence
    evidence.present = True
    evidence.run_id = run_dir.name

    manifest = run_dir / "run-manifest.json"
    if manifest.is_file():
        try:
            block = json.loads(manifest.read_text(encoding="utf-8")).get(
                "harness_uplift"
            )
        except (json.JSONDecodeError, OSError):
            block = None
        if isinstance(block, dict):
            evidence.manifest_uplift = bool(block.get("enabled"))
            evidence.manifest_components = tuple(block.get("components") or ())
            rounds = block.get("tool_rounds")
            evidence.manifest_tool_rounds = (
                int(rounds) if isinstance(rounds, int) else None
            )

    for line in (run_dir / "instances.jsonl").read_text(
        encoding="utf-8", errors="replace"
    ).splitlines():
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except json.JSONDecodeError:
            continue
        evidence.instances += 1
        metadata = row.get("metadata") or {}
        record = metadata.get("localization")
        if record:
            evidence.with_localization += 1
            for field_name in _leaked_fields(record, metadata):
                leak = f"{row.get('instance_id')}: {field_name}"
                if leak not in evidence.answer_field_leaks:
                    evidence.answer_field_leaks.append(leak)
        if MANDATE_MARKER in (row.get("task_description") or ""):
            evidence.with_mandate += 1
    return evidence


def audit(evidence: ArmEvidence) -> dict[str, Any]:
    """Compare observed evidence against what this arm is supposed to be."""
    expected = EXPECTED[evidence.arm]
    observed = evidence.observed
    mismatches = [
        f"{key}: expected {expected[key]}, observed {observed[key]}"
        for key in expected
        if expected[key] != observed[key]
    ]

    # The manifest records intent; the instances record effect. Disagreement is
    # a bug between them, and it is invisible if only one is consulted.
    inconsistent: list[str] = []
    if evidence.manifest_uplift is True and evidence.with_localization == 0:
        inconsistent.append(
            "manifest says the uplift was enabled, but no instance carries a "
            "localization record"
        )
    if evidence.manifest_uplift is False and evidence.with_localization > 0:
        inconsistent.append(
            "manifest says the uplift was disabled, but instances carry "
            "localization records"
        )

    blocking = list(mismatches) + list(inconsistent) + [
        f"answer field in a localization record: {leak}"
        for leak in evidence.answer_field_leaks
    ]
    return {
        "arm": evidence.arm,
        "run_id": evidence.run_id,
        "present": evidence.present,
        "manifest_uplift": evidence.manifest_uplift,
        "manifest_components": list(evidence.manifest_components),
        "manifest_tool_rounds": evidence.manifest_tool_rounds,
        "instances": evidence.instances,
        "with_localization": evidence.with_localization,
        "with_mandate": evidence.with_mandate,
        "expected": expected,
        "observed": observed,
        "mismatches": mismatches,
        "inconsistencies": inconsistent,
        "answer_field_leaks": list(evidence.answer_field_leaks),
        "ok": evidence.present and not blocking,
    }


def render(audits: dict[str, dict[str, Any]]) -> str:
    lines = [
        "Arm verification — do the arms differ only as intended?",
        "=" * 62,
        "",
    ]
    for arm, data in audits.items():
        if not data["present"]:
            lines += [f"{arm}: NOT PRESENT (no run with instances.jsonl)", ""]
            continue
        verdict = "OK" if data["ok"] else "FAILED"
        lines += [
            f"{arm}  ({data['run_id']})  {verdict}",
            f"  manifest   uplift={data['manifest_uplift']} "
            f"tool_rounds={data['manifest_tool_rounds']} "
            f"components={data['manifest_components']}",
            f"  instances  {data['instances']} recorded; "
            f"{data['with_localization']} with a localization record; "
            f"{data['with_mandate']} with the grading contract",
        ]
        for problem in data["mismatches"] + data["inconsistencies"]:
            lines.append(f"  !! {problem}")
        for leak in data["answer_field_leaks"]:
            lines.append(f"  !! LEAK {leak}")
        lines.append("")

    ok = all(d["ok"] for d in audits.values())
    if ok:
        lines += [
            "VERDICT: the arms differ only in the intended switch.",
            "The comparison may be read.",
        ]
    else:
        lines += [
            "VERDICT: DO NOT READ THE COMPARISON.",
            "",
            "At least one arm is not what its directory name claims. A run whose",
            "uplift state is wrong still produces a complete artifact tree, so the",
            "comparison would look valid and answer a different question than the",
            "one being asked.",
        ]
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--json", type=Path, help="also write the audit as JSON")
    args = parser.parse_args()

    audits = {arm: audit(load_evidence(arm)) for arm in ARMS}
    print(render(audits))
    if args.json:
        args.json.parent.mkdir(parents=True, exist_ok=True)
        args.json.write_text(
            json.dumps(audits, indent=2, ensure_ascii=False), encoding="utf-8"
        )
        print(f"\nwrote {args.json}")
    # Non-zero when an arm is present but wrong, so a script that chains this
    # before the comparison stops instead of reporting a void result.
    return 0 if all(d["ok"] for d in audits.values()) else 2


if __name__ == "__main__":
    sys.exit(main())
