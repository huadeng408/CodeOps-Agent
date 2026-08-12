"""Verify the frozen SWE-bench arms and atomically pin their report inputs."""

from __future__ import annotations

import argparse
from collections import defaultdict
import json
import re
import sys
from pathlib import Path
from typing import Any, Mapping

REPO_ROOT = Path(__file__).resolve().parents[2]
if str(REPO_ROOT) not in sys.path:
    sys.path.insert(0, str(REPO_ROOT))

from eval.swebench_work.report_gate import (  # noqa: E402
    ARMS,
    DEFAULT_RECEIPT,
    DEFAULT_RUNS,
    FinalizedRun,
    ReportGateError,
    load_finalized_run,
    write_verification_receipt,
)

MANDATE_MARKER = "How this task is graded"
EXPECTED_MANIFEST = {
    "baseline": {"enabled": False, "components": [], "tool_rounds": 8},
    "optimized": {
        "enabled": True,
        "components": ["localization", "tool_rounds", "edit_mandate", "validation"],
        "tool_rounds": 24,
    },
}
ANSWER_FIELDS = (
    "test_patch",
    "patch",
    "gold_patch",
    "FAIL_TO_PASS",
    "PASS_TO_PASS",
    "hints_text",
    "hints_to_generated_patch",
)
PROMPT_ANSWER_FIELDS = tuple(field for field in ANSWER_FIELDS if field != "patch")
VALUE_PREFIX = 60


def _jsonable(value: Any) -> Any:
    if isinstance(value, Mapping):
        return {str(key): _jsonable(item) for key, item in value.items()}
    if isinstance(value, (list, tuple)):
        return [_jsonable(item) for item in value]
    return value


def _dict_keys(value: Any) -> set[str]:
    keys: set[str] = set()
    if isinstance(value, Mapping):
        for key, item in value.items():
            keys.add(str(key))
            keys |= _dict_keys(item)
    elif isinstance(value, (list, tuple)):
        for item in value:
            keys |= _dict_keys(item)
    return keys


def _string_values(value: Any) -> list[str]:
    values: list[str] = []
    if isinstance(value, Mapping):
        for item in value.values():
            values.extend(_string_values(item))
    elif isinstance(value, (list, tuple)):
        for item in value:
            values.extend(_string_values(item))
    elif isinstance(value, str):
        values.append(value)
    return values


def _leaked_fields(record: Any, metadata: Mapping[str, Any]) -> list[str]:
    """Return answer fields copied into a localization record by key or value."""
    present = _dict_keys(record)
    leaks = [name for name in ANSWER_FIELDS if name in present]
    record_strings = _string_values(record)
    for name in ANSWER_FIELDS:
        if name in leaks:
            continue
        value = metadata.get(name)
        if isinstance(value, str) and len(value) >= VALUE_PREFIX:
            if any(value[:VALUE_PREFIX] in item for item in record_strings):
                leaks.append(name)
    return leaks


def _task_description_leaks(description: str, metadata: Mapping[str, Any]) -> list[str]:
    """Detect answer-only fields or values in the actual model input."""
    normalized = re.sub(r"[\s_-]+", "_", description.casefold())
    normalized_description = description.replace("\r\n", "\n").replace("\r", "\n")
    leaks = [name for name in PROMPT_ANSWER_FIELDS if name.casefold() in normalized]
    for name in ANSWER_FIELDS:
        if name in leaks:
            continue
        value = metadata.get(name)
        if isinstance(value, str):
            normalized_value = value.replace("\r\n", "\n").replace("\r", "\n")
            if (
                len(normalized_value) >= VALUE_PREFIX
                and normalized_value[:VALUE_PREFIX] in normalized_description
            ):
                leaks.append(name)
    return leaks


def audit_run(run: FinalizedRun) -> dict[str, Any]:
    """Prove one finalized run has the complete identity claimed by its arm."""
    expected = EXPECTED_MANIFEST[run.arm]
    block = run.manifest.get("harness_uplift")
    manifest = block if isinstance(block, Mapping) else {}
    manifest_components = list(manifest.get("components") or ())
    mismatches: list[str] = []
    if manifest.get("enabled") is not expected["enabled"]:
        mismatches.append(
            f"manifest enabled: expected {expected['enabled']}, observed {manifest.get('enabled')!r}"
        )
    if set(manifest_components) != set(expected["components"]):
        mismatches.append(
            f"manifest components: expected {sorted(expected['components'])}, "
            f"observed {sorted(manifest_components)}"
        )
    if manifest.get("tool_rounds") != expected["tool_rounds"]:
        mismatches.append(
            f"manifest tool_rounds: expected {expected['tool_rounds']}, "
            f"observed {manifest.get('tool_rounds')!r}"
        )

    rows_by_id: dict[str, list[Mapping[str, Any]]] = defaultdict(list)
    localization_ids: set[str] = set()
    mandate_ids: set[str] = set()
    missing_localization_rows: list[str] = []
    missing_mandate_rows: list[str] = []
    unexpected_localization_rows: list[str] = []
    unexpected_mandate_rows: list[str] = []
    leaks: list[str] = []

    for row in run.instances:
        source_id = str(row.get("instance_id", row.get("source_id", "")))
        rows_by_id[source_id].append(row)
        metadata_value = row.get("metadata")
        metadata = metadata_value if isinstance(metadata_value, Mapping) else {}
        localization = metadata.get("localization")
        has_localization = bool(localization)
        task_description = str(row.get("task_description") or "")
        has_mandate = MANDATE_MARKER in task_description
        row_label = f"{source_id}#{len(rows_by_id[source_id])}"
        for field_name in _task_description_leaks(task_description, metadata):
            leak = f"{row_label}: task_description: {field_name}"
            if leak not in leaks:
                leaks.append(leak)
        if has_localization:
            localization_ids.add(source_id)
            for field_name in _leaked_fields(localization, metadata):
                leak = f"{row_label}: {field_name}"
                if leak not in leaks:
                    leaks.append(leak)
        if has_mandate:
            mandate_ids.add(source_id)
        if run.arm == "baseline":
            if has_localization:
                unexpected_localization_rows.append(row_label)
            if has_mandate:
                unexpected_mandate_rows.append(row_label)
        else:
            if not has_localization:
                missing_localization_rows.append(row_label)
            if not has_mandate:
                missing_mandate_rows.append(row_label)

    source_ids = set(run.source_ids)
    missing_instance_ids = sorted(source_ids - set(rows_by_id))
    missing_localization_ids = sorted(source_ids - localization_ids)
    missing_mandate_ids = sorted(source_ids - mandate_ids)
    if run.arm == "baseline":
        if unexpected_localization_rows:
            mismatches.append("baseline rows contain localization evidence")
        if unexpected_mandate_rows:
            mismatches.append("baseline rows contain edit mandate evidence")
    else:
        if missing_instance_ids:
            mismatches.append("optimized arm lacks instance rows for source IDs")
        if missing_localization_ids or missing_localization_rows:
            mismatches.append("optimized arm lacks complete localization evidence")
        if missing_mandate_ids or missing_mandate_rows:
            mismatches.append("optimized arm lacks complete mandate evidence")

    return {
        "arm": run.arm,
        "run_id": run.run_id,
        "present": True,
        "manifest_uplift": manifest.get("enabled"),
        "manifest_components": manifest_components,
        "manifest_tool_rounds": manifest.get("tool_rounds"),
        "instances": len(run.instances),
        "source_total": len(run.source_ids),
        "with_localization": sum(
            bool((row.get("metadata") or {}).get("localization"))
            for row in run.instances
        ),
        "with_mandate": sum(
            MANDATE_MARKER in str(row.get("task_description") or "")
            for row in run.instances
        ),
        "missing_instance_ids": missing_instance_ids,
        "missing_localization_ids": missing_localization_ids if run.arm == "optimized" else [],
        "missing_mandate_ids": missing_mandate_ids if run.arm == "optimized" else [],
        "missing_localization_rows": sorted(missing_localization_rows),
        "missing_mandate_rows": sorted(missing_mandate_rows),
        "unexpected_localization_rows": sorted(unexpected_localization_rows),
        "unexpected_mandate_rows": sorted(unexpected_mandate_rows),
        "answer_field_leaks": sorted(leaks),
        "mismatches": mismatches,
        "inconsistencies": [],
        "ok": not mismatches and not leaks,
    }


def render(audits: Mapping[str, Mapping[str, Any]]) -> str:
    lines = ["Arm verification — complete frozen identity audit", "=" * 62, ""]
    for arm in ARMS:
        data = audits[arm]
        verdict = "OK" if data["ok"] else "FAILED"
        lines.extend(
            [
                f"{arm}  ({data['run_id']})  {verdict}",
                f"  manifest   uplift={data['manifest_uplift']} "
                f"tool_rounds={data['manifest_tool_rounds']} "
                f"components={data['manifest_components']}",
                f"  evidence   {data['instances']} attempt row(s); "
                f"{data['with_localization']} localized; {data['with_mandate']} mandated",
            ]
        )
        for problem in data["mismatches"]:
            lines.append(f"  !! {problem}")
        for leak in data["answer_field_leaks"]:
            lines.append(f"  !! LEAK {leak}")
        lines.append("")
    lines.append(
        "VERDICT: VERIFIED — the frozen arms differ only as intended."
        if all(data["ok"] for data in audits.values())
        else "VERDICT: BLOCKED — at least one frozen arm identity is invalid."
    )
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline-run", default=DEFAULT_RUNS["baseline"])
    parser.add_argument("--optimized-run", default=DEFAULT_RUNS["optimized"])
    parser.add_argument("--verified-arms", type=Path, default=DEFAULT_RECEIPT)
    parser.add_argument("--json", type=Path, help="optional separate identity audit JSON")
    args = parser.parse_args()

    try:
        runs = {
            "baseline": load_finalized_run("baseline", args.baseline_run),
            "optimized": load_finalized_run("optimized", args.optimized_run),
        }
        audits = {arm: audit_run(run) for arm, run in runs.items()}
        failed = [arm for arm, audit in audits.items() if not audit["ok"]]
        if failed:
            reasons = []
            for arm in failed:
                reasons.extend(f"{arm}: {item}" for item in audits[arm]["mismatches"])
                reasons.extend(
                    f"{arm}: answer field leak {item}"
                    for item in audits[arm]["answer_field_leaks"]
                )
            raise ReportGateError("; ".join(reasons) or "arm identity audit failed")
        write_verification_receipt(runs, audits, args.verified_arms)
        if args.json:
            args.json.parent.mkdir(parents=True, exist_ok=True)
            args.json.write_text(
                json.dumps(audits, indent=2, ensure_ascii=False, sort_keys=True) + "\n",
                encoding="utf-8",
            )
    except ReportGateError as exc:
        print(f"BLOCKED: {exc}")
        return 2

    print(render(audits))
    print(f"\nwrote {args.verified_arms}")
    if args.json:
        print(f"wrote {args.json}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
