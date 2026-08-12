from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path

import pytest

from eval.harness.artifacts import RunArtifacts
from eval.swebench_work import report_gate as G


SOURCE_IDS = tuple(f"repo__case-{number}" for number in range(20))
DEFAULT_CONTAMINATED_ID = "astropy__astropy-12907"


def test_default_locations_and_run_ids_are_approved_values() -> None:
    assert dict(G.DEFAULT_RUNS) == {
        "baseline": "swebench-deepseek-v4-pro-a5cb0378",
        "optimized": "swebench-deepseek-v4-pro-eea6403e",
    }
    with pytest.raises(TypeError):
        G.DEFAULT_RUNS["baseline"] = "other-run"  # type: ignore[index]
    assert G.EXPERIMENT_DIR == Path("eval_results/harness-uplift-20260810")
    assert G.COHORT_PATH == Path("data/eval/swebench/subset-astropy-20.json")
    assert G.DEFAULT_RECEIPT == Path("eval_results/harness-uplift-20260810/verified-arms.json")


def test_default_contaminated_instance_has_a_stable_audit_safe_reason() -> None:
    assert G.CONTAMINATED[DEFAULT_CONTAMINATED_ID] == "known benchmark contamination"


def _cohort(tmp_path: Path, source_ids: tuple[str, ...] = SOURCE_IDS) -> Path:
    path = tmp_path / "cohort.json"
    path.write_text(
        json.dumps({"size": len(source_ids), "instance_ids": list(source_ids)}),
        encoding="utf-8",
    )
    return path


def _trace_summary(run_id: str, source_ids: tuple[str, ...]) -> dict:
    return {
        "spans": [
            {
                "name": "eval.instance",
                "attributes": {
                    "eval.run_id": run_id,
                    "eval.instance_id": source_id,
                },
            }
            for source_id in source_ids
        ]
    }


def _write_finalized_run(
    tmp_path: Path,
    *,
    arm: str = "baseline",
    run_id: str = "final-run",
    source_ids: tuple[str, ...] = SOURCE_IDS,
) -> tuple[Path, Path]:
    """Write a checksum-valid finalized artifact tree using production writer."""
    arm_root = tmp_path / G.ARMS[arm]
    artifacts = RunArtifacts(run_id, arm_root)
    for source_id in source_ids:
        artifacts.record_instance({"instance_id": source_id})
        artifacts.record_prediction({"instance_id": source_id, "resolved": True})
        artifacts.record_event(source_id, "completed")
    artifacts.root.joinpath("failures.jsonl").write_text("", encoding="utf-8")
    summary = {"run_id": run_id, "total": 20, "completed": 20, "failed": 0, "skipped": 0}
    artifacts.write_summary(summary)
    artifacts.write_manifest(
        {
            "run_id": run_id,
            "mode": "official",
            "synthetic": False,
            "summary": dict(summary),
        }
    )
    artifacts.record_trace("trace-summary.json", _trace_summary(run_id, source_ids))
    artifacts.record_trace("span-assertion.json", {"run_id": run_id, "verdict": "PASS"})
    last_source_id = source_ids[-1]
    artifacts.record_scorer_output(
        "report.json",
        json.dumps({last_source_id: {"resolved": True}}) + "\n",
    )
    artifacts.record_scorer_output(
        "run-summary.json",
        json.dumps(
            {
                "submitted_instances": 1,
                "completed_instances": 1,
                "resolved_instances": 1,
                "unresolved_instances": 0,
                "empty_patch_instances": 0,
                "error_instances": 0,
                "submitted_ids": [last_source_id],
                "completed_ids": [last_source_id],
                "resolved_ids": [last_source_id],
                "unresolved_ids": [],
                "empty_patch_ids": [],
                "error_ids": [],
            }
        )
        + "\n",
    )
    artifacts.record_scorer_output("run_instance.log", "official scorer output\n")
    artifacts.record_scorer_output("test_output.txt", "official test output\n")
    artifacts.write_checksums()
    return artifacts.root, _cohort(tmp_path, source_ids)


def _load(tmp_path: Path, **kwargs: object) -> G.FinalizedRun:
    arm = str(kwargs.pop("arm", "baseline"))
    run_id = str(kwargs.pop("run_id", "final-run"))
    run_dir, cohort_path = _write_finalized_run(tmp_path, arm=arm, run_id=run_id)
    del run_dir
    return G.load_finalized_run(
        arm,
        run_id,
        experiment_dir=tmp_path,
        cohort_path=cohort_path,
        **kwargs,
    )


@pytest.mark.parametrize(
    ("field", "value"),
    [("mode", "demo"), ("synthetic", True)],
)
def test_synthetic_or_non_official_run_cannot_be_promoted(
    tmp_path: Path, field: str, value: object
) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    manifest_path = run_dir / "run-manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    manifest[field] = value
    manifest_path.write_text(json.dumps(manifest), encoding="utf-8")
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="official non-synthetic"):
        G.load_finalized_run(
            "baseline",
            "final-run",
            experiment_dir=tmp_path,
            cohort_path=cohort_path,
        )


def test_prediction_verdict_must_match_last_official_scorer_receipt(
    tmp_path: Path,
) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    report_path = run_dir / "scorer" / "report.json"
    report = json.loads(report_path.read_text(encoding="utf-8"))
    report[SOURCE_IDS[-1]]["resolved"] = False
    report_path.write_text(json.dumps(report), encoding="utf-8")
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="official scorer verdict"):
        G.load_finalized_run(
            "baseline",
            "final-run",
            experiment_dir=tmp_path,
            cohort_path=cohort_path,
        )


def test_resolved_official_scorer_receipt_rejects_same_instance_as_empty_patch(
    tmp_path: Path,
) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    summary_path = run_dir / "scorer" / "run-summary.json"
    summary = json.loads(summary_path.read_text(encoding="utf-8"))
    summary["empty_patch_instances"] = 1
    summary["empty_patch_ids"] = [SOURCE_IDS[-1]]
    summary_path.write_text(json.dumps(summary), encoding="utf-8")
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="empty_patch"):
        G.load_finalized_run(
            "baseline",
            "final-run",
            experiment_dir=tmp_path,
            cohort_path=cohort_path,
        )


@pytest.mark.parametrize("run_id", ["../escape", "nested/run", "C:/escape", ""])
def test_run_id_must_be_a_basename(tmp_path: Path, run_id: str) -> None:
    """Resolution must never allow a run ID to select another directory."""
    with pytest.raises(G.ReportGateError, match="run_id"):
        G.load_finalized_run("baseline", run_id, experiment_dir=tmp_path)


def test_live_run_without_final_artifacts_is_blocked(tmp_path: Path) -> None:
    """A partially written live run cannot be promoted as finalized evidence."""
    run = tmp_path / G.ARMS["baseline"] / "live-run"
    run.mkdir(parents=True)
    run.joinpath("instances.jsonl").write_text("{}\n", encoding="utf-8")
    run.joinpath("predictions.jsonl").write_text("", encoding="utf-8")

    with pytest.raises(G.ReportGateError, match="summary.json"):
        G.load_finalized_run("baseline", "live-run", experiment_dir=tmp_path)


def test_run_directory_symlink_to_sibling_is_blocked(tmp_path: Path) -> None:
    """The selected run directory itself must have a stable identity."""
    arm_root = tmp_path / G.ARMS["baseline"]
    target = arm_root / "target-run"
    target.mkdir(parents=True)
    target.joinpath("summary.json").write_text(
        json.dumps({"run_id": "requested-run"}),
        encoding="utf-8",
    )
    target.joinpath("run-manifest.json").write_text(
        json.dumps({"run_id": "requested-run"}),
        encoding="utf-8",
    )
    link = arm_root / "requested-run"
    try:
        os.symlink(target, link, target_is_directory=True)
    except OSError as exc:
        pytest.skip(f"directory symlink unavailable: {exc}")

    with pytest.raises(G.ReportGateError, match="symlink|reparse"):
        G.load_finalized_run("baseline", "requested-run", experiment_dir=tmp_path)


def test_run_directory_reparse_point_is_blocked_without_symlink(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """Windows reparse points must be rejected even without symlink metadata."""
    run = tmp_path / G.ARMS["baseline"] / "requested-run"
    run.mkdir(parents=True)
    assert not run.is_symlink()
    monkeypatch.setattr(G, "_is_reparse_point", lambda path: path == run)

    with pytest.raises(G.ReportGateError, match="reparse"):
        G.load_finalized_run("baseline", "requested-run", experiment_dir=tmp_path)


@pytest.mark.parametrize("missing", sorted(G.REQUIRED_EXISTENCE))
def test_required_artifact_must_exist(tmp_path: Path, missing: str) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    (run_dir / missing).unlink()

    with pytest.raises(G.ReportGateError, match=missing.replace(".", r"\.")):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


@pytest.mark.parametrize("target", sorted(G.REQUIRED_PINNED))
def test_required_pinned_artifact_cannot_be_a_symlink(tmp_path: Path, target: str) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    path = run_dir / target
    replacement = run_dir / "replacement"
    replacement.write_text(path.read_text(encoding="utf-8"), encoding="utf-8")
    path.unlink()
    try:
        os.symlink(replacement, path)
    except OSError as exc:
        pytest.skip(f"file symlink unavailable: {exc}")

    with pytest.raises(G.ReportGateError, match=target.replace(".", r"\.")):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_corrupt_pinned_file_is_blocked(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    run_dir.joinpath("summary.json").write_text("{}", encoding="utf-8")

    with pytest.raises(G.ReportGateError, match="mismatch:summary.json"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_unpinned_artifact_is_blocked(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    run_dir.joinpath("surprise.txt").write_text("not pinned", encoding="utf-8")

    with pytest.raises(G.ReportGateError, match="unpinned:surprise.txt"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


@pytest.mark.parametrize("verdict", ["FAIL", "INCOMPLETE"])
def test_nonpassing_span_assertion_is_blocked(tmp_path: Path, verdict: str) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    assertion = json.loads(run_dir.joinpath("traces/span-assertion.json").read_text(encoding="utf-8"))
    assertion["verdict"] = verdict
    run_dir.joinpath("traces/span-assertion.json").write_text(json.dumps(assertion), encoding="utf-8")
    RunArtifacts("final-run", tmp_path / G.ARMS["baseline"]).write_checksums()

    with pytest.raises(G.ReportGateError, match="span-assertion.json.*PASS"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_summary_total_must_be_exactly_twenty(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    for name in ("summary.json", "run-manifest.json"):
        payload = json.loads(run_dir.joinpath(name).read_text(encoding="utf-8"))
        summary = payload["summary"] if name == "run-manifest.json" else payload
        summary.update({"total": 19, "completed": 19})
        run_dir.joinpath(name).write_text(json.dumps(payload), encoding="utf-8")
    RunArtifacts("final-run", tmp_path / G.ARMS["baseline"]).write_checksums()

    with pytest.raises(G.ReportGateError, match="total.*20"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_trace_span_run_id_must_match_explicit_run(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    trace = json.loads(run_dir.joinpath("traces/trace-summary.json").read_text(encoding="utf-8"))
    trace["spans"][0]["attributes"]["eval.run_id"] = "wrong-run"
    run_dir.joinpath("traces/trace-summary.json").write_text(json.dumps(trace), encoding="utf-8")
    RunArtifacts("final-run", tmp_path / G.ARMS["baseline"]).write_checksums()

    with pytest.raises(G.ReportGateError, match="eval.run_id"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


@pytest.mark.parametrize(
    ("entry", "match"),
    [
        ("0" * 64 + "  checksums.sha256", "self-pin"),
        ("0" * 63 + "x  summary.json", "checksum manifest"),
        ("0" * 64 + "  /escape", "unsafe checksum path"),
        ("0" * 64 + "  C:/escape", "unsafe checksum path"),
        ("0" * 64 + "  ../escape", "unsafe checksum path"),
        ("0" * 64 + "  ./summary.json", "unsafe checksum path"),
        ("0" * 64 + "  scorer//report.json", "unsafe checksum path"),
        ("0" * 64 + "  traces\\trace-summary.json", "unsafe checksum path"),
        ("0" * 64 + "  scorer/name:bad", "unsafe checksum path"),
    ],
)
def test_checksum_manifest_rejects_unsafe_entries(
    tmp_path: Path,
    entry: str,
    match: str,
) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    run_dir.joinpath("checksums.sha256").write_text(entry + "\n", encoding="utf-8")

    with pytest.raises(G.ReportGateError, match=match):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_checksum_manifest_rejects_duplicate_path(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    manifest = run_dir.joinpath("checksums.sha256")
    lines = manifest.read_text(encoding="utf-8").splitlines()
    manifest.write_text("\n".join([*lines, lines[0]]) + "\n", encoding="utf-8")

    with pytest.raises(G.ReportGateError, match="duplicate checksum path"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_checksum_manifest_rejects_raw_alias_before_duplicate_normalization(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    manifest = run_dir.joinpath("checksums.sha256")
    summary_line = next(
        line for line in manifest.read_text(encoding="utf-8").splitlines() if line.endswith("  summary.json")
    )
    digest = summary_line.split("  ", 1)[0]
    manifest.write_text(
        manifest.read_text(encoding="utf-8") + f"{digest}  ./summary.json\n",
        encoding="utf-8",
    )

    with pytest.raises(G.ReportGateError, match="unsafe checksum path"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_required_file_must_be_pinned_even_when_present(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    manifest = run_dir.joinpath("checksums.sha256")
    manifest.write_text(
        "\n".join(line for line in manifest.read_text(encoding="utf-8").splitlines() if "summary.json" not in line) + "\n",
        encoding="utf-8",
    )

    with pytest.raises(G.ReportGateError, match="summary.json"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_manifest_summary_counts_must_match_summary(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    manifest = json.loads(run_dir.joinpath("run-manifest.json").read_text(encoding="utf-8"))
    manifest["summary"]["completed"] = 19
    manifest["summary"]["failed"] = 1
    run_dir.joinpath("run-manifest.json").write_text(json.dumps(manifest), encoding="utf-8")
    RunArtifacts("final-run", tmp_path / G.ARMS["baseline"]).write_checksums()

    with pytest.raises(G.ReportGateError, match="summary"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


@pytest.mark.parametrize("artifact", ["summary.json", "run-manifest.json", "traces/span-assertion.json"])
def test_embedded_run_id_must_match_explicit_run(tmp_path: Path, artifact: str) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    payload = json.loads(run_dir.joinpath(artifact).read_text(encoding="utf-8"))
    payload["run_id"] = "wrong-run"
    run_dir.joinpath(artifact).write_text(json.dumps(payload), encoding="utf-8")
    RunArtifacts("final-run", tmp_path / G.ARMS["baseline"]).write_checksums()

    with pytest.raises(G.ReportGateError, match="run_id"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def _rewrite_records(
    run_dir: Path,
    records: dict[str, list[dict[str, object]]],
) -> None:
    for name, rows in records.items():
        run_dir.joinpath(name).write_text(
            "".join(json.dumps(row) + "\n" for row in rows),
            encoding="utf-8",
        )


def _refresh_checksums(tmp_path: Path, arm: str = "baseline", run_id: str = "final-run") -> None:
    RunArtifacts(run_id, tmp_path / G.ARMS[arm]).write_checksums()


def _records_for(source_ids: tuple[str, ...] = SOURCE_IDS) -> dict[str, list[dict[str, object]]]:
    return {
        "instances.jsonl": [{"instance_id": source_id} for source_id in source_ids],
        "predictions.jsonl": [
            {"instance_id": source_id, "resolved": True} for source_id in source_ids
        ],
        "events.jsonl": [{"instance_id": source_id, "status": "completed"} for source_id in source_ids],
        "failures.jsonl": [],
    }


def _set_summary_counts(run_dir: Path, *, completed: int, failed: int, skipped: int) -> None:
    summary = json.loads(run_dir.joinpath("summary.json").read_text(encoding="utf-8"))
    manifest = json.loads(run_dir.joinpath("run-manifest.json").read_text(encoding="utf-8"))
    for payload in (summary, manifest["summary"]):
        payload.update({"total": 20, "completed": completed, "failed": failed, "skipped": skipped})
    run_dir.joinpath("summary.json").write_text(json.dumps(summary), encoding="utf-8")
    run_dir.joinpath("run-manifest.json").write_text(json.dumps(manifest), encoding="utf-8")


def test_summary_counts_must_match_last_terminal_events(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    records = _records_for()
    source_id = SOURCE_IDS[0]
    records["events.jsonl"][0] = {"instance_id": source_id, "status": "failed-scorer"}
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    records["failures.jsonl"] = [{"instance_id": source_id, "category": "scorer", "message": "unavailable"}]
    _rewrite_records(run_dir, records)
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="summary completed.*last terminal"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_instance_rows_match_recorded_scorer_and_completed_attempts(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    run_dir.joinpath("instances.jsonl").write_text("", encoding="utf-8")
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="instance rows"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_completed_and_scorer_terminal_require_instance_row(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    records = _records_for()
    source_id = SOURCE_IDS[0]
    records["events.jsonl"][0] = {"instance_id": source_id, "status": "failed-scorer"}
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    records["instances.jsonl"] = records["instances.jsonl"][1:]
    records["failures.jsonl"] = [{"instance_id": source_id, "category": "scorer", "message": "offline"}]
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=19, failed=1, skipped=0)
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="instance rows"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_pre_record_non_scorer_failure_may_lack_instance_row(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    records = _records_for()
    source_id = SOURCE_IDS[0]
    records["events.jsonl"][0] = {"instance_id": source_id, "status": "failed-agent"}
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    records["instances.jsonl"] = records["instances.jsonl"][1:]
    records["failures.jsonl"] = [{"instance_id": source_id, "category": "agent", "message": "before record"}]
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=19, failed=1, skipped=0)
    _refresh_checksums(tmp_path)

    run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
    assert run.outcomes[source_id] is False
    assert source_id not in run.official_verdicts


def test_unaccounted_duplicate_instance_is_blocked(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    with run_dir.joinpath("instances.jsonl").open("a", encoding="utf-8") as handle:
        handle.write(json.dumps({"instance_id": SOURCE_IDS[0]}) + "\n")
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="unaccounted instance"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


@pytest.mark.parametrize("artifact", ["predictions.jsonl", "failures.jsonl", "events.jsonl"])
def test_prediction_failure_and_event_ids_must_belong_to_cohort(
    tmp_path: Path,
    artifact: str,
) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    with run_dir.joinpath(artifact).open("a", encoding="utf-8") as handle:
        handle.write(json.dumps({"instance_id": "foreign__case", "resolved": True}) + "\n")
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="outside cohort"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_every_source_id_is_exactly_outcome_or_scorer_unmeasured(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    records = _records_for()
    source_id = SOURCE_IDS[0]
    records["events.jsonl"][0] = {"instance_id": source_id, "status": "failed-scorer"}
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    records["failures.jsonl"] = [{"instance_id": source_id, "category": "scorer", "message": "unavailable"}]
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=19, failed=1, skipped=0)
    _refresh_checksums(tmp_path)

    run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
    assert set(run.outcomes) | set(run.unmeasured_by_reason["scorer"]) == set(SOURCE_IDS)
    assert not set(run.outcomes) & set(run.unmeasured_by_reason["scorer"])


def test_resolved_false_with_failed_scorer_event_is_unmeasured(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    records = _records_for()
    source_id = SOURCE_IDS[0]
    records["events.jsonl"][0] = {"instance_id": source_id, "status": "failed-scorer"}
    records["predictions.jsonl"][0]["resolved"] = False
    records["failures.jsonl"] = [{"instance_id": source_id, "category": "scorer", "message": "partial"}]
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=19, failed=1, skipped=0)
    _refresh_checksums(tmp_path)

    run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
    assert source_id not in run.outcomes
    assert source_id in run.unmeasured_by_reason["scorer"]


def test_non_scorer_failure_is_false_outcome_not_official_verdict(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    records = _records_for()
    source_id = SOURCE_IDS[0]
    records["events.jsonl"][0] = {"instance_id": source_id, "status": "failed-agent"}
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    records["instances.jsonl"] = records["instances.jsonl"][1:]
    records["failures.jsonl"] = [{"instance_id": source_id, "category": "agent", "message": "agent stopped"}]
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=19, failed=1, skipped=0)
    _refresh_checksums(tmp_path)

    run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
    assert run.outcomes[source_id] is False
    assert source_id not in run.official_verdicts


@pytest.mark.parametrize("scorer_count", [3, 4])
def test_scorer_failure_threshold(tmp_path: Path, scorer_count: int) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    records = _records_for()
    failures = []
    for source_id in SOURCE_IDS[:scorer_count]:
        records["events.jsonl"][SOURCE_IDS.index(source_id)] = {
            "instance_id": source_id,
            "status": "failed-scorer",
        }
        records["predictions.jsonl"] = [row for row in records["predictions.jsonl"] if row["instance_id"] != source_id]
        failures.append({"instance_id": source_id, "category": "scorer", "message": "down"})
    records["failures.jsonl"] = failures
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=20 - scorer_count, failed=scorer_count, skipped=0)
    _refresh_checksums(tmp_path)

    if scorer_count == 3:
        run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
        assert len(run.unmeasured_by_reason["scorer"]) == 3
    else:
        with pytest.raises(G.ReportGateError, match="scorer failures exceed 3"):
            G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_repeated_failure_attempts_reconcile_with_failure_rows(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    source_id = SOURCE_IDS[0]
    records = _records_for()
    records["events.jsonl"][0:1] = [
        {"instance_id": source_id, "status": "failed-scorer"},
        {"instance_id": source_id, "status": "failed-scorer"},
    ]
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    records["instances.jsonl"].append({"instance_id": source_id})
    records["failures.jsonl"] = [
        {"instance_id": source_id, "category": "scorer", "message": "first"},
        {"instance_id": source_id, "category": "scorer", "message": "second"},
    ]
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=19, failed=1, skipped=0)
    _refresh_checksums(tmp_path)

    run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
    assert run.failures_by_reason["scorer"][source_id]["raw_category"] == "scorer"


@pytest.mark.parametrize("terminal", ["failed-agent", "cancelled", "skipped"])
def test_non_scorer_terminal_cannot_be_followed_by_another_event(
    tmp_path: Path,
    terminal: str,
) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    source_id = SOURCE_IDS[0]
    records = _records_for()
    records["events.jsonl"][0:1] = [
        {"instance_id": source_id, "status": terminal},
        {"instance_id": source_id, "status": "completed"},
    ]
    records["failures.jsonl"] = [
        {"instance_id": source_id, "category": G._event_category(terminal), "message": "terminal"}
    ]
    _rewrite_records(run_dir, records)
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="event after non-scorer terminal"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_failed_then_completed_retry_uses_completed_outcome(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    source_id = SOURCE_IDS[0]
    records = _records_for()
    records["events.jsonl"][0:1] = [
        {"instance_id": source_id, "status": "failed-scorer"},
        {"instance_id": source_id, "status": "completed"},
    ]
    records["instances.jsonl"].append({"instance_id": source_id})
    records["failures.jsonl"] = [{"instance_id": source_id, "category": "scorer", "message": "retry"}]
    _rewrite_records(run_dir, records)
    _refresh_checksums(tmp_path)

    run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
    assert run.official_verdicts[source_id] is True
    assert source_id not in run.failures_by_reason.get("scorer", {})


def test_completed_then_multiple_skipped_resume_preserves_official_verdict(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    source_id = SOURCE_IDS[0]
    records = _records_for()
    records["events.jsonl"][0:1] = [
        {"instance_id": source_id, "status": "completed"},
        {"instance_id": source_id, "status": "skipped-resume"},
        {"instance_id": source_id, "status": "skipped-resume"},
    ]
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=19, failed=0, skipped=1)
    _refresh_checksums(tmp_path)

    run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
    assert run.official_verdicts[source_id] is True
    assert run.outcomes[source_id] is True


def test_skipped_resume_without_prediction_is_blocked(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    source_id = SOURCE_IDS[0]
    records = _records_for()
    records["events.jsonl"][0] = {"instance_id": source_id, "status": "skipped-resume"}
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    records["instances.jsonl"] = records["instances.jsonl"][1:]
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=19, failed=0, skipped=1)
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="skipped-resume"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_unknown_instance_in_trace_is_blocked(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    trace = json.loads(run_dir.joinpath("traces/trace-summary.json").read_text(encoding="utf-8"))
    trace["spans"].append({"name": "search", "attributes": {"eval.instance_id": "foreign__case"}})
    run_dir.joinpath("traces/trace-summary.json").write_text(json.dumps(trace), encoding="utf-8")
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="outside cohort"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_duplicate_instance_terminal_spans_are_blocked(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    trace = json.loads(run_dir.joinpath("traces/trace-summary.json").read_text(encoding="utf-8"))
    trace["spans"].append(dict(trace["spans"][0]))
    run_dir.joinpath("traces/trace-summary.json").write_text(json.dumps(trace), encoding="utf-8")
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="exactly one"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_missing_instance_terminal_span_is_blocked(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    trace = json.loads(run_dir.joinpath("traces/trace-summary.json").read_text(encoding="utf-8"))
    trace["spans"].pop()
    run_dir.joinpath("traces/trace-summary.json").write_text(json.dumps(trace), encoding="utf-8")
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="exactly one"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_scorer_raw_scope_is_last_invocation_only(tmp_path: Path) -> None:
    run = _load(tmp_path)
    assert run.scorer_evidence_scope == "last-invocation-only"


@pytest.mark.parametrize(
    ("status", "raw", "bucket"),
    [
        ("failed-scorer", "scorer", "scorer"),
        ("failed-agent", "agent", "agent"),
        ("failed-budget", "budget", "budget"),
        ("failed-timeout", "timeout", "timeout"),
        ("failed-oom", "oom", "oom"),
        ("failed-infra", "infra", "infra"),
        ("cancelled", "cancelled", "cancelled/skipped"),
        ("skipped", "skipped", "cancelled/skipped"),
        ("failed-something-new", "something-new", "unknown"),
    ],
)
def test_failure_taxonomy_preserves_raw_category_and_bucket(
    tmp_path: Path,
    status: str,
    raw: str,
    bucket: str,
) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    source_id = SOURCE_IDS[0]
    records = _records_for()
    records["events.jsonl"][0] = {"instance_id": source_id, "status": status}
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    if status != "failed-scorer":
        records["instances.jsonl"] = records["instances.jsonl"][1:]
    records["failures.jsonl"] = [{"instance_id": source_id, "category": raw, "message": "path C:/secret"}]
    _rewrite_records(run_dir, records)
    _set_summary_counts(
        run_dir,
        completed=19,
        failed=1 if status.startswith("failed-") else 0,
        skipped=1 if status in {"cancelled", "skipped"} else 0,
    )
    _refresh_checksums(tmp_path)

    run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
    record = run.failures_by_reason[bucket][source_id]
    expected_detail = f"{status};message_sha256={hashlib.sha256(b'path C:/secret').hexdigest()}"
    assert record == {"bucket": bucket, "raw_category": raw, "detail": expected_detail}


@pytest.mark.parametrize(
    ("status", "raw"),
    [
        ("failed-cancelled", "cancelled"),
        ("failed-skipped", "skipped"),
        ("failed-cancelled", "agent"),
        ("failed-skipped", "infra"),
        ("failed-agent", "cancelled"),
        ("failed-scorer", "skipped"),
    ],
)
def test_failure_event_and_row_categories_must_match_after_normalization(
    tmp_path: Path,
    status: str,
    raw: str,
) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    source_id = SOURCE_IDS[0]
    records = _records_for()
    records["events.jsonl"][0] = {"instance_id": source_id, "status": status}
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    if status != "failed-scorer":
        records["instances.jsonl"] = records["instances.jsonl"][1:]
    records["failures.jsonl"] = [{"instance_id": source_id, "category": raw, "message": "mismatch"}]
    _rewrite_records(run_dir, records)
    _set_summary_counts(
        run_dir,
        completed=19,
        failed=1 if status.startswith("failed-") else 0,
        skipped=1 if status in {"cancelled", "skipped"} else 0,
    )
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="failure category mismatch"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)



@pytest.mark.parametrize(
    ("status", "raw"),
    [
        ("cancelled", "agent"),
        ("skipped", "scorer"),
        ("cancelled", "infra"),
        ("skipped", "budget"),
    ],
)
def test_direct_cancelled_or_skipped_rejects_raw_category_outside_its_bucket(
    tmp_path: Path,
    status: str,
    raw: str,
) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    source_id = SOURCE_IDS[0]
    records = _records_for()
    records["events.jsonl"][0] = {"instance_id": source_id, "status": status}
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    records["instances.jsonl"] = records["instances.jsonl"][1:]
    records["failures.jsonl"] = [{"instance_id": source_id, "category": raw, "message": "mismatch"}]
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=19, failed=0, skipped=1)
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="failure category mismatch"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


@pytest.mark.parametrize(
    ("status", "raw"),
    [("cancelled", "skipped"), ("skipped", "cancelled")],
)
def test_direct_cancelled_and_skipped_accept_same_bucket_raw_category(
    tmp_path: Path,
    status: str,
    raw: str,
) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    source_id = SOURCE_IDS[0]
    records = _records_for()
    records["events.jsonl"][0] = {"instance_id": source_id, "status": status}
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    records["instances.jsonl"] = records["instances.jsonl"][1:]
    records["failures.jsonl"] = [{"instance_id": source_id, "category": raw, "message": "same bucket"}]
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=19, failed=0, skipped=1)
    _refresh_checksums(tmp_path)

    run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
    assert run.failures_by_reason["cancelled/skipped"][source_id]["raw_category"] == raw


@pytest.mark.parametrize(
    ("next_status", "raw"),
    [
        ("failed-scorer", "scorer"),
        ("failed-agent", "agent"),
        ("failed-budget", "budget"),
        ("failed-timeout", "timeout"),
        ("failed-oom", "oom"),
        ("failed-infra", "infra"),
        ("failed-cancelled", "cancelled"),
        ("failed-skipped", "skipped"),
        ("cancelled", "cancelled"),
        ("skipped", "skipped"),
    ],
)
def test_completed_cannot_be_followed_by_any_non_resume_terminal(
    tmp_path: Path,
    next_status: str,
    raw: str,
) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    source_id = SOURCE_IDS[0]
    records = _records_for()
    records["events.jsonl"][0:1] = [
        {"instance_id": source_id, "status": "completed"},
        {"instance_id": source_id, "status": next_status},
    ]
    records["failures.jsonl"] = [{"instance_id": source_id, "category": raw, "message": "after completed"}]
    if next_status == "failed-scorer":
        records["instances.jsonl"].append({"instance_id": source_id})
    _rewrite_records(run_dir, records)
    _set_summary_counts(
        run_dir,
        completed=19,
        failed=1 if next_status.startswith("failed-") else 0,
        skipped=1 if next_status in {"cancelled", "skipped"} else 0,
    )
    _refresh_checksums(tmp_path)

    with pytest.raises(G.ReportGateError, match="event after completed"):
        G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)


def test_finalized_run_nested_mappings_are_read_only(tmp_path: Path) -> None:
    run = _load(tmp_path)
    source_id = SOURCE_IDS[0]
    before = G._thaw_json(run.manifest), dict(run.outcomes)
    with pytest.raises(TypeError):
        run.manifest["run_id"] = "wrong"  # type: ignore[index]
    with pytest.raises(TypeError):
        run.outcomes[source_id] = False  # type: ignore[index]
    assert (G._thaw_json(run.manifest), dict(run.outcomes)) == before


def test_failure_receipt_nested_mapping_is_read_only(tmp_path: Path) -> None:
    run_dir, cohort_path = _write_finalized_run(tmp_path)
    source_id = SOURCE_IDS[0]
    records = _records_for()
    records["events.jsonl"][0] = {"instance_id": source_id, "status": "failed-agent"}
    records["predictions.jsonl"] = records["predictions.jsonl"][1:]
    records["instances.jsonl"] = records["instances.jsonl"][1:]
    records["failures.jsonl"] = [{"instance_id": source_id, "category": "agent", "message": "safe message"}]
    _rewrite_records(run_dir, records)
    _set_summary_counts(run_dir, completed=19, failed=1, skipped=0)
    _refresh_checksums(tmp_path)

    run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
    before = G._thaw_json(run.failures_by_reason), dict(run.outcomes)
    with pytest.raises(TypeError):
        run.failures_by_reason["agent"][source_id]["raw_category"] = "wrong"  # type: ignore[index]
    assert (G._thaw_json(run.failures_by_reason), dict(run.outcomes)) == before


def test_default_contaminated_id_stays_in_source_but_not_eligible(tmp_path: Path) -> None:
    source_ids = (DEFAULT_CONTAMINATED_ID, *SOURCE_IDS[1:])
    run_dir, cohort_path = _write_finalized_run(tmp_path, source_ids=source_ids)

    run = G.load_finalized_run("baseline", "final-run", experiment_dir=tmp_path, cohort_path=cohort_path)
    assert DEFAULT_CONTAMINATED_ID in run.source_ids
    assert any(row["instance_id"] == DEFAULT_CONTAMINATED_ID for row in run.instances)
    assert DEFAULT_CONTAMINATED_ID not in run.eligible_ids
    assert run.excluded_contaminated[DEFAULT_CONTAMINATED_ID] == "known benchmark contamination"




def _receipt_fixture(
    tmp_path: Path,
) -> tuple[dict[str, G.FinalizedRun], dict[str, dict[str, object]], Path]:
    from eval.swebench_work.verify_arms import MANDATE_MARKER, audit_run

    cohort_path: Path | None = None
    for arm, run_id in (("baseline", "baseline-run"), ("optimized", "optimized-run")):
        run_dir, cohort_path = _write_finalized_run(tmp_path, arm=arm, run_id=run_id)
        manifest = json.loads(run_dir.joinpath("run-manifest.json").read_text(encoding="utf-8"))
        manifest["harness_uplift"] = {
            "enabled": arm == "optimized",
            "components": (
                ["localization", "tool_rounds", "edit_mandate", "validation"]
                if arm == "optimized"
                else []
            ),
            "tool_rounds": 24 if arm == "optimized" else 8,
        }
        run_dir.joinpath("run-manifest.json").write_text(
            json.dumps(manifest), encoding="utf-8"
        )
        if arm == "optimized":
            rows = [
                json.loads(line)
                for line in run_dir.joinpath("instances.jsonl").read_text(encoding="utf-8").splitlines()
                if line.strip()
            ]
            for row in rows:
                row["metadata"] = {"localization": {"files": ["pkg/x.py"]}}
                row["task_description"] = f"Fix the defect\n\n{MANDATE_MARKER}"
            run_dir.joinpath("instances.jsonl").write_text(
                "".join(json.dumps(row) + "\n" for row in rows),
                encoding="utf-8",
            )
        _refresh_checksums(tmp_path, arm=arm, run_id=run_id)

    assert cohort_path is not None
    runs = {
        arm: G.load_finalized_run(
            arm,
            run_id,
            experiment_dir=tmp_path,
            cohort_path=cohort_path,
        )
        for arm, run_id in (("baseline", "baseline-run"), ("optimized", "optimized-run"))
    }
    audits = {arm: audit_run(run) for arm, run in runs.items()}
    assert all(audit["ok"] for audit in audits.values())
    return runs, audits, cohort_path


def test_receipt_round_trip_reloads_same_runs(tmp_path: Path) -> None:
    runs, audits, cohort_path = _receipt_fixture(tmp_path)
    receipt = tmp_path / "verified-arms.json"
    G.write_verification_receipt(runs, audits, receipt)

    reloaded = G.load_verified_runs(
        receipt,
        experiment_dir=tmp_path,
        cohort_path=cohort_path,
    )
    assert {arm: run.run_id for arm, run in reloaded.items()} == {
        "baseline": "baseline-run",
        "optimized": "optimized-run",
    }
    text = receipt.read_text(encoding="utf-8")
    assert '"verdict": "VERIFIED"' in text
    for forbidden in ("model_patch", "test_patch", "FAIL_TO_PASS", "PASS_TO_PASS"):
        assert forbidden not in text


def test_rehashed_normalized_outcome_tamper_is_blocked(tmp_path: Path) -> None:
    runs, audits, cohort_path = _receipt_fixture(tmp_path)
    receipt = tmp_path / "verified-arms.json"
    G.write_verification_receipt(runs, audits, receipt)
    payload = json.loads(receipt.read_text(encoding="utf-8"))
    payload["arms"]["baseline"]["outcomes"][SOURCE_IDS[0]] = False
    canonical = {key: value for key, value in payload.items() if key != "payload_sha256"}
    payload["payload_sha256"] = hashlib.sha256(G._canonical_json(canonical)).hexdigest()
    receipt.write_text(json.dumps(payload), encoding="utf-8")

    with pytest.raises(G.ReportGateError, match="no longer matches"):
        G.load_verified_runs(receipt, experiment_dir=tmp_path, cohort_path=cohort_path)


def test_changed_artifact_after_receipt_is_blocked(tmp_path: Path) -> None:
    runs, audits, cohort_path = _receipt_fixture(tmp_path)
    receipt = tmp_path / "verified-arms.json"
    G.write_verification_receipt(runs, audits, receipt)
    runs["optimized"].run_dir.joinpath("run-manifest.json").write_text(
        "{}", encoding="utf-8"
    )

    with pytest.raises(G.ReportGateError, match="checksum verification failed"):
        G.load_verified_runs(receipt, experiment_dir=tmp_path, cohort_path=cohort_path)


def test_failed_receipt_validation_does_not_overwrite_existing_file(tmp_path: Path) -> None:
    runs, audits, _ = _receipt_fixture(tmp_path)
    receipt = tmp_path / "verified-arms.json"
    receipt.write_bytes(b"keep-existing")
    audits["optimized"]["ok"] = False

    with pytest.raises(G.ReportGateError, match="identity audit failed"):
        G.write_verification_receipt(runs, audits, receipt)
    assert receipt.read_bytes() == b"keep-existing"
