from __future__ import annotations

import json
from pathlib import Path

from eval.harness.fault_injection import (
    FaultInjectionConfig,
    _source_pin,
    run_fault_injection,
    verify_receipt,
)
from orchestrator.workflows import SQLiteWorkflowStore


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
    assert receipt["run_id"] == "fault-test"
    assert receipt["budget"]["worker_count"] == 2
    assert receipt["budget"]["task_count"] == 6
    assert receipt["budget"]["canonical"] is False
    assert receipt["budget"]["canonical_target"] == {"worker_count": 8, "task_count": 200, "fault_count": 30}
    assert receipt["fault_injection"]["requested"] == 2
    assert receipt["fault_injection"]["applied"] == 2
    assert receipt["recovery"]["denominator"] == 6
    assert receipt["recovery"]["successes"] == 6
    assert receipt["recovery"]["success_rate"] == 1.0
    assert receipt["sqlite"]["event_count"] > 0
    assert len(receipt["processes"]["exit_codes"]) >= 2
    assert verify_receipt(config.artifact_root / config.run_id) == []
    artifact_store = SQLiteWorkflowStore(config.artifact_root / config.run_id / "workflow.sqlite")
    try:
        artifact_event_count = sum(len(artifact_store.events(task_id)) for task_id in receipt["sqlite"]["events_per_workflow"])
        assert artifact_event_count == receipt["sqlite"]["event_count"]
        assert artifact_store.load("task-0001") is not None
    finally:
        artifact_store.close()


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
    assert any(item["category"] == "process_exit" for item in receipt["failures"])
    failure = next(item for item in receipt["failures"] if item["category"] == "process_exit")
    assert "unexpected child failure" in failure["detail"]


def test_source_pin_records_commit_and_worktree_inventory() -> None:
    pin = _source_pin()

    assert len(pin["git_sha"]) == 40
    assert len(pin["dirty_hash"]) == 64
    assert int(pin["untracked_files"]) >= 0
