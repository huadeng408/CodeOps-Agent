from __future__ import annotations

import json
import sqlite3
import time
from pathlib import Path
from types import SimpleNamespace

from eval.harness import fault_injection
from eval.harness.fault_injection import (
    FaultInjectionConfig,
    _source_pin,
    run_fault_injection,
    verify_receipt,
)
from orchestrator.workflows import SQLiteWorkflowStore, WorkerResult, WorkerState, WorkflowRun


class _FinishedProcess:
    def __init__(self, exit_code: int) -> None:
        self.exit_code = exit_code

    def poll(self) -> int:
        return self.exit_code

    def wait(self, timeout: float | None = None) -> int:
        return self.exit_code


class _RunningProcess:
    def poll(self) -> None:
        return None


def test_fault_injection_recovers_real_processes_and_emits_receipt(tmp_path: Path) -> None:
    config = FaultInjectionConfig(
        run_id="fault-test",
        artifact_root=tmp_path / "artifacts",
        worker_count=2,
        task_count=6,
        fault_count=2,
        task_duration_s=0.08,
        fault_interval_s=0.04,
        timeout_s=30.0,
    )

    receipt = run_fault_injection(config)

    assert receipt["status"] == "SMOKE_VERIFIED"
    assert len(receipt["trace_id"]) == 32
    assert receipt["exit_code"] == 2
    assert receipt["run_id"] == "fault-test"
    assert receipt["raw_evidence"]["artifact_root"]
    assert receipt["raw_evidence"]["task_manifest_sha256"] == receipt["data_pin"]["task_manifest_sha256"]
    assert receipt["budget"]["worker_count"] == 2
    assert receipt["budget"]["task_count"] == 6
    assert receipt["budget"]["stage_count"] == 3
    assert receipt["budget"]["lease_ttl_s"] == 0.5
    assert receipt["budget"]["canonical"] is False
    assert receipt["budget"]["canonical_target"] == {
        "worker_count": 8,
        "task_count": 200,
        "fault_count": 30,
        "stage_count": 3,
    }
    assert receipt["fault_injection"]["requested"] == 2
    assert receipt["fault_injection"]["applied"] == 2
    assert receipt["fault_injection"]["termination_attempts"] >= 2
    assert all(event["stage_id"].startswith("stage-") for event in receipt["fault_injection"]["evidence"])
    assert receipt["recovery"]["denominator"] == 6
    assert receipt["recovery"]["successes"] == 6
    assert receipt["recovery"]["success_rate"] == 1.0
    assert receipt["recovery"]["stage_denominator"] == 18
    assert receipt["recovery"]["completed_stages"] == 18
    assert receipt["recovery"]["resume_events"] >= 2
    assert receipt["recovery"]["checkpoint_complete"] is True
    assert receipt["sqlite"]["event_count"] > 0
    assert receipt["sqlite"]["completed_stage_count"] == 18
    assert len(receipt["processes"]["exit_codes"]) >= 2
    assert verify_receipt(config.artifact_root / config.run_id) == []
    artifact_path = config.artifact_root / config.run_id / "workflow.sqlite"
    artifact_store = sqlite3.connect(f"file:{artifact_path.as_posix()}?mode=ro", uri=True)
    try:
        artifact_event_count = artifact_store.execute("select count(*) from workflow_events").fetchone()[0]
        assert artifact_event_count == receipt["sqlite"]["event_count"]
        assert artifact_store.execute("select count(*) from workflow_checkpoints").fetchone()[0] == 6
        checkpoints = artifact_store.execute("select state_json from workflow_checkpoints").fetchall()
        assert all(len(json.loads(row[0])["workers"]) == 3 for row in checkpoints)
    finally:
        artifact_store.close()
    assert verify_receipt(config.artifact_root / config.run_id) == []


def test_fault_injection_receipt_is_tamper_evident(tmp_path: Path) -> None:
    config = FaultInjectionConfig(
        run_id="tamper-test",
        artifact_root=tmp_path / "artifacts",
        worker_count=1,
        task_count=1,
        fault_count=0,
        task_duration_s=0.01,
        timeout_s=10.0,
    )

    run_fault_injection(config)
    receipt_path = config.artifact_root / config.run_id / "receipt.json"
    payload = json.loads(receipt_path.read_text(encoding="utf-8"))
    payload["status"] = "TAMPERED"
    receipt_path.write_text(json.dumps(payload) + "\n", encoding="utf-8")

    problems = verify_receipt(config.artifact_root / config.run_id)

    assert any(problem.startswith("mismatch:receipt.json") for problem in problems)


def test_fault_injection_receipt_does_not_include_environment_secrets(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.setenv("OPENAI_API_KEY", "sk-test-secret-that-must-not-be-written")
    config = FaultInjectionConfig(
        run_id="secret-test",
        artifact_root=tmp_path / "artifacts",
        worker_count=1,
        task_count=1,
        fault_count=0,
        task_duration_s=0.01,
        timeout_s=10.0,
    )

    run_fault_injection(config)
    text = "\n".join(
        path.read_text(encoding="utf-8", errors="ignore")
        for path in config.artifact_root.rglob("*")
        if path.is_file()
    )

    assert "sk-test-secret" not in text
    assert "OPENAI_API_KEY" not in text


def test_unexpected_child_exit_is_recorded_and_not_verified(tmp_path: Path) -> None:
    config = FaultInjectionConfig(
        run_id="unexpected-child",
        artifact_root=tmp_path / "artifacts",
        worker_count=1,
        task_count=1,
        fault_count=0,
        task_duration_s=0.01,
        timeout_s=10.0,
        crash_once_task_id="task-0001",
    )

    receipt = run_fault_injection(config)

    assert receipt["status"] == "INCOMPLETE"
    assert receipt["exit_code"] == 2
    assert any(item["category"] == "process_exit" for item in receipt["failures"])
    failure = next(item for item in receipt["failures"] if item["category"] == "process_exit")
    assert "unexpected child failure" in failure["detail"]


def test_source_pin_records_commit_and_worktree_inventory() -> None:
    pin = _source_pin()

    assert len(pin["git_sha"]) == 40
    assert len(pin["dirty_hash"]) == 64
    assert int(pin["untracked_files"]) >= 0


def test_fault_target_rejects_progress_from_previous_process(tmp_path: Path) -> None:
    progress_root = tmp_path / "progress"
    progress_root.mkdir()
    (progress_root / "worker-0.json").write_text(
        json.dumps({"slot": 0, "pid": 111, "task_id": "stale-task", "state": "running"}),
        encoding="utf-8",
    )
    record = fault_injection._ProcessRecord(
        process_id="current-process",
        slot=0,
        pid=222,
        started_at=time.time(),
    )

    store = SQLiteWorkflowStore(tmp_path / "workflow.sqlite")
    try:
        target = fault_injection._select_fault_target(
            {0: (_RunningProcess(), record)},
            progress_root,
            store,
        )
    finally:
        store.close()

    assert target is None


def test_fault_target_requires_running_checkpoint_owned_by_current_process(
    tmp_path: Path,
) -> None:
    progress_root = tmp_path / "progress"
    progress_root.mkdir()
    (progress_root / "worker-0.json").write_text(
        json.dumps(
            {
                "slot": 0,
                "pid": 999,
                "process_token": "launch-token",
                "task_id": "task-0001",
                "stage_id": "stage-1",
                "state": "running",
            }
        ),
        encoding="utf-8",
    )
    record = fault_injection._ProcessRecord(
        process_id="launcher-process",
        slot=0,
        pid=111,
        started_at=time.time(),
        process_token="launch-token",
    )
    active = {0: (_RunningProcess(), record)}
    store = SQLiteWorkflowStore(tmp_path / "workflow.sqlite")
    try:
        assert fault_injection._select_fault_target(active, progress_root, store) is None

        store.save(
            WorkflowRun(
                id="task-0001",
                workers={
                    "stage-1": WorkerResult(
                        id="stage-1",
                        provider="deterministic",
                        state=WorkerState.RUNNING,
                    )
                },
            ),
            "stage-1",
            "worker started",
        )
        store.acquire_lease("task-0001", "stage-1", "other-process", 10.0)
        assert fault_injection._select_fault_target(active, progress_root, store) is None

        other_lease = store.get_lease("task-0001", "stage-1")
        assert other_lease is not None
        store.release_lease(
            "task-0001",
            "stage-1",
            other_lease.owner_id,
            other_lease.token,
        )
        store.acquire_lease("task-0001", "stage-1", "launch-token", 10.0)

        target = fault_injection._select_fault_target(active, progress_root, store)
        assert target is not None
        assert target[3:] == ("task-0001", "stage-1")
    finally:
        store.close()


def test_fault_is_counted_only_after_process_exit_with_running_checkpoint(
    tmp_path: Path, monkeypatch
) -> None:
    store = SQLiteWorkflowStore(tmp_path / "workflow.sqlite")
    running = WorkflowRun(
        id="task-0001",
        workers={
            "stage-1": WorkerResult(
                id="stage-1",
                provider="deterministic",
                state=WorkerState.RUNNING,
            )
        },
    )
    store.save(running, "stage-1", "worker started")
    lease = store.acquire_lease("task-0001", "stage-1", "launch-token", 10.0)
    assert lease is not None
    monkeypatch.setattr(fault_injection, "_terminate_process", lambda _process: True)
    try:
        terminated, interrupted = fault_injection._terminate_fault_target(
            _RunningProcess(), store, ["task-0001"], "launch-token"
        )
        assert terminated is True
        assert interrupted == ("task-0001", "stage-1")

        completed = WorkflowRun(
            id="task-0001",
            workers={
                "stage-1": WorkerResult.completed(
                    "stage-1", "deterministic", "completed"
                )
            },
        )
        store.save(completed, "stage-1", "worker completed")
        store.release_lease(
            "task-0001", "stage-1", lease.owner_id, lease.token
        )

        terminated, interrupted = fault_injection._terminate_fault_target(
            _RunningProcess(), store, ["task-0001"], "launch-token"
        )
        assert terminated is True
        assert interrupted is None
    finally:
        store.close()


def test_fault_injection_requires_one_durable_recovery_event_per_fault(
    tmp_path: Path, monkeypatch
) -> None:
    real_sqlite_summary = fault_injection._sqlite_summary

    def without_recovery_event(store, task_ids):
        summary = real_sqlite_summary(store, task_ids)
        summary["recovery_event_count"] = 0
        return summary

    monkeypatch.setattr(fault_injection, "_sqlite_summary", without_recovery_event)
    config = FaultInjectionConfig(
        run_id="missing-recovery-event",
        artifact_root=tmp_path / "artifacts",
        worker_count=1,
        task_count=2,
        fault_count=1,
        task_duration_s=0.08,
        fault_interval_s=0.04,
        timeout_s=15.0,
    )

    receipt = run_fault_injection(config)

    assert receipt["status"] == "INCOMPLETE"
    assert receipt["exit_code"] == 2
    assert any(
        failure["category"] == "insufficient_recovery_events"
        for failure in receipt["failures"]
    )


def test_unexpected_exit_does_not_claim_stale_progress_task(tmp_path: Path) -> None:
    progress_root = tmp_path / "progress"
    progress_root.mkdir()
    (progress_root / "worker-0.json").write_text(
        json.dumps({"slot": 0, "pid": 111, "task_id": "stale-task", "state": "running"}),
        encoding="utf-8",
    )
    record = fault_injection._ProcessRecord(
        process_id="current-process",
        slot=0,
        pid=222,
        started_at=time.time(),
    )
    active = {0: (_FinishedProcess(17), record)}
    failures: list[dict[str, object]] = []

    fault_injection._reap_processes(active, {0: False}, failures, progress_root)

    assert failures[0]["category"] == "process_exit"
    assert failures[0]["task_id"] == ""


def test_progress_for_process_matches_launch_token_across_windows_launcher(
    tmp_path: Path,
) -> None:
    progress_root = tmp_path / "progress"
    progress_root.mkdir()
    (progress_root / "worker-0.json").write_text(
        json.dumps(
            {
                "slot": 0,
                "pid": 999,
                "process_token": "launch-token",
                "task_id": "task-0001",
                "state": "running",
            }
        ),
        encoding="utf-8",
    )
    record = fault_injection._ProcessRecord(
        process_id="launcher-process",
        slot=0,
        pid=111,
        started_at=time.time(),
        process_token="launch-token",
    )

    progress = fault_injection._progress_for_process(progress_root, 0, record)

    assert progress["task_id"] == "task-0001"


def test_deadline_drain_records_process_that_already_exited_nonzero(
    tmp_path: Path, monkeypatch
) -> None:
    process = _FinishedProcess(23)
    record = fault_injection._ProcessRecord(
        process_id="deadline-process",
        slot=0,
        pid=333,
        started_at=time.time(),
    )
    monkeypatch.setattr(
        fault_injection,
        "_launch_child",
        lambda *args, **kwargs: (process, record),
    )
    monotonic_ticks = iter((0.0, 0.0, 0.0, 2.0))
    monkeypatch.setattr(
        fault_injection,
        "time",
        SimpleNamespace(
            monotonic=lambda: next(monotonic_ticks),
            sleep=lambda _seconds: None,
            time=time.time,
        ),
    )
    config = FaultInjectionConfig(
        run_id="deadline-exit",
        artifact_root=tmp_path / "artifacts",
        worker_count=1,
        task_count=1,
        fault_count=0,
        task_duration_s=0.01,
        timeout_s=1.0,
    )

    receipt = run_fault_injection(config)

    process_failures = [
        item for item in receipt["failures"] if item["category"] == "process_exit"
    ]
    assert len(process_failures) == 1
    assert process_failures[0]["process_id"] == "deadline-process"
    assert process_failures[0]["exit_code"] == 23


def test_truncated_process_log_records_original_size_and_digest(tmp_path: Path) -> None:
    log_path = tmp_path / "worker.log"
    content = "start-of-log\n" + ("x" * 17_000) + "\nend-of-log"
    log_path.write_text(content, encoding="utf-8")

    captured = fault_injection._read_log(log_path)

    assert captured.startswith("[log truncated: original_chars=17024 sha256=")
    assert captured.endswith("end-of-log")
    assert "start-of-log" not in captured


def test_process_log_redacts_secret_before_tail_truncation(tmp_path: Path) -> None:
    log_path = tmp_path / "worker.log"
    secret = "sk-" + ("s" * 17_000)
    log_path.write_text(f"Bearer {secret}\nend-of-log", encoding="utf-8")

    captured = fault_injection._read_log(log_path)

    assert secret[-128:] not in captured
    assert "Bearer <redacted>" in captured


def test_progress_replace_retries_transient_windows_permission_error(
    tmp_path: Path, monkeypatch
) -> None:
    progress_path = tmp_path / "progress" / "worker-0.json"
    real_replace = fault_injection.os.replace
    attempts = 0

    def replace_with_transient_lock(source, destination):
        nonlocal attempts
        attempts += 1
        if attempts == 1:
            raise PermissionError(5, "access denied")
        return real_replace(source, destination)

    monkeypatch.setattr(fault_injection.os, "replace", replace_with_transient_lock)

    fault_injection._write_progress(
        progress_path, {"slot": 0, "pid": 123, "task_id": "task-0001", "state": "running"}
    )

    assert attempts == 2
    assert fault_injection._read_json(progress_path)["task_id"] == "task-0001"
