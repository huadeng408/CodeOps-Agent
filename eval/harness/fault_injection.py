"""Real-process fault injection for resumable workflow acceptance.

The runner deliberately executes the production ``WorkflowEngine`` in child
processes. The parent kills active children on a deterministic schedule and
restarts their assignments against the same SQLite checkpoint database. All
evidence is written under an ignored run directory and finalized with the
shared :class:`RunArtifacts` checksum contract.
"""

from __future__ import annotations

import argparse
import asyncio
import hashlib
import json
import os
import shutil
import signal
import sqlite3
import subprocess
import sys
import tempfile
import time
import uuid
from collections import Counter
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from eval.harness.artifacts import RunArtifacts
from eval.harness.redaction import redact_credential_text
from eval.harness.source_pin import source_pin
from orchestrator.workflows import (
    SQLiteWorkflowStore,
    WorkerResult,
    WorkerSpec,
    WorkflowEngine,
    WorkflowSpec,
)


@dataclass(frozen=True, slots=True)
class FaultInjectionConfig:
    run_id: str
    artifact_root: Path
    worker_count: int = 8
    task_count: int = 200
    fault_count: int = 30
    stage_count: int = 3
    task_duration_s: float = 0.08
    fault_interval_s: float = 0.04
    lease_ttl_s: float = 0.5
    timeout_s: float = 180.0
    model_pin: str = "deterministic-workflow-executor-v1"
    crash_once_task_id: str | None = None

    def validate(self) -> None:
        if not self.run_id or Path(self.run_id).name != self.run_id or self.run_id in {".", ".."}:
            raise ValueError("run_id must be one safe directory name")
        if self.worker_count <= 0 or self.task_count <= 0:
            raise ValueError("worker_count and task_count must be positive")
        if self.fault_count < 0:
            raise ValueError("fault_count must not be negative")
        if self.stage_count < 2:
            raise ValueError("stage_count must be at least two")
        if self.task_duration_s <= 0 or self.fault_interval_s <= 0 or self.lease_ttl_s <= 0 or self.timeout_s <= 0:
            raise ValueError("durations and timeout must be positive")
        if not self.model_pin.strip():
            raise ValueError("model_pin must not be empty")


@dataclass(slots=True)
class _ProcessRecord:
    process_id: str
    slot: int
    pid: int
    started_at: float
    process_token: str = ""
    injected: bool = False
    task_at_injection: str = ""
    stage_at_injection: str = ""
    ended_at: float | None = None
    exit_code: int | None = None
    log_path: Path | None = None
    log_handle: Any = None

    def to_dict(self) -> dict[str, Any]:
        return {
            "process_id": self.process_id,
            "slot": self.slot,
            "pid": self.pid,
            "started_at": self.started_at,
            "ended_at": self.ended_at,
            "exit_code": self.exit_code,
            "injected": self.injected,
            "task_at_injection": self.task_at_injection,
            "stage_at_injection": self.stage_at_injection,
        }


def run_fault_injection(config: FaultInjectionConfig) -> dict[str, Any]:
    """Run the real subprocess acceptance and return its finalized receipt."""

    config.validate()
    artifact_path = (Path(config.artifact_root).resolve() / config.run_id).resolve()
    artifact_base = Path(config.artifact_root).resolve()
    if artifact_path.exists() and any(artifact_path.iterdir()):
        raise FileExistsError(f"refusing to overwrite existing receipt directory: {artifact_path}")

    artifacts = RunArtifacts(config.run_id, artifact_base)
    runtime_root = Path(tempfile.mkdtemp(prefix=f"{config.run_id}-", dir=str(artifact_base)))
    db_path = runtime_root / "workflows.sqlite"
    progress_root = runtime_root / "progress"
    progress_root.mkdir(parents=True, exist_ok=True)
    task_ids = [f"task-{index:04d}" for index in range(1, config.task_count + 1)]
    assignments = _assign_tasks(task_ids, config.worker_count)
    stage_ids = [f"stage-{index}" for index in range(1, config.stage_count + 1)]
    task_manifest = {
        "schema_version": 2,
        "run_id": config.run_id,
        "tasks": task_ids,
        "assignments": assignments,
        "worker_count": config.worker_count,
        "workload": {
            "kind": "checkpointed_dependency_pipeline",
            "stage_count": config.stage_count,
            "stage_ids": stage_ids,
            "stage_duration_s": config.task_duration_s,
        },
    }
    task_manifest_bytes = json.dumps(task_manifest, sort_keys=True, separators=(",", ":")).encode("utf-8")
    data_pin = hashlib.sha256(task_manifest_bytes).hexdigest()
    source_pin = _source_pin()
    trace_id = uuid.uuid4().hex
    artifacts.write_manifest(
        {
            "schema_version": 1,
            "run_id": config.run_id,
            "trace_id": trace_id,
            "git_sha": source_pin["git_sha"],
            "dirty_hash": source_pin["dirty_hash"],
            "model": config.model_pin,
            "model_pin": config.model_pin,
            "source_pin": source_pin,
            "data_pin": {"task_manifest_sha256": data_pin},
            "capabilities": {
                "real_subprocesses": True,
                "sqlite_checkpoints": True,
                "multi_stage_pipeline": True,
            },
        }
    )
    artifacts.write("task-manifest.json", task_manifest)
    artifacts.write_environment()

    records: list[_ProcessRecord] = []
    fault_events: list[dict[str, Any]] = []
    failures: list[dict[str, Any]] = []
    active: dict[int, tuple[subprocess.Popen[bytes], _ProcessRecord]] = {}
    next_launch: dict[int, bool] = {slot: True for slot in assignments}
    applied_faults = 0
    deadline = time.monotonic() + config.timeout_s
    next_fault_at = time.monotonic() + config.fault_interval_s
    store = SQLiteWorkflowStore(db_path)
    try:
        while time.monotonic() < deadline:
            _reap_processes(active, next_launch, failures, progress_root)
            complete = _completed_tasks(store, task_ids)
            if len(complete) == len(task_ids) and applied_faults >= config.fault_count and not active:
                break

            for slot, task_assignment in assignments.items():
                if (
                    not next_launch.get(slot)
                    or slot in active
                    or _assignment_complete(store, task_assignment)
                ):
                    next_launch[slot] = False
                    continue
                next_launch[slot] = False
                process, record = _launch_child(
                    slot,
                    task_assignment,
                    db_path,
                    progress_root / f"worker-{slot}.json",
                    config.task_duration_s,
                    config.lease_ttl_s,
                    config.stage_count,
                    runtime_root / "logs",
                    config.crash_once_task_id,
                    runtime_root / "crash-once.marker",
                )
                active[slot] = (process, record)
                records.append(record)

            if applied_faults < config.fault_count and time.monotonic() >= next_fault_at:
                target = _select_fault_target(active, progress_root)
                if target is not None:
                    slot, process, record, current_task, current_stage = target
                    record.injected = True
                    record.task_at_injection = current_task
                    record.stage_at_injection = current_stage
                    fault_events.append(
                        {
                            "sequence": applied_faults + 1,
                            "slot": slot,
                            "pid": record.pid,
                            "process_id": record.process_id,
                            "task_id": current_task,
                            "stage_id": current_stage,
                            "signal": "SIGKILL" if os.name != "nt" else "TerminateProcess",
                            "requested_at": time.time(),
                        }
                    )
                    _terminate_process(process)
                    applied_faults += 1
                    next_launch[slot] = True
                    next_fault_at = time.monotonic() + config.fault_interval_s
                else:
                    next_fault_at = time.monotonic() + min(config.fault_interval_s, 0.01)

            time.sleep(0.01)
        else:
            failures.append({"category": "timeout", "message": "fault injection run exceeded timeout"})

        for slot, (process, record) in list(active.items()):
            terminated_by_parent = _terminate_process(process)
            record.ended_at = time.time()
            record.exit_code = process.wait(timeout=10)
            if record.log_handle is not None:
                record.log_handle.close()
            if record.exit_code != 0 and not record.injected and not terminated_by_parent:
                _record_process_exit_failure(
                    failures,
                    record,
                    record.exit_code,
                    progress_root,
                )
            active.pop(slot)
            next_launch[slot] = False

        complete = _completed_tasks(store, task_ids)
        incomplete = [task_id for task_id in task_ids if task_id not in complete]
        if incomplete:
            failures.append(
                {
                    "category": "incomplete_tasks",
                    "message": "workflow checkpoints did not reach completed",
                    "task_ids": incomplete,
                }
            )
        if applied_faults != config.fault_count:
            failures.append(
                {
                    "category": "fault_injection",
                    "message": f"applied {applied_faults} of requested {config.fault_count} process terminations",
                }
            )

        sqlite_summary = _sqlite_summary(store, task_ids)
        store.backup_to(artifacts.root / "workflow.sqlite")
        artifacts.write("fault-events.json", {"events": fault_events})
        artifacts.write("process-exits.json", {"exit_codes": [record.to_dict() for record in records]})
        artifacts.write(
            "process-logs.json",
            {
                "logs": {
                    record.process_id: _read_log(record.log_path)
                    for record in records
                    if record.log_path
                }
            },
        )
        artifacts.write("sqlite-events.json", sqlite_summary)
        artifacts.write("failures.json", {"failures": failures})
        recovery_successes = len(complete)
        recovery_rate = recovery_successes / len(task_ids)
        status = (
            "VERIFIED"
            if _is_canonical(config)
            and not failures
            and applied_faults == config.fault_count
            and recovery_successes == len(task_ids)
            else "SMOKE_VERIFIED"
            if not failures
            and applied_faults == config.fault_count
            and recovery_successes == len(task_ids)
            else "INCOMPLETE"
        )
        receipt = {
            "schema_version": 1,
            "status": status,
            "exit_code": 0 if status == "VERIFIED" else 2,
            "run_id": config.run_id,
            "trace_id": trace_id,
            "source_pin": source_pin,
            "data_pin": {"task_manifest_sha256": data_pin},
            "model_pin": config.model_pin,
            "workload": {
                "kind": "checkpointed_dependency_pipeline",
                "stage_count": config.stage_count,
                "dependency_edges_per_task": config.stage_count - 1,
            },
            "budget": {
                "worker_count": config.worker_count,
                "task_count": config.task_count,
                "fault_count": config.fault_count,
                "stage_count": config.stage_count,
                "task_duration_s": config.task_duration_s,
                "estimated_task_duration_s": config.task_duration_s * config.stage_count,
                "fault_interval_s": config.fault_interval_s,
                "lease_ttl_s": config.lease_ttl_s,
                "timeout_s": config.timeout_s,
                "canonical": _is_canonical(config),
                "canonical_target": {
                    "worker_count": 8,
                    "task_count": 200,
                    "fault_count": 30,
                    "stage_count": 3,
                },
            },
            "fault_injection": {
                "requested": config.fault_count,
                "applied": applied_faults,
                "events": len(fault_events),
                "evidence": fault_events,
            },
            "recovery": {
                "denominator": len(task_ids),
                "successes": recovery_successes,
                "failures": len(task_ids) - recovery_successes,
                "success_rate": recovery_rate,
                "incomplete_task_ids": incomplete,
                "stage_denominator": config.task_count * config.stage_count,
                "completed_stages": sqlite_summary["completed_stage_count"],
                "resume_events": sqlite_summary["recovery_event_count"],
                "resumed_workflows": sqlite_summary["resumed_workflow_count"],
                "checkpoint_complete": (
                    sqlite_summary["completed_stage_count"]
                    == config.task_count * config.stage_count
                ),
            },
            "processes": {
                "started": len(records),
                "injected_terminations": sum(1 for record in records if record.injected),
                "exit_codes": [record.to_dict() for record in records],
            },
            "sqlite": sqlite_summary,
            "failures": failures,
            "raw_evidence": {
                "storage_scope": "LOCAL_IGNORED",
                "artifact_root": f"{Path(config.artifact_root).as_posix().rstrip('/')}/{config.run_id}",
                "task_manifest_sha256": data_pin,
                "task_manifest_file_sha256": _sha256_file(artifacts.root / "task-manifest.json"),
                "fault_events_sha256": _sha256_file(artifacts.root / "fault-events.json"),
                "process_exits_sha256": _sha256_file(artifacts.root / "process-exits.json"),
                "sqlite_events_sha256": _sha256_file(artifacts.root / "sqlite-events.json"),
                "workflow_sqlite_sha256": _sha256_file(artifacts.root / "workflow.sqlite"),
            },
            "artifacts": {"checksum_file": "checksums.sha256", "sqlite_file": "workflow.sqlite"},
        }
        artifacts.write("receipt.json", receipt)
        artifacts.write_checksums()
        receipt["checksum_verification"] = verify_receipt(artifacts.root)
        artifacts.write("receipt.json", receipt)
        artifacts.write_checksums()
        receipt["checksum_verification"] = verify_receipt(artifacts.root)
        return receipt
    finally:
        _stop_active_processes(active)
        store.close()
        shutil.rmtree(runtime_root, ignore_errors=True)


def verify_receipt(receipt_root: str | Path) -> list[str]:
    root = Path(receipt_root).resolve()
    if not root.is_dir():
        return [f"missing:{root}"]
    try:
        run_id = root.name
        artifacts = RunArtifacts(run_id, root.parent)
        return artifacts.verify_checksums()
    except (OSError, ValueError) as exc:
        return [f"invalid:{exc}"]


def _sha256_file(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _assign_tasks(task_ids: list[str], worker_count: int) -> dict[int, list[str]]:
    assignments = {slot: [] for slot in range(worker_count)}
    for index, task_id in enumerate(task_ids):
        assignments[index % worker_count].append(task_id)
    return assignments


def _source_pin() -> dict[str, Any]:
    return source_pin(Path(__file__).resolve().parents[2])


def _launch_child(
    slot: int,
    task_ids: list[str],
    db_path: Path,
    progress_path: Path,
    task_duration_s: float,
    lease_ttl_s: float,
    stage_count: int,
    log_root: Path,
    crash_once_task_id: str | None,
    crash_marker: Path,
) -> tuple[subprocess.Popen[bytes], _ProcessRecord]:
    root = Path(__file__).resolve().parents[2]
    process_token = uuid.uuid4().hex
    command = [
        sys.executable,
        "-m",
        "eval.harness.fault_injection",
        "--child",
        "--slot",
        str(slot),
        "--db",
        str(db_path),
        "--tasks-json",
        json.dumps(task_ids, separators=(",", ":")),
        "--progress",
        str(progress_path),
        "--task-duration",
        str(task_duration_s),
        "--lease-ttl",
        str(lease_ttl_s),
        "--stages",
        str(stage_count),
        "--process-token",
        process_token,
    ]
    if crash_once_task_id:
        command.extend(["--crash-task", crash_once_task_id, "--crash-marker", str(crash_marker)])
    log_root.mkdir(parents=True, exist_ok=True)
    log_path = log_root / f"worker-{slot}-{time.time_ns()}.log"
    log_handle = log_path.open("w", encoding="utf-8")
    process = subprocess.Popen(command, cwd=root, stdout=log_handle, stderr=subprocess.STDOUT)
    record = _ProcessRecord(
        process_id=f"worker-{slot}-pid-{process.pid}-{uuid.uuid4().hex[:8]}",
        slot=slot,
        pid=process.pid,
        started_at=time.time(),
        process_token=process_token,
        log_path=log_path,
        log_handle=log_handle,
    )
    return process, record


def _select_fault_target(
    active: dict[int, tuple[subprocess.Popen[bytes], _ProcessRecord]], progress_root: Path
) -> tuple[int, subprocess.Popen[bytes], _ProcessRecord, str, str] | None:
    for slot, (process, record) in sorted(active.items()):
        if process.poll() is not None:
            continue
        progress = _progress_for_process(progress_root, slot, record)
        if (
            progress.get("state") == "running"
            and progress.get("task_id")
            and progress.get("stage_id")
        ):
            return (
                slot,
                process,
                record,
                str(progress["task_id"]),
                str(progress["stage_id"]),
            )
    return None


def _reap_processes(
    active: dict[int, tuple[subprocess.Popen[bytes], _ProcessRecord]],
    next_launch: dict[int, bool],
    failures: list[dict[str, Any]],
    progress_root: Path,
) -> None:
    for slot, (process, record) in list(active.items()):
        code = process.poll()
        if code is None:
            continue
        record.ended_at = time.time()
        record.exit_code = code
        if record.log_handle is not None:
            record.log_handle.close()
        if code != 0 and not record.injected:
            _record_process_exit_failure(failures, record, code, progress_root)
        active.pop(slot)
        next_launch[slot] = True


def _progress_for_process(
    progress_root: Path,
    slot: int,
    record: _ProcessRecord,
) -> dict[str, Any]:
    progress = _read_json(progress_root / f"worker-{slot}.json")
    try:
        progress_slot = int(progress.get("slot", -1))
    except (TypeError, ValueError):
        return {}
    if progress_slot != slot:
        return {}
    if record.process_token:
        if progress.get("process_token") != record.process_token:
            return {}
    else:
        try:
            progress_pid = int(progress.get("pid", 0))
        except (TypeError, ValueError):
            return {}
        if progress_pid != record.pid:
            return {}
    if progress.get("state") not in {"running", "completed"}:
        return {}
    return progress


def _record_process_exit_failure(
    failures: list[dict[str, Any]],
    record: _ProcessRecord,
    exit_code: int,
    progress_root: Path,
) -> None:
    progress = _progress_for_process(progress_root, record.slot, record)
    failures.append(
        {
            "category": "process_exit",
            "process_id": record.process_id,
            "slot": record.slot,
            "exit_code": exit_code,
            "task_id": str(progress.get("task_id", "")),
            "detail": _read_log(record.log_path),
        }
    )


def _terminate_process(process: subprocess.Popen[bytes]) -> bool:
    if process.poll() is not None:
        return False
    try:
        if os.name == "nt":
            process.kill()
        else:
            process.send_signal(signal.SIGKILL)
    except (OSError, ProcessLookupError):
        if process.poll() is not None:
            return False
        raise
    process.wait(timeout=10)
    return True


def _stop_active_processes(active: dict[int, tuple[subprocess.Popen[bytes], _ProcessRecord]]) -> None:
    for process, record in active.values():
        if process.poll() is None:
            _terminate_process(process)
        if record.log_handle is not None:
            record.log_handle.close()


def _read_log(path: Path | None) -> str:
    if path is None:
        return ""
    try:
        text = path.read_text(encoding="utf-8", errors="replace")
    except OSError:
        return ""
    safe_text = redact_credential_text(text)
    captured = safe_text[-16_000:]
    if len(text) <= 16_000:
        return captured
    digest = hashlib.sha256(text.encode("utf-8")).hexdigest()
    return f"[log truncated: original_chars={len(text)} sha256={digest}]\n{captured}"


def _is_canonical(config: FaultInjectionConfig) -> bool:
    return (
        config.worker_count == 8
        and config.task_count == 200
        and config.fault_count == 30
        and config.stage_count == 3
    )


def _completed_tasks(store: SQLiteWorkflowStore, task_ids: list[str]) -> set[str]:
    completed: set[str] = set()
    for task_id in task_ids:
        try:
            checkpoint = store.load(task_id)
        except (OSError, ValueError, sqlite3.Error):
            continue
        if checkpoint and checkpoint.workers and all(result.state.value == "completed" for result in checkpoint.workers.values()):
            completed.add(task_id)
    return completed


def _assignment_complete(store: SQLiteWorkflowStore, task_ids: list[str]) -> bool:
    return len(_completed_tasks(store, task_ids)) == len(task_ids)


def _sqlite_summary(store: SQLiteWorkflowStore, task_ids: list[str]) -> dict[str, Any]:
    state_counts: Counter[str] = Counter()
    detail_counts: Counter[str] = Counter()
    event_count = 0
    completed_stage_count = 0
    recovery_event_count = 0
    resumed_workflows: set[str] = set()
    per_workflow: dict[str, int] = {}
    for task_id in task_ids:
        checkpoint = store.load(task_id)
        if checkpoint is not None:
            completed_stage_count += sum(
                result.state.value == "completed" for result in checkpoint.workers.values()
            )
        events = store.events(task_id)
        per_workflow[task_id] = len(events)
        event_count += len(events)
        for _sequence, _worker_id, state, detail in events:
            state_counts[state] += 1
            detail_counts[detail] += 1
            if detail == "worker recovered from previous process":
                recovery_event_count += 1
                resumed_workflows.add(task_id)
    return {
        "workflow_count": len(task_ids),
        "event_count": event_count,
        "state_counts": dict(sorted(state_counts.items())),
        "detail_counts": dict(sorted(detail_counts.items())),
        "events_per_workflow": per_workflow,
        "completed_stage_count": completed_stage_count,
        "recovery_event_count": recovery_event_count,
        "resumed_workflow_count": len(resumed_workflows),
    }


def _read_json(path: Path) -> dict[str, Any]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (FileNotFoundError, OSError, json.JSONDecodeError):
        return {}
    return payload if isinstance(payload, dict) else {}


def _write_progress(path: Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(dir=path.parent, suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(payload, handle, ensure_ascii=True)
        # Windows can briefly deny replacing a file while another process has
        # just finished reading it. Retry the atomic swap without hiding a
        # persistent permission failure.
        for attempt in range(5):
            try:
                os.replace(temporary, path)
                break
            except PermissionError:
                if attempt == 4:
                    raise
                time.sleep(0.01 * (attempt + 1))
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def _claim_once_marker(path: Path) -> bool:
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        with path.open("x", encoding="utf-8") as handle:
            handle.write(str(os.getpid()))
    except FileExistsError:
        return False
    return True


async def _run_child(
    slot: int,
    db_path: Path,
    task_ids: list[str],
    progress_path: Path,
    duration: float,
    lease_ttl_s: float,
    stage_count: int,
    process_token: str = "",
    crash_task_id: str | None = None,
    crash_marker: Path | None = None,
) -> None:
    store = SQLiteWorkflowStore(db_path)
    try:
        for task_id in task_ids:
            _write_progress(
                path=progress_path,
                payload={
                    "slot": slot,
                    "pid": os.getpid(),
                    "process_token": process_token,
                    "task_id": task_id,
                    "state": "starting",
                },
            )
            if crash_task_id == task_id and crash_marker is not None and _claim_once_marker(crash_marker):
                raise RuntimeError(f"simulated unexpected child failure for {task_id}")

            async def execute(
                worker: WorkerSpec,
                _upstream: dict[str, WorkerResult],
                task_id: str = task_id,
            ) -> WorkerResult:
                _write_progress(
                    path=progress_path,
                    payload={
                        "slot": slot,
                        "pid": os.getpid(),
                        "process_token": process_token,
                        "task_id": task_id,
                        "stage_id": worker.id,
                        "state": "running",
                    },
                )
                await asyncio.sleep(duration)
                return WorkerResult.completed(
                    worker.id,
                    worker.provider,
                    f"completed:{task_id}:{worker.id}",
                )

            stages = [
                WorkerSpec(
                    id=f"stage-{index}",
                    title=f"Stage {index} for {task_id}",
                    objective="checkpoint, resume, and preserve dependency output",
                    depends_on=(f"stage-{index - 1}",) if index > 1 else (),
                )
                for index in range(1, stage_count + 1)
            ]
            spec = WorkflowSpec(
                id=task_id,
                workers=stages,
            )
            await WorkflowEngine(store, execute, max_concurrency=1, lease_ttl_seconds=lease_ttl_s).run(spec)
            _write_progress(
                path=progress_path,
                payload={
                    "slot": slot,
                    "pid": os.getpid(),
                    "process_token": process_token,
                    "task_id": task_id,
                    "stage_id": f"stage-{stage_count}",
                    "state": "completed",
                },
            )
    finally:
        store.close()


def _parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--child", action="store_true")
    parser.add_argument("--slot", type=int, default=0)
    parser.add_argument("--db", type=Path)
    parser.add_argument("--tasks-json", default="[]")
    parser.add_argument("--progress", type=Path)
    parser.add_argument("--task-duration", type=float, default=0.08)
    parser.add_argument("--lease-ttl", type=float, default=0.5)
    parser.add_argument("--stages", type=int, default=3)
    parser.add_argument("--process-token", default="")
    parser.add_argument("--crash-task", default=None)
    parser.add_argument("--crash-marker", type=Path, default=None)
    parser.add_argument("--run-id", default=None)
    parser.add_argument("--artifact-root", type=Path, default=Path(".tmp/fault-injection"))
    parser.add_argument("--workers", type=int, default=8)
    parser.add_argument("--tasks", type=int, default=200)
    parser.add_argument("--faults", type=int, default=30)
    parser.add_argument("--fault-interval", type=float, default=0.04)
    parser.add_argument("--timeout", type=float, default=180.0)
    parser.add_argument("--model-pin", default="deterministic-workflow-executor-v1")
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = _parse_args(argv)
    if args.child:
        if args.db is None or args.progress is None:
            raise SystemExit("--child requires --db and --progress")
        task_ids = json.loads(args.tasks_json)
        asyncio.run(
            _run_child(
                args.slot,
                args.db,
                [str(item) for item in task_ids],
                args.progress,
                args.task_duration,
                args.lease_ttl,
                args.stages,
                args.process_token,
                args.crash_task,
                args.crash_marker,
            )
        )
        return 0
    run_id = args.run_id or time.strftime("fault-%Y%m%d-%H%M%S")
    receipt = run_fault_injection(
        FaultInjectionConfig(
            run_id=run_id,
            artifact_root=args.artifact_root,
            worker_count=args.workers,
            task_count=args.tasks,
            fault_count=args.faults,
            stage_count=args.stages,
            task_duration_s=args.task_duration,
            fault_interval_s=args.fault_interval,
            lease_ttl_s=args.lease_ttl,
            timeout_s=args.timeout,
            model_pin=args.model_pin,
        )
    )
    print(json.dumps({"status": receipt["status"], "run_id": receipt["run_id"], "success_rate": receipt["recovery"]["success_rate"]}, ensure_ascii=True))
    return int(receipt["exit_code"])


if __name__ == "__main__":
    raise SystemExit(main())
