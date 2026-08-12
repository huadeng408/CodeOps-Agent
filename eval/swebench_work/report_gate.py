"""Validate immutable, finalized SWE-bench evaluation run artifacts."""

from __future__ import annotations

from collections import Counter, defaultdict
from dataclasses import dataclass
import hashlib
import json
import os
import re
import stat
from pathlib import Path
from types import MappingProxyType
from typing import Any, Mapping

from eval.harness.artifacts import RunArtifacts


ARMS = {
    "baseline": "arm-a-baseline",
    "optimized": "arm-b-optimized",
}
DEFAULT_RUNS: Mapping[str, str] = MappingProxyType(
    {
        "baseline": "swebench-deepseek-v4-pro-a5cb0378",
        "optimized": "swebench-deepseek-v4-pro-eea6403e",
    }
)
EXPERIMENT_DIR = Path("eval_results/harness-uplift-20260810")
COHORT_PATH = Path("data/eval/swebench/subset-astropy-20.json")
DEFAULT_RECEIPT = Path("eval_results/harness-uplift-20260810/verified-arms.json")
# Receipt-safe approved exclusion list; raw evidence remains available to audits.
CONTAMINATED: Mapping[str, str] = MappingProxyType(
    {"astropy__astropy-12907": "known benchmark contamination"}
)
REQUIRED_EXISTENCE = frozenset(
    {
        "checksums.sha256",
        "instances.jsonl",
        "predictions.jsonl",
        "events.jsonl",
        "failures.jsonl",
        "summary.json",
        "run-manifest.json",
        "traces/trace-summary.json",
        "traces/span-assertion.json",
        "scorer/report.json",
        "scorer/run-summary.json",
        "scorer/run_instance.log",
        "scorer/test_output.txt",
    }
)
REQUIRED_PINNED = REQUIRED_EXISTENCE - {"checksums.sha256"}
REQUIRED_SCORER_FILES = frozenset(
    {
        "scorer/report.json",
        "scorer/run-summary.json",
        "scorer/run_instance.log",
        "scorer/test_output.txt",
    }
)
SCORER_EVIDENCE_SCOPE = "last-invocation-only"
MAX_SCORER_FAILURES = 3
_CHECKSUM_LINE = re.compile(r"^([0-9a-f]{64})  ([^\r\n]+)$")


class ReportGateError(RuntimeError):
    """A human-readable reason a run cannot be promoted."""


@dataclass(frozen=True)
class FinalizedRun:
    arm: str
    run_id: str
    run_dir: Path
    manifest: Mapping[str, Any]
    summary: Mapping[str, Any]
    manifest_sha256: str
    checksums_sha256: str
    cohort_path: Path
    cohort_sha256: str
    source_ids: tuple[str, ...]
    eligible_ids: frozenset[str]
    instances: tuple[Mapping[str, Any], ...]
    predictions: tuple[Mapping[str, Any], ...]
    events: tuple[Mapping[str, Any], ...]
    failures: tuple[Mapping[str, Any], ...]
    trace_summary: Mapping[str, Any]
    span_assertion: Mapping[str, Any]
    official_verdicts: Mapping[str, bool]
    outcomes: Mapping[str, bool]
    unmeasured_by_reason: Mapping[str, Mapping[str, Mapping[str, str]]]
    failures_by_reason: Mapping[str, Mapping[str, Mapping[str, str]]]
    excluded_contaminated: Mapping[str, str]
    scorer_evidence_scope: str


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _read_json(path: Path) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ReportGateError(f"invalid JSON: {path.name}") from exc


def _read_jsonl(path: Path) -> list[Any]:
    records: list[Any] = []
    try:
        for line in path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                records.append(json.loads(line))
    except (OSError, json.JSONDecodeError) as exc:
        raise ReportGateError(f"invalid JSONL: {path.name}") from exc
    return records


def _read_object(path: Path) -> dict[str, Any]:
    value = _read_json(path)
    if not isinstance(value, dict):
        raise ReportGateError(f"{path.name} must be a JSON object")
    return value


def _load_cohort(path: Path) -> tuple[tuple[str, ...], str]:
    payload = _read_object(path)
    ids = payload.get("instance_ids")
    if payload.get("size") != 20 or not isinstance(ids, list) or len(ids) != 20:
        raise ReportGateError("cohort must declare size 20 with 20 instance_ids")
    if any(not isinstance(item, str) or not item for item in ids) or len(set(ids)) != 20:
        raise ReportGateError("cohort instance_ids must be unique non-empty strings")
    return tuple(ids), _sha256(path)


def _freeze_json(value: Any) -> Any:
    if isinstance(value, Mapping):
        return MappingProxyType({key: _freeze_json(item) for key, item in value.items()})
    if isinstance(value, list) or isinstance(value, tuple):
        return tuple(_freeze_json(item) for item in value)
    if isinstance(value, set) or isinstance(value, frozenset):
        return frozenset(_freeze_json(item) for item in value)
    return value


def _thaw_json(value: Any) -> Any:
    if isinstance(value, Mapping):
        return {key: _thaw_json(item) for key, item in value.items()}
    if isinstance(value, tuple):
        return [_thaw_json(item) for item in value]
    if isinstance(value, frozenset):
        return sorted(_thaw_json(item) for item in value)
    return value


def _is_reparse_point(path: Path) -> bool:
    try:
        attributes = getattr(os.stat(path, follow_symlinks=False), "st_file_attributes", 0)
    except OSError:
        return False
    return bool(attributes & getattr(stat, "FILE_ATTRIBUTE_REPARSE_POINT", 0))


def _resolve_run_dir(arm: str, run_id: str, experiment_dir: Path) -> tuple[Path, Path]:
    if arm not in ARMS:
        raise ReportGateError(f"unknown arm: {arm}")
    requested = Path(run_id)
    if not run_id or requested.is_absolute() or requested.name != run_id:
        raise ReportGateError("run_id must be a single relative basename")
    arm_root = Path(experiment_dir) / ARMS[arm]
    run_dir = arm_root / run_id
    if run_dir.is_symlink():
        raise ReportGateError("run directory symlink is not allowed")
    if _is_reparse_point(run_dir):
        raise ReportGateError("run directory reparse point is not allowed")
    try:
        if run_dir.resolve().parent != arm_root.resolve():
            raise ReportGateError("run directory must be a direct child of its arm root")
    except OSError as exc:
        raise ReportGateError("run directory cannot be resolved safely") from exc
    return run_dir, arm_root


def _failure_bucket(raw_category: str) -> str:
    if raw_category in {"scorer", "agent", "budget", "timeout", "oom", "infra"}:
        return raw_category
    if raw_category in {"cancelled", "skipped"}:
        return "cancelled/skipped"
    return "unknown"


def _event_failure_bucket(event_status: str) -> str:
    if event_status in {"cancelled", "skipped"}:
        return "cancelled/skipped"
    if event_status in {
        "failed-scorer",
        "failed-agent",
        "failed-budget",
        "failed-timeout",
        "failed-oom",
        "failed-infra",
    }:
        return event_status.removeprefix("failed-")
    return "unknown"


def _safe_checksum_path(run_dir: Path, rel: str) -> Path:
    if (
        not rel
        or rel.startswith(("/", "//", "\\\\", "\\\\?\\", "\\\\.\\"))
        or re.match(r"^[A-Za-z]:", rel) is not None
        or "\\" in rel
    ):
        raise ReportGateError(f"unsafe checksum path: {rel}")
    parts = rel.split("/")
    if any(part in {"", ".", ".."} or ":" in part for part in parts):
        raise ReportGateError(f"unsafe checksum path: {rel}")
    path = run_dir.joinpath(*parts)
    if path.is_symlink() or _is_reparse_point(path):
        raise ReportGateError(f"unsafe checksum path symlink: {rel}")
    try:
        path.resolve().relative_to(run_dir.resolve())
    except (OSError, ValueError) as exc:
        raise ReportGateError(f"unsafe checksum path: {rel}") from exc
    return path


def _validate_required_artifacts(run_dir: Path) -> None:
    required_order = ("summary.json", *sorted(REQUIRED_EXISTENCE - {"summary.json"}))
    for rel in required_order:
        path = run_dir / rel
        if not path.is_file():
            raise ReportGateError(f"missing required artifact: {rel}")
        if path.is_symlink() or _is_reparse_point(path):
            raise ReportGateError(f"required artifact must not be a symlink or reparse point: {rel}")
        try:
            path.resolve().relative_to(run_dir.resolve())
        except (OSError, ValueError) as exc:
            raise ReportGateError(f"required artifact escapes run directory: {rel}") from exc


def _parse_checksums(run_dir: Path) -> None:
    manifest = run_dir / "checksums.sha256"
    seen: set[str] = set()
    try:
        lines = manifest.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise ReportGateError("cannot read checksums.sha256") from exc
    for line in lines:
        if not line.strip():
            continue
        match = _CHECKSUM_LINE.fullmatch(line)
        if match is None:
            raise ReportGateError("malformed checksum manifest")
        _, rel = match.groups()
        if rel == "checksums.sha256":
            raise ReportGateError("checksum manifest self-pin is forbidden")
        if rel in seen:
            raise ReportGateError(f"duplicate checksum path: {rel}")
        seen.add(rel)
        _safe_checksum_path(run_dir, rel)
    missing = sorted(REQUIRED_PINNED - seen)
    if missing:
        raise ReportGateError(f"missing required checksum pin: {', '.join(missing)}")


def _source_id(record: Mapping[str, Any], artifact: str) -> str:
    source_id = record.get("instance_id", record.get("source_id"))
    if not isinstance(source_id, str) or not source_id:
        raise ReportGateError(f"{artifact} row lacks source ID")
    return source_id


def _terminal_class(status: str) -> str:
    if status == "completed":
        return "completed"
    if status in {"skipped-resume", "skipped", "cancelled"}:
        return "skipped"
    if status.startswith("failed-"):
        return "failed"
    raise ReportGateError(f"unsupported non-terminal event status: {status!r}")


def _event_category(status: str) -> str:
    if status.startswith("failed-"):
        return status.removeprefix("failed-")
    return status


def _normalise_records(
    source_ids: tuple[str, ...],
    instances: list[Any],
    predictions: list[Any],
    events: list[Any],
    failures: list[Any],
) -> tuple[
    dict[str, bool],
    dict[str, bool],
    dict[str, dict[str, dict[str, str]]],
    dict[str, dict[str, dict[str, str]]],
]:
    allowed = set(source_ids)
    typed: dict[str, list[dict[str, Any]]] = {}
    for name, rows in {
        "instances.jsonl": instances,
        "predictions.jsonl": predictions,
        "events.jsonl": events,
        "failures.jsonl": failures,
    }.items():
        typed[name] = []
        for row in rows:
            if not isinstance(row, dict):
                raise ReportGateError(f"{name} rows must be JSON objects")
            source_id = _source_id(row, name)
            if source_id not in allowed:
                raise ReportGateError(f"{name} has source ID outside cohort: {source_id}")
            typed[name].append(row)

    by_id: dict[str, list[dict[str, Any]]] = {source_id: [] for source_id in source_ids}
    for event in typed["events.jsonl"]:
        status = event.get("status")
        if not isinstance(status, str) or not status:
            raise ReportGateError("events.jsonl event status must be non-empty")
        _terminal_class(status)
        by_id[_source_id(event, "events.jsonl")].append(event)
    if any(not by_id[source_id] for source_id in source_ids):
        missing = next(source_id for source_id in source_ids if not by_id[source_id])
        raise ReportGateError(f"missing terminal event for source ID: {missing}")

    instance_counts = Counter(_source_id(row, "instances.jsonl") for row in typed["instances.jsonl"])
    prediction_by_id: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in typed["predictions.jsonl"]:
        prediction_by_id[_source_id(row, "predictions.jsonl")].append(row)
    failure_by_id: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for row in typed["failures.jsonl"]:
        category = row.get("category")
        if not isinstance(category, str) or not category:
            raise ReportGateError("failures.jsonl row lacks category")
        message = row.get("message")
        if not isinstance(message, str):
            raise ReportGateError("failures.jsonl row lacks message")
        failure_by_id[_source_id(row, "failures.jsonl")].append(row)

    outcomes: dict[str, bool] = {}
    official: dict[str, bool] = {}
    current_failures: dict[str, dict[str, dict[str, str]]] = defaultdict(dict)
    unmeasured: dict[str, dict[str, dict[str, str]]] = defaultdict(dict)
    current_scorer_ids: set[str] = set()

    for source_id in source_ids:
        history = by_id[source_id]
        for previous, event in zip(history, history[1:]):
            if previous["status"] == "completed" and event["status"] != "skipped-resume":
                raise ReportGateError(f"event after completed must be skipped-resume: {source_id}")
            if previous["status"] not in {"failed-scorer", "completed", "skipped-resume"}:
                raise ReportGateError(f"event after non-scorer terminal: {source_id}")
        completed_attempts = sum(event["status"] == "completed" for event in history)
        scorer_attempts = sum(event["status"] == "failed-scorer" for event in history)
        if completed_attempts > 1:
            raise ReportGateError(f"multiple completed attempts for source ID: {source_id}")
        if instance_counts[source_id] != completed_attempts + scorer_attempts:
            raise ReportGateError(f"unaccounted instance rows for source ID: {source_id}")
        if len(prediction_by_id[source_id]) > completed_attempts:
            if not (
                completed_attempts == 0
                and history[-1]["status"] == "failed-scorer"
                and len(prediction_by_id[source_id]) == 1
            ):
                raise ReportGateError(f"prediction rows do not match completed attempts: {source_id}")
        elif len(prediction_by_id[source_id]) != completed_attempts:
            raise ReportGateError(f"prediction rows do not match completed attempts: {source_id}")
        if any(event["status"] == "skipped-resume" for event in history):
            first_skip = next(index for index, event in enumerate(history) if event["status"] == "skipped-resume")
            if any(event["status"] != "skipped-resume" for event in history[first_skip:]):
                raise ReportGateError(f"invalid event after skipped-resume: {source_id}")
            if not any(event["status"] == "completed" for event in history[:first_skip]):
                raise ReportGateError(f"skipped-resume without prior prediction: {source_id}")

        failures_for_id = failure_by_id[source_id]
        failure_index = 0
        for event in history:
            status = event["status"]
            if not (status.startswith("failed-") or status in {"cancelled", "skipped"}):
                continue
            if failure_index >= len(failures_for_id):
                raise ReportGateError(f"failure event lacks matching failure row: {source_id}")
            row = failures_for_id[failure_index]
            failure_index += 1
            if _failure_bucket(str(row["category"])) != _event_failure_bucket(status):
                raise ReportGateError(f"failure category mismatch for source ID: {source_id}")
        if failure_index != len(failures_for_id):
            raise ReportGateError(f"unpaired failure rows for source ID: {source_id}")

        current_event = history[-1]
        current_status = current_event["status"]
        if current_status in {"completed", "skipped-resume"}:
            prediction = prediction_by_id[source_id][0]
            resolved = prediction.get("resolved")
            if not isinstance(resolved, bool):
                raise ReportGateError(f"completed prediction requires boolean resolved: {source_id}")
            official[source_id] = resolved
            outcomes[source_id] = resolved
            continue
        if current_status == "failed-scorer":
            current_scorer_ids.add(source_id)
        if current_status.startswith("failed-") or current_status in {"cancelled", "skipped"}:
            if not failures_for_id:
                raise ReportGateError(f"current failure lacks failure row: {source_id}")
            row = failures_for_id[-1]
            raw = str(row["category"])
            bucket = _failure_bucket(raw)
            if bucket != _event_failure_bucket(current_status):
                raise ReportGateError(f"current failure category mismatch for source ID: {source_id}")
            message_sha256 = hashlib.sha256(str(row["message"]).encode("utf-8")).hexdigest()
            detail = f"{current_status};message_sha256={message_sha256}"
            record = {"bucket": bucket, "raw_category": raw, "detail": detail}
            current_failures[bucket][source_id] = record
            if current_status == "failed-scorer":
                unmeasured["scorer"][source_id] = record
            else:
                outcomes[source_id] = False
            continue
        raise ReportGateError(f"unsupported terminal event status: {current_status!r}")

    if len(current_scorer_ids) > MAX_SCORER_FAILURES:
        raise ReportGateError(f"scorer failures exceed {MAX_SCORER_FAILURES}")
    unmeasured_ids = set(unmeasured.get("scorer", {}))
    if set(outcomes) | unmeasured_ids != allowed or set(outcomes) & unmeasured_ids:
        raise ReportGateError("source ID outcome conservation failed")
    if not set(official) <= set(outcomes):
        raise ReportGateError("official verdicts must be outcomes")
    return official, outcomes, dict(unmeasured), dict(current_failures)


def _validate_trace(trace_summary: Mapping[str, Any], source_ids: tuple[str, ...], run_id: str) -> None:
    spans = trace_summary.get("spans")
    if not isinstance(spans, list) or not spans:
        raise ReportGateError("trace-summary.json must have a non-empty spans list")
    allowed = set(source_ids)
    instance_spans: Counter[str] = Counter()
    for span in spans:
        if not isinstance(span, dict):
            raise ReportGateError("trace-summary.json spans must be JSON objects")
        attributes = span.get("attributes", {})
        if not isinstance(attributes, dict):
            raise ReportGateError("trace span attributes must be an object")
        trace_run_id = attributes.get("eval.run_id")
        if trace_run_id not in (None, "") and trace_run_id != run_id:
            raise ReportGateError("trace span eval.run_id must match explicit run")
        source_id = attributes.get("eval.instance_id")
        if source_id not in (None, ""):
            if not isinstance(source_id, str) or source_id not in allowed:
                raise ReportGateError("trace span source ID outside cohort")
            if span.get("name") == "eval.instance":
                instance_spans[source_id] += 1
    for source_id in source_ids:
        if instance_spans[source_id] != 1:
            raise ReportGateError(f"trace requires exactly one eval.instance span: {source_id}")


def _events_by_id(
    source_ids: tuple[str, ...],
    events: list[Any],
) -> dict[str, list[dict[str, Any]]]:
    grouped = {source_id: [] for source_id in source_ids}
    for event in events:
        if isinstance(event, dict):
            source_id = event.get("instance_id", event.get("source_id"))
            if source_id in grouped:
                grouped[source_id].append(event)
    return grouped


def _validate_official_scorer_receipt(
    run_dir: Path,
    source_ids: tuple[str, ...],
    official_verdicts: Mapping[str, bool],
) -> None:
    """Cross-check the retained last official invocation against predictions."""
    report = _read_object(run_dir / "scorer" / "report.json")
    run_summary = _read_object(run_dir / "scorer" / "run-summary.json")
    if len(report) != 1:
        raise ReportGateError("official scorer report must contain one retained invocation")
    source_id, result = next(iter(report.items()))
    if source_id not in source_ids or not isinstance(result, dict):
        raise ReportGateError("official scorer report source ID is invalid")
    resolved = result.get("resolved")
    if not isinstance(resolved, bool):
        raise ReportGateError("official scorer report requires boolean resolved")
    if official_verdicts.get(source_id) is not resolved:
        raise ReportGateError("official scorer verdict disagrees with prediction")

    category_ids = {
        key: run_summary.get(key)
        for key in ("resolved_ids", "unresolved_ids", "empty_patch_ids")
    }
    if any(not isinstance(ids, list) for ids in category_ids.values()):
        raise ReportGateError("official scorer run-summary requires verdict category lists")
    matching_categories = [key for key, ids in category_ids.items() if ids == [source_id]]
    empty_categories = [key for key, ids in category_ids.items() if ids == []]
    if len(matching_categories) != 1 or len(empty_categories) != 2:
        raise ReportGateError(
            "official scorer run-summary resolved, unresolved, or empty_patch categories disagree"
        )
    expected_category = "resolved_ids" if resolved else matching_categories[0]
    if matching_categories[0] != expected_category or (
        not resolved and expected_category not in {"unresolved_ids", "empty_patch_ids"}
    ):
        raise ReportGateError(
            "official scorer run-summary resolved, unresolved, or empty_patch verdict disagrees"
        )

    empty_patch = matching_categories[0] == "empty_patch_ids"
    expected_lists = {
        "submitted_ids": [source_id],
        "completed_ids": [] if empty_patch else [source_id],
        "error_ids": [],
    }
    for key, expected in expected_lists.items():
        if run_summary.get(key) != expected:
            raise ReportGateError(f"official scorer run-summary disagreement for {key}")
    expected_counts = {
        "submitted_instances": 1,
        "completed_instances": int(not empty_patch),
        "resolved_instances": int(resolved),
        "unresolved_instances": int(matching_categories[0] == "unresolved_ids"),
        "empty_patch_instances": int(empty_patch),
        "error_instances": 0,
    }
    for key, expected in expected_counts.items():
        if run_summary.get(key) != expected:
            raise ReportGateError(f"official scorer run-summary disagreement for {key}")


def load_finalized_run(
    arm: str,
    run_id: str,
    *,
    experiment_dir: Path = EXPERIMENT_DIR,
    cohort_path: Path = COHORT_PATH,
) -> FinalizedRun:
    """Load an immutable run only when its evidence satisfies promotion rules."""
    run_dir, arm_root = _resolve_run_dir(arm, run_id, experiment_dir)
    _validate_required_artifacts(run_dir)
    _parse_checksums(run_dir)
    checksum_problems = RunArtifacts(run_id, arm_root).verify_checksums()
    if checksum_problems:
        raise ReportGateError("checksum verification failed: " + "; ".join(checksum_problems))

    summary = _read_object(run_dir / "summary.json")
    manifest = _read_object(run_dir / "run-manifest.json")
    trace_summary = _read_object(run_dir / "traces/trace-summary.json")
    span_assertion = _read_object(run_dir / "traces/span-assertion.json")
    for name, payload in {
        "summary.json": summary,
        "run-manifest.json": manifest,
        "span-assertion.json": span_assertion,
    }.items():
        if payload.get("run_id") != run_id:
            raise ReportGateError(f"{name} run_id must match explicit run")
    if manifest.get("mode") != "official" or manifest.get("synthetic") is not False:
        raise ReportGateError("run-manifest.json must describe an official non-synthetic run")
    if span_assertion.get("verdict") != "PASS":
        raise ReportGateError("span-assertion.json verdict must be PASS")
    manifest_summary = manifest.get("summary")
    if not isinstance(manifest_summary, dict):
        raise ReportGateError("run-manifest.json summary must be an object")
    for key in ("total", "completed", "failed", "skipped"):
        if summary.get(key) != manifest_summary.get(key):
            raise ReportGateError(f"summary disagreement for {key}")
    if summary.get("total") != 20:
        raise ReportGateError("summary total must equal 20")

    source_ids, cohort_sha256 = _load_cohort(Path(cohort_path))
    instances = _read_jsonl(run_dir / "instances.jsonl")
    predictions = _read_jsonl(run_dir / "predictions.jsonl")
    events = _read_jsonl(run_dir / "events.jsonl")
    failures = _read_jsonl(run_dir / "failures.jsonl")
    official, outcomes, unmeasured, failures_by_reason = _normalise_records(
        source_ids, instances, predictions, events, failures
    )
    _validate_official_scorer_receipt(run_dir, source_ids, official)
    terminal_counts = Counter(_terminal_class(event_history[-1]["status"]) for event_history in _events_by_id(source_ids, events).values())
    for key in ("completed", "failed", "skipped"):
        if summary.get(key) != terminal_counts[key]:
            raise ReportGateError(f"summary {key} does not match last terminal events")
    if sum(terminal_counts.values()) != summary["total"]:
        raise ReportGateError("terminal event counts do not match summary total")
    _validate_trace(trace_summary, source_ids, run_id)

    contaminated_reasons = dict(CONTAMINATED)
    excluded = {source_id: contaminated_reasons[source_id] for source_id in source_ids if source_id in contaminated_reasons}
    return FinalizedRun(
        arm=arm,
        run_id=run_id,
        run_dir=run_dir,
        manifest=_freeze_json(manifest),
        summary=_freeze_json(summary),
        manifest_sha256=_sha256(run_dir / "run-manifest.json"),
        checksums_sha256=_sha256(run_dir / "checksums.sha256"),
        cohort_path=Path(cohort_path),
        cohort_sha256=cohort_sha256,
        source_ids=source_ids,
        eligible_ids=frozenset(source_ids) - frozenset(excluded),
        instances=tuple(_freeze_json(row) for row in instances),
        predictions=tuple(_freeze_json(row) for row in predictions),
        events=tuple(_freeze_json(row) for row in events),
        failures=tuple(_freeze_json(row) for row in failures),
        trace_summary=_freeze_json(trace_summary),
        span_assertion=_freeze_json(span_assertion),
        official_verdicts=_freeze_json(official),
        outcomes=_freeze_json(outcomes),
        unmeasured_by_reason=_freeze_json(unmeasured),
        failures_by_reason=_freeze_json(failures_by_reason),
        excluded_contaminated=_freeze_json(excluded),
        scorer_evidence_scope=SCORER_EVIDENCE_SCOPE,
    )


def _canonical_json(payload: Any) -> bytes:
    return json.dumps(
        payload,
        sort_keys=True,
        separators=(",", ":"),
        ensure_ascii=False,
    ).encode("utf-8")


def _arm_receipt_payload(run: FinalizedRun) -> dict[str, Any]:
    return {
        "run_id": run.run_id,
        "manifest_sha256": run.manifest_sha256,
        "checksums_sha256": run.checksums_sha256,
        "official_verdicts": _thaw_json(run.official_verdicts),
        "outcomes": _thaw_json(run.outcomes),
        "unmeasured_by_reason": _thaw_json(run.unmeasured_by_reason),
        "failures_by_reason": _thaw_json(run.failures_by_reason),
        "excluded_contaminated": _thaw_json(run.excluded_contaminated),
        "scorer_evidence_scope": run.scorer_evidence_scope,
    }


def _receipt_payload(
    runs: Mapping[str, FinalizedRun],
    audits: Mapping[str, Mapping[str, Any]],
) -> dict[str, Any]:
    if set(runs) != set(ARMS) or set(audits) != set(ARMS):
        raise ReportGateError("verification requires baseline and optimized arms")
    baseline = runs["baseline"]
    for arm in ARMS:
        run = runs[arm]
        audit = audits[arm]
        if run.arm != arm or audit.get("arm") != arm or audit.get("run_id") != run.run_id:
            raise ReportGateError(f"identity audit does not match {arm} run")
        if audit.get("ok") is not True:
            raise ReportGateError(f"identity audit failed for {arm}")
        if (
            run.source_ids != baseline.source_ids
            or run.eligible_ids != baseline.eligible_ids
            or run.cohort_sha256 != baseline.cohort_sha256
            or run.excluded_contaminated != baseline.excluded_contaminated
        ):
            raise ReportGateError("arms do not share one frozen cohort")
    return {
        "schema_version": 1,
        "verdict": "VERIFIED",
        "cohort": {
            "path": baseline.cohort_path.as_posix(),
            "sha256": baseline.cohort_sha256,
            "source_ids": list(baseline.source_ids),
            "eligible_ids": sorted(baseline.eligible_ids),
        },
        "contamination": _thaw_json(baseline.excluded_contaminated),
        "arms": {arm: _arm_receipt_payload(runs[arm]) for arm in ARMS},
        "identity_audit": {arm: _thaw_json(audits[arm]) for arm in ARMS},
    }


def write_verification_receipt(
    runs: dict[str, FinalizedRun],
    audits: dict[str, dict[str, Any]],
    path: Path,
) -> Path:
    """Atomically pin the two validated runs without copying model answers."""
    payload = _receipt_payload(runs, audits)
    receipt = dict(payload)
    receipt["payload_sha256"] = hashlib.sha256(_canonical_json(payload)).hexdigest()
    path = Path(path)
    temp = path.with_suffix(path.suffix + ".tmp")
    try:
        path.parent.mkdir(parents=True, exist_ok=True)
        with temp.open("w", encoding="utf-8", newline="\n") as handle:
            json.dump(receipt, handle, indent=2, ensure_ascii=False, sort_keys=True)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temp, path)
    except OSError as exc:
        try:
            temp.unlink(missing_ok=True)
        except OSError:
            pass
        raise ReportGateError(f"cannot write verification receipt: {path}") from exc
    return path


def load_verified_runs(
    path: Path,
    *,
    experiment_dir: Path = EXPERIMENT_DIR,
    cohort_path: Path = COHORT_PATH,
) -> dict[str, FinalizedRun]:
    """Reload and fully revalidate both runs named by a verification receipt."""
    receipt = _read_object(Path(path))
    stored_digest = receipt.get("payload_sha256")
    payload = {key: value for key, value in receipt.items() if key != "payload_sha256"}
    if not isinstance(stored_digest, str) or stored_digest != hashlib.sha256(
        _canonical_json(payload)
    ).hexdigest():
        raise ReportGateError("verification receipt payload hash mismatch")
    if receipt.get("schema_version") != 1 or receipt.get("verdict") != "VERIFIED":
        raise ReportGateError("verification receipt schema or verdict is invalid")
    arms_payload = receipt.get("arms")
    if not isinstance(arms_payload, dict) or set(arms_payload) != set(ARMS):
        raise ReportGateError("verification receipt must name both arms")

    runs: dict[str, FinalizedRun] = {}
    for arm in ARMS:
        stored = arms_payload.get(arm)
        if not isinstance(stored, dict):
            raise ReportGateError(f"verification receipt arm is invalid: {arm}")
        run_id = stored.get("run_id")
        if not isinstance(run_id, str):
            raise ReportGateError(f"verification receipt run ID is invalid: {arm}")
        runs[arm] = load_finalized_run(
            arm,
            run_id,
            experiment_dir=Path(experiment_dir),
            cohort_path=Path(cohort_path),
        )

    from eval.swebench_work.verify_arms import audit_run

    audits = {arm: audit_run(run) for arm, run in runs.items()}
    expected = _receipt_payload(runs, audits)
    if payload != expected:
        raise ReportGateError("verification receipt no longer matches finalized artifacts")
    return runs
