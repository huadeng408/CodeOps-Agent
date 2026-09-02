"""Contract tests for the pinned Terminal-Bench official receipt boundary."""

from __future__ import annotations

import hashlib
import json
import os
import runpy
import subprocess
import sys
from contextlib import nullcontext
from importlib.metadata import version
from pathlib import Path
from types import SimpleNamespace
from typing import Any, Self

import pytest

from eval.harness.trace_capture import TraceCapture


def _write_dataset(root: Path) -> None:
    task = root / "tasks" / "fixed-task"
    task.mkdir(parents=True)
    (root / "terminalbench_2.jsonl").write_text(
        '{"name": "fixed-task", "description": "fixture"}\n', encoding="utf-8"
    )


def _write_official_run(
    root: Path,
    *,
    resolved: bool,
    metadata: dict[str, object] | None = None,
    result_metadata: dict[str, object] | None = None,
) -> None:
    root.mkdir(parents=True)
    result = {"task_id": "fixed-task", "is_resolved": resolved}
    if result_metadata:
        result.update(result_metadata)
    (root / "results.json").write_text(
        json.dumps({"results": [result]}),
        encoding="utf-8",
    )
    run_metadata = {"agent_name": "fixture-agent", "n_concurrent_trials": 1}
    inferred_dataset = root.parent / "dataset" / "tasks"
    if inferred_dataset.is_dir():
        run_metadata["dataset_path"] = str(inferred_dataset.resolve())
    if metadata:
        run_metadata.update(metadata)
    (root / "run_metadata.json").write_text(
        json.dumps(run_metadata),
        encoding="utf-8",
    )


def _write_official_lock(
    root: Path,
    *,
    model: str = "gpt-5.6-sol",
    package_version: str = "0.2.18",
    task_id: str = "fixed-task",
    max_concurrency: int = 1,
    dataset_root: Path | None = None,
    n_attempts: int = 1,
) -> None:
    resolved_dataset_root = dataset_root or root.parent / "dataset"
    (root / "tb.lock").write_text(
        json.dumps(
            {
                "harness": {"package": "terminal-bench", "version": package_version},
                "agent": {
                    "name": "fixture-agent",
                    "import_path": "fixture:Agent",
                    "model_name": None,
                    "extra_kwargs": {"model": model},
                },
                "run_config": {
                    "n_concurrent_trials": max_concurrency,
                    "n_attempts": n_attempts,
                },
                "dataset": {
                    "task_ids": [task_id],
                    "local_path": str((resolved_dataset_root / "tasks").resolve()),
                },
                "local_config": {"run_id": root.name},
            }
        ),
        encoding="utf-8",
    )


def _terminalbench_runner(
    dataset: Path,
    *,
    task_tree_sha256: str | None = None,
    upstream_pin: str | None = None,
    official_command: tuple[str, ...] = ("fixture-terminal-bench",),
) -> object:
    from eval.benchmarks.terminalbenchofficial import (
        TerminalBenchOfficialConfig,
        TerminalBenchOfficialRunner,
    )

    source_hash = hashlib.sha256((dataset / "terminalbench_2.jsonl").read_bytes()).hexdigest()
    return TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=dataset,
            dataset_sha256=source_hash,
            package_version="0.2.18",
            model="openai/gpt-5.6-sol",
            task_id="fixed-task",
            task_tree_sha256=task_tree_sha256,
            upstream_pin=upstream_pin,
            official_command=official_command,
        )
    )


def _collect_fresh_receipt(
    runner: Any, official_run_dir: Path, artifact_root: Path
) -> dict[str, Any]:
    import eval.benchmarks.terminalbenchofficial as terminal_module

    staged = official_run_dir.with_name(official_run_dir.name + ".fixture")
    official_run_dir.rename(staged)
    original_run = terminal_module.subprocess.run

    def replay_fixture(command, *, cwd, env, check):
        assert check is False
        staged.rename(official_run_dir)
        return subprocess.CompletedProcess(command, 0)

    terminal_module.subprocess.run = replay_fixture
    try:
        execution = runner.prepare_run(official_run_dir)
        execution.run(cwd=official_run_dir.parent, env={})
        return runner.collect_receipt(execution, artifact_root)
    finally:
        terminal_module.subprocess.run = original_run
        if staged.exists() and not official_run_dir.exists():
            staged.rename(official_run_dir)


def test_terminalbench_official_runner_copies_raw_results_and_marks_failure(tmp_path: Path) -> None:
    from eval.benchmarks.terminalbenchofficial import (
        TerminalBenchOfficialConfig,
        TerminalBenchOfficialRunner,
    )

    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    source_hash = hashlib.sha256((dataset / "terminalbench_2.jsonl").read_bytes()).hexdigest()
    raw_run = tmp_path / "upstream-run"
    _write_official_run(raw_run, resolved=False)
    runner = TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=dataset,
            dataset_sha256=source_hash,
            package_version="0.2.18",
            model="openai/gpt-5.6-sol",
            task_id="fixed-task",
            official_command=("fixture-terminal-bench",),
        )
    )

    receipt = _collect_fresh_receipt(runner, raw_run, tmp_path / "receipt")

    copied = tmp_path / "receipt" / "scorer" / "terminalbench-results.json"
    assert copied.read_bytes() == (raw_run / "results.json").read_bytes()
    assert receipt["status"] == "OFFICIAL_FAILURE"
    assert receipt["task_ids"] == ["fixed-task"]
    assert receipt["official_output_sha256"] == hashlib.sha256(copied.read_bytes()).hexdigest()
    assert (tmp_path / "receipt" / "checksums.sha256").is_file()


def test_terminalbench_production_pins_match_checked_in_inputs() -> None:
    from eval.benchmarks.terminalbenchofficial import (
        TERMINAL_BENCH_DATASET_SHA256,
        TERMINAL_BENCH_PACKAGE,
        TERMINAL_BENCH_PACKAGE_VERSION,
        TERMINAL_BENCH_TASK_ID,
        TERMINAL_BENCH_TASK_TREE_SHA256,
        task_tree_sha256,
    )

    dataset_root = (
        Path(__file__).resolve().parents[2]
        / "eval"
        / "benchmark_data"
        / "terminalbench"
    )

    assert TERMINAL_BENCH_TASK_ID == "break-filter-js-from-html"
    assert hashlib.sha256(
        (dataset_root / "terminalbench_2.jsonl").read_bytes()
    ).hexdigest() == TERMINAL_BENCH_DATASET_SHA256
    assert task_tree_sha256(dataset_root / "tasks") == TERMINAL_BENCH_TASK_TREE_SHA256
    assert version(TERMINAL_BENCH_PACKAGE) == TERMINAL_BENCH_PACKAGE_VERSION


def test_terminalbench_official_runner_binds_complete_task_tree_hash(tmp_path: Path) -> None:
    from eval.benchmarks.terminalbenchofficial import task_tree_sha256

    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    (dataset / "tasks" / "fixed-task" / "instruction.md").write_text(
        "fixed task input\n", encoding="utf-8"
    )
    expected_tree_hash = task_tree_sha256(dataset / "tasks")
    runner = _terminalbench_runner(dataset, task_tree_sha256=expected_tree_hash)
    raw_run = tmp_path / "upstream-run"
    _write_official_run(raw_run, resolved=False)

    receipt = _collect_fresh_receipt(runner, raw_run, tmp_path / "receipt")

    assert receipt["task_tree_sha256"] == expected_tree_hash
    assert receipt["task_tree_file_count"] == 1

    (dataset / "tasks" / "fixed-task" / "instruction.md").write_text(
        "tampered task input\n", encoding="utf-8"
    )
    with pytest.raises(ValueError, match="task tree hash does not match expected pin"):
        _collect_fresh_receipt(runner, raw_run, tmp_path / "tampered-receipt")


def test_terminalbench_official_runner_checks_optional_upstream_pin(tmp_path: Path) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset, upstream_pin="upstream-commit-123")
    raw_run = tmp_path / "upstream-run"
    _write_official_run(
        raw_run,
        resolved=False,
        metadata={"upstream_pin": "upstream-commit-123"},
    )

    receipt = _collect_fresh_receipt(runner, raw_run, tmp_path / "receipt")

    assert receipt["upstream_pin"] == "upstream-commit-123"

    mismatched_run = tmp_path / "mismatched-run"
    _write_official_run(
        mismatched_run,
        resolved=False,
        metadata={"upstream_pin": "another-upstream"},
    )
    with pytest.raises(ValueError, match="upstream pin does not match"):
        _collect_fresh_receipt(runner, mismatched_run, tmp_path / "mismatched-receipt")


@pytest.mark.parametrize(
    ("metadata", "message"),
    [
        ({"model_name": "openai/other-model"}, "model does not match"),
        ({"package_version": "0.2.17"}, "package version does not match"),
        ({"task_ids": ["other-task"]}, "task ids do not match"),
    ],
)
def test_terminalbench_official_runner_rejects_identity_mismatch_in_run_metadata(
    tmp_path: Path,
    metadata: dict[str, object],
    message: str,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "upstream-run"
    _write_official_run(raw_run, resolved=False, metadata=metadata)

    with pytest.raises(ValueError, match=message):
        _collect_fresh_receipt(runner, raw_run, tmp_path / "receipt")


def test_terminalbench_official_runner_rejects_failed_process_even_if_trial_resolved(
    tmp_path: Path,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "upstream-run"
    _write_official_run(
        raw_run,
        resolved=True,
        metadata={"process_status": "failed", "exit_code": 17},
    )

    with pytest.raises(ValueError, match="process status"):
        _collect_fresh_receipt(runner, raw_run, tmp_path / "receipt")


def test_terminalbench_official_runner_validates_real_v0218_lock_shape(tmp_path: Path) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "official-run"
    _write_official_run(
        raw_run,
        resolved=False,
        metadata={
            "run_id": raw_run.name,
            "task_ids": ["fixed-task"],
            "start_time": "2026-09-02T01:00:00+00:00",
            "end_time": "2026-09-02T01:01:00+00:00",
        },
    )
    _write_official_lock(raw_run)

    receipt = _collect_fresh_receipt(runner, raw_run, tmp_path / "receipt")

    assert receipt["status"] == "OFFICIAL_FAILURE"
    assert receipt["process_status"] == "completed"


@pytest.mark.parametrize(
    ("lock_kwargs", "message"),
    [
        ({"model": "other-model"}, "model does not match"),
        ({"package_version": "0.2.17"}, "package version does not match"),
        ({"task_id": "other-task"}, "task ids do not match"),
        ({"max_concurrency": 2}, "concurrency does not match"),
    ],
)
def test_terminalbench_official_runner_rejects_lock_identity_mismatch(
    tmp_path: Path,
    lock_kwargs: dict[str, object],
    message: str,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "official-run"
    _write_official_run(
        raw_run,
        resolved=False,
        metadata={
            "run_id": raw_run.name,
            "task_ids": ["fixed-task"],
            "start_time": "2026-09-02T01:00:00+00:00",
            "end_time": "2026-09-02T01:01:00+00:00",
        },
    )
    _write_official_lock(raw_run, **lock_kwargs)

    with pytest.raises(ValueError, match=message):
        _collect_fresh_receipt(runner, raw_run, tmp_path / "receipt")


def test_terminalbench_official_runner_rejects_incomplete_official_metadata(tmp_path: Path) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "official-run"
    _write_official_run(
        raw_run,
        resolved=False,
        metadata={
            "run_id": raw_run.name,
            "task_ids": ["fixed-task"],
            "start_time": "2026-09-02T01:00:00+00:00",
            "end_time": None,
        },
    )
    _write_official_lock(raw_run)

    with pytest.raises(ValueError, match="process status"):
        _collect_fresh_receipt(runner, raw_run, tmp_path / "receipt")


def test_terminalbench_official_runner_validates_parent_dataset_file_for_tasks_root(
    tmp_path: Path,
) -> None:
    from eval.benchmarks.terminalbenchofficial import (
        TerminalBenchOfficialConfig,
        TerminalBenchOfficialRunner,
    )

    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    dataset_file = dataset / "terminalbench_2.jsonl"
    runner = TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=dataset / "tasks",
            dataset_sha256=hashlib.sha256(dataset_file.read_bytes()).hexdigest(),
            package_version="0.2.18",
            model="openai/gpt-5.6-sol",
            task_id="fixed-task",
            official_command=("fixture-terminal-bench",),
        )
    )
    raw_run = tmp_path / "upstream-run"
    _write_official_run(raw_run, resolved=False)
    dataset_file.write_text('{"name": "tampered"}\n', encoding="utf-8")

    with pytest.raises(ValueError, match="dataset hash does not match"):
        _collect_fresh_receipt(runner, raw_run, tmp_path / "receipt")


def test_terminalbench_official_runner_rejects_missing_dataset_bytes(
    tmp_path: Path,
) -> None:
    from eval.benchmarks.terminalbenchofficial import (
        TerminalBenchOfficialConfig,
        TerminalBenchOfficialRunner,
    )

    dataset = tmp_path / "dataset"
    (dataset / "tasks" / "fixed-task").mkdir(parents=True)
    runner = TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=dataset,
            dataset_sha256="a" * 64,
            package_version="0.2.18",
            model="openai/gpt-5.6-sol",
            task_id="fixed-task",
        )
    )

    with pytest.raises(ValueError, match="dataset file is missing"):
        runner.validate_input_pins()


def test_terminalbench_receipt_collection_requires_fresh_process_proof(
    tmp_path: Path,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "upstream-run"
    _write_official_run(raw_run, resolved=True)

    with pytest.raises(TypeError, match="process_returncode"):
        runner.collect_receipt(raw_run, tmp_path / "receipt")


def test_terminalbench_official_runner_rejects_caller_claimed_process_evidence(
    tmp_path: Path,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "caller-claimed-run"
    _write_official_run(raw_run, resolved=True)

    with pytest.raises(ValueError, match="runner-issued execution"):
        runner.collect_receipt(
            raw_run,
            tmp_path / "receipt",
            process_returncode=0,
            result_existed_before=False,
        )
    assert not (tmp_path / "receipt" / "receipt.json").exists()


def test_terminalbench_official_runner_collects_output_only_after_its_execution_session(
    tmp_path: Path,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    raw_run = tmp_path / "runner-observed-run"
    command = [
        sys.executable,
        "-c",
        (
            "from pathlib import Path; import json; "
            f"root=Path({str(raw_run)!r}); root.mkdir(parents=True); "
            "(root / 'results.json').write_text(json.dumps({'results': "
            "[{'task_id': 'fixed-task', 'is_resolved': True}]})); "
            "(root / 'run_metadata.json').write_text(json.dumps("
            "{'agent_name': 'fixture-agent', 'n_concurrent_trials': 1}))"
        ),
    ]

    runner = _terminalbench_runner(dataset, official_command=tuple(command))
    execution = runner.prepare_run(raw_run)
    completed = execution.run(cwd=tmp_path)
    receipt = runner.collect_receipt(execution, tmp_path / "receipt")

    assert completed.returncode == 0
    assert receipt["status"] == "OFFICIAL_PASS"


def test_terminalbench_official_runner_records_nonzero_process_as_infra_failure(
    tmp_path: Path,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    raw_run = tmp_path / "process-failure"
    command = (sys.executable, "-c", "raise SystemExit(17)")
    runner = _terminalbench_runner(dataset, official_command=command)
    execution = runner.prepare_run(raw_run)

    completed = execution.run(cwd=tmp_path)
    receipt = runner.collect_receipt(execution, tmp_path / "receipt")

    assert completed.returncode == 17
    assert receipt["status"] == "OFFICIAL_INFRA_FAILURE"
    assert receipt["failure_mode"] == "OFFICIAL_PROCESS_EXIT_NONZERO"
    assert receipt["process_status"] == "failed"
    assert receipt["process_exit_code"] == 17
    assert receipt["expected_trial_count"] == 1
    assert receipt["observed_trial_count"] == 0
    assert receipt["missing_required_outputs"] == [
        "process-failure/results.json",
        "process-failure/run_metadata.json",
    ]
    assert receipt["official_output"] is None
    assert json.loads(
        (tmp_path / "receipt" / "receipt.json").read_text(encoding="utf-8")
    ) == receipt


def test_terminalbench_official_runner_rejects_an_unpinned_process_command(
    tmp_path: Path,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    from eval.benchmarks.terminalbenchofficial import (
        TerminalBenchOfficialConfig,
        TerminalBenchOfficialRunner,
    )

    runner = TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=dataset,
            dataset_sha256=hashlib.sha256(
                (dataset / "terminalbench_2.jsonl").read_bytes()
            ).hexdigest(),
            package_version="0.2.18",
            model="openai/gpt-5.6-sol",
            task_id="fixed-task",
        )
    )

    with pytest.raises(ValueError, match="official command pin is required"):
        runner.prepare_run(tmp_path / "unbound-run")


def test_terminalbench_official_runner_binds_output_dataset_provenance_and_attempts(
    tmp_path: Path,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "official-run"
    _write_official_run(
        raw_run,
        resolved=False,
        metadata={
            "run_id": raw_run.name,
            "uuid": "fixture-uuid",
            "dataset_path": str((dataset / "tasks").resolve()),
            "task_ids": ["fixed-task"],
            "n_tasks": 1,
            "n_attempts": 1,
            "start_time": "2026-09-02T01:00:00+00:00",
            "end_time": "2026-09-02T01:01:00+00:00",
        },
    )
    _write_official_lock(raw_run, dataset_root=dataset, n_attempts=1)

    receipt = _collect_fresh_receipt(runner, raw_run, tmp_path / "receipt")

    assert receipt["dataset_path"] == (dataset / "tasks").resolve().as_posix()
    assert receipt["n_attempts"] == 1

    mismatched = tmp_path / "mismatched-run"
    _write_official_run(
        mismatched,
        resolved=False,
        metadata={
            "run_id": mismatched.name,
            "uuid": "fixture-uuid-2",
            "dataset_path": str((tmp_path / "other-dataset").resolve()),
            "task_ids": ["fixed-task"],
            "n_tasks": 1,
            "n_attempts": 1,
            "start_time": "2026-09-02T01:00:00+00:00",
            "end_time": "2026-09-02T01:01:00+00:00",
        },
    )
    _write_official_lock(mismatched, dataset_root=dataset, n_attempts=1)
    with pytest.raises(ValueError, match="dataset path does not match"):
        _collect_fresh_receipt(runner, mismatched, tmp_path / "mismatched-receipt")


def test_terminalbench_receipt_uses_execution_snapshot_if_output_changes_after_evidence(
    tmp_path: Path,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "snapshot-race-run"
    command = [
        sys.executable,
        "-c",
        (
            "from pathlib import Path; import json; "
            f"root=Path({str(raw_run)!r}); root.mkdir(parents=True); "
            "(root / 'results.json').write_text(json.dumps({'results': "
            "[{'task_id': 'fixed-task', 'is_resolved': True}]})); "
            "(root / 'run_metadata.json').write_text(json.dumps("
            "{'agent_name': 'fixture-agent', 'n_concurrent_trials': 1}))"
        ),
    ]
    runner = _terminalbench_runner(dataset, official_command=tuple(command))
    execution = runner.prepare_run(raw_run)
    execution.run(cwd=tmp_path)
    (raw_run / "results.json").write_text(
        json.dumps({"results": [{"task_id": "fixed-task", "is_resolved": False}]}),
        encoding="utf-8",
    )
    receipt = runner.collect_receipt(execution, tmp_path / "receipt")
    copied = json.loads(
        (
            tmp_path
            / "receipt"
            / "scorer"
            / "terminalbench-results.json"
        ).read_text(encoding="utf-8")
    )

    assert receipt["status"] == "OFFICIAL_PASS"
    assert copied["results"][0]["is_resolved"] is True


def test_terminalbench_receipt_uses_execution_snapshot_if_metadata_changes_after_evidence(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    import eval.benchmarks.terminalbenchofficial as terminal_module

    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "metadata-snapshot-run"

    def fake_run(command, *, cwd, env, check):
        _write_official_run(raw_run, resolved=True)
        return subprocess.CompletedProcess(command, 0)

    monkeypatch.setattr(terminal_module.subprocess, "run", fake_run)
    execution = runner.prepare_run(raw_run)
    execution.run(cwd=tmp_path, env={})
    (raw_run / "run_metadata.json").write_text(
        json.dumps({"agent_name": "fixture-agent", "n_concurrent_trials": 2}),
        encoding="utf-8",
    )
    receipt = runner.collect_receipt(execution, tmp_path / "receipt")
    copied = json.loads(
        (
            tmp_path
            / "receipt"
            / "scorer"
            / "terminalbench-run-metadata.json"
        ).read_text(encoding="utf-8")
    )

    assert receipt["status"] == "OFFICIAL_PASS"
    assert copied["n_concurrent_trials"] == 1


def test_terminalbench_receipt_uses_execution_snapshot_if_lock_changes_after_evidence(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    import eval.benchmarks.terminalbenchofficial as terminal_module

    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "lock-snapshot-run"

    def fake_run(command, *, cwd, env, check):
        _write_official_run(
            raw_run,
            resolved=True,
            metadata={
                "run_id": raw_run.name,
                "task_ids": ["fixed-task"],
                "start_time": "2026-09-02T01:00:00+00:00",
                "end_time": "2026-09-02T01:01:00+00:00",
            },
        )
        _write_official_lock(raw_run)
        return subprocess.CompletedProcess(command, 0)

    monkeypatch.setattr(terminal_module.subprocess, "run", fake_run)
    execution = runner.prepare_run(raw_run)
    execution.run(cwd=tmp_path, env={})
    expected_lock_sha256 = hashlib.sha256((raw_run / "tb.lock").read_bytes()).hexdigest()
    _write_official_lock(raw_run, model="other-model")

    receipt = runner.collect_receipt(execution, tmp_path / "receipt")

    assert receipt["status"] == "OFFICIAL_PASS"
    assert receipt["official_lock_file"] == "tb.lock"
    assert receipt["official_lock_sha256"] == expected_lock_sha256


def test_terminalbench_receipt_uses_execution_snapshot_for_process_sidecars(
    tmp_path: Path,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "sidecar-snapshot-run"

    def fake_run(command, *, cwd, env, check):
        _write_official_run(raw_run, resolved=True)
        (raw_run / "process-status.json").write_text(
            json.dumps({"status": "completed", "exit_code": 0}),
            encoding="utf-8",
        )
        return subprocess.CompletedProcess(command, 0)

    import eval.benchmarks.terminalbenchofficial as terminal_module

    original_run = terminal_module.subprocess.run
    terminal_module.subprocess.run = fake_run
    try:
        execution = runner.prepare_run(raw_run)
        execution.run(cwd=tmp_path, env={})
    finally:
        terminal_module.subprocess.run = original_run

    (raw_run / "process-status.json").write_text(
        json.dumps({"status": "failed", "exit_code": 17}),
        encoding="utf-8",
    )
    receipt = runner.collect_receipt(execution, tmp_path / "receipt")

    assert receipt["status"] == "OFFICIAL_PASS"
    assert receipt["process_status"] == "completed"


def test_terminalbench_official_runner_rejects_explicit_nonzero_process_returncode(
    tmp_path: Path,
) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "upstream-run"
    _write_official_run(raw_run, resolved=True)

    with pytest.raises(RuntimeError, match="non-zero exit code"):
        runner.collect_receipt(
            raw_run,
            tmp_path / "receipt",
            process_returncode=9,
            result_existed_before=False,
        )


def test_terminalbench_official_runner_rejects_stale_preexisting_results(tmp_path: Path) -> None:
    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    runner = _terminalbench_runner(dataset)
    raw_run = tmp_path / "upstream-run"
    _write_official_run(raw_run, resolved=True)

    with pytest.raises(RuntimeError, match="already existed"):
        runner.collect_receipt(
            raw_run,
            tmp_path / "receipt",
            process_returncode=0,
            result_existed_before=True,
        )

    assert not (tmp_path / "receipt" / "receipt.json").exists()


def test_terminalbench_official_runner_rejects_invalid_model_and_concurrency(tmp_path: Path) -> None:
    from eval.benchmarks.terminalbenchofficial import (
        TerminalBenchOfficialConfig,
        TerminalBenchOfficialRunner,
    )

    runner = TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=tmp_path,
            dataset_sha256="a" * 64,
            package_version="0.2.18",
            model="gpt-5.6-sol",
            task_id="fixed-task",
            max_concurrency=11,
        )
    )

    assert runner.validate() == [
        "model must include a LiteLLM provider prefix",
        "max_concurrency must be between 1 and 10",
    ]


def test_terminalbench_agent_uses_responses_wire_api_without_chat_completion(monkeypatch) -> None:
    from eval.swebench_work.deepseek_tb_agent import DeepSeekTBAgent

    calls: list[tuple[str, dict[str, object]]] = []

    class FakeResponses:
        def create(self, **kwargs):
            calls.append(("responses", kwargs))
            return type(
                "Response",
                (),
                {
                    "output_text": "```bash\necho solved > /app/out.html\n```",
                    "usage": type("Usage", (), {"input_tokens": 7, "output_tokens": 9})(),
                },
            )()

    class FakeChatCompletions:
        def create(self, **kwargs):  # pragma: no cover - must never be selected
            raise AssertionError("chat completions must not be used for responses wire API")

    class FakeClient:
        responses = FakeResponses()
        chat = type("Chat", (), {"completions": FakeChatCompletions()})()

    monkeypatch.setattr("openai.OpenAI", lambda **kwargs: FakeClient())
    agent = DeepSeekTBAgent(model="gpt-5.6-sol", wire_api="responses", api_key="x", base_url="https://example.test/v1")

    output, tokens_in, tokens_out = agent._request_commands(
        "do the task", "return only a bash command block"
    )

    assert output.startswith("```bash")
    assert (tokens_in, tokens_out) == (7, 9)
    assert calls == [
        (
            "responses",
            {
                "model": "gpt-5.6-sol",
                "input": [
                    {"role": "developer", "content": "return only a bash command block"},
                    {"role": "user", "content": "Task:\n\ndo the task"},
                ],
                "max_output_tokens": 4096,
            },
        )
    ]


def test_terminalbench_agent_multiturn_mode_replays_terminal_feedback_and_writes_transcript(
    monkeypatch, tmp_path: Path
) -> None:
    from eval.swebench_work.deepseek_tb_agent import DeepSeekTBAgent

    responses = iter([
        ("```bash\nprintf 'first'\n```", 10, 11),
        ("```bash\nprintf 'second'\n```", 12, 13),
    ])
    prompts: list[str] = []

    class FakeSession:
        def __init__(self) -> None:
            self.commands: list[object] = []
            self.outputs = iter(["Current Terminal Screen:\nfirst\n", "New Terminal Output:\nsecond\n"])

        def send_keys(self, keys, **kwargs) -> None:
            self.commands.append(keys)

        def get_incremental_output(self) -> str:
            return next(self.outputs)

    agent = DeepSeekTBAgent(
        model="gpt-5.6-sol",
        wire_api="responses",
        max_turns=2,
    )

    def fake_request(instruction: str, system_prompt: str = "") -> tuple[str, int, int]:
        prompts.append(instruction)
        return next(responses)

    monkeypatch.setattr(agent, "_request_commands", fake_request)
    session = FakeSession()

    result = agent.perform_task("solve the task", session, logging_dir=tmp_path)

    assert result.total_input_tokens == 22
    assert result.total_output_tokens == 24
    assert len(prompts) == 2
    assert "Terminal feedback" in prompts[1]
    assert "first" in prompts[1]
    assert session.commands == [
        ["printf 'first'", "Enter"],
        ["printf 'second'", "Enter"],
    ]
    transcript = json.loads((tmp_path / "agent-transcript.json").read_text(encoding="utf-8"))
    assert transcript["turns"] == [
        {"turn": 1, "commands": ["printf 'first'"], "terminal_output": "Current Terminal Screen:\nfirst\n"},
        {"turn": 2, "commands": ["printf 'second'"], "terminal_output": "New Terminal Output:\nsecond\n"},
    ]


def test_terminalbench_agent_multiturn_mode_rejects_protected_path_commands(monkeypatch) -> None:
    from terminal_bench.agents.failure_mode import FailureMode

    from eval.swebench_work.deepseek_tb_agent import DeepSeekTBAgent

    agent = DeepSeekTBAgent(max_turns=1)
    monkeypatch.setattr(
        agent,
        "_request_commands",
        lambda instruction, system_prompt="": ("```bash\ncat /tests/test_outputs.py\n```", 3, 4),
    )

    result = agent.perform_task("solve the task", object())

    assert result.failure_mode is FailureMode.FATAL_LLM_PARSE_ERROR


def test_terminalbench_agent_waits_for_each_command_before_collecting_feedback(monkeypatch) -> None:
    from eval.swebench_work.deepseek_tb_agent import DeepSeekTBAgent

    class FakeSession:
        def __init__(self) -> None:
            self.calls: list[tuple[object, dict[str, object]]] = []

        def send_keys(self, keys, **kwargs) -> None:
            self.calls.append((keys, kwargs))

        def get_incremental_output(self) -> str:
            return "command completed"

    agent = DeepSeekTBAgent(max_turns=1)
    monkeypatch.setattr(
        agent,
        "_request_commands",
        lambda instruction, system_prompt="": ("```bash\nprintf ready\n```", 1, 1),
    )
    session = FakeSession()

    agent.perform_task("solve", session)

    assert session.calls == [
        (["printf ready", "Enter"], {"block": True, "max_timeout_sec": 120})
    ]


def test_terminalbench_agent_emits_receipt_bound_agent_and_chat_spans(monkeypatch) -> None:
    from eval.swebench_work.deepseek_tb_agent import DeepSeekTBAgent

    capture = TraceCapture()
    assert capture.install()
    monkeypatch.setenv("TERMINALBENCH_RUN_ID", "terminalbench-trace-001")
    monkeypatch.setenv("TERMINALBENCH_INSTANCE_ID", "fixed-task")

    responses = iter(["```bash\nprintf first\n```", "```bash\nprintf second\n```"])

    class FakeSession:
        def send_keys(self, _keys, **_kwargs) -> None:
            return None

        def get_incremental_output(self) -> str:
            return "command completed"

    class FakeResponses:
        def create(self, **_kwargs):
            return type(
                "Response",
                (),
                {
                    "output_text": next(responses),
                    "usage": type("Usage", (), {"input_tokens": 5, "output_tokens": 7})(),
                },
            )()

    class FakeClient:
        responses = FakeResponses()

    agent = DeepSeekTBAgent(model="gpt-5.6-sol", wire_api="responses", max_turns=2)
    monkeypatch.setattr("openai.OpenAI", lambda **_kwargs: FakeClient())

    agent.perform_task("solve", FakeSession())

    spans = [
        span
        for span in capture.spans()
        if span.attributes.get("eval.run_id") == "terminalbench-trace-001"
    ]
    assert [span.name for span in spans] == ["chat", "chat", "invoke_agent"]
    agent_span = spans[-1]
    assert all(span.parent_span_id == agent_span.span_id for span in spans[:2])
    assert all(span.attributes["eval.instance_id"] == "fixed-task" for span in spans)


def test_terminalbench_direct_provider_call_stays_untraced_without_receipt_identity(monkeypatch) -> None:
    from eval.swebench_work.deepseek_tb_agent import DeepSeekTBAgent

    capture = TraceCapture()
    assert capture.install()
    monkeypatch.delenv("TERMINALBENCH_RUN_ID", raising=False)
    monkeypatch.delenv("TERMINALBENCH_INSTANCE_ID", raising=False)

    class FakeResponses:
        def create(self, **_kwargs):
            return type(
                "Response",
                (),
                {"output_text": "```bash\nprintf ready\n```", "usage": type("Usage", (), {})()},
            )()

    class FakeClient:
        responses = FakeResponses()

    monkeypatch.setattr("openai.OpenAI", lambda **_kwargs: FakeClient())
    output, _, _ = DeepSeekTBAgent(model="gpt-5.6-sol", wire_api="responses")._request_commands("solve")

    assert output.startswith("```bash")
    assert [span for span in capture.spans() if span.name == "chat"] == []


def test_terminalbench_verifier_proxy_is_disabled_by_default_and_records_manifest(
    tmp_path: Path, monkeypatch
) -> None:
    from terminal_bench.terminal.docker_compose_manager import DockerComposeManager

    from eval.swebench_work.terminalbench_proxy import scoped_verifier_proxy

    monkeypatch.delenv("TERMINALBENCH_VERIFIER_PROXY", raising=False)
    original = DockerComposeManager.get_docker_compose_command
    monkeypatch.setattr(
        DockerComposeManager,
        "get_docker_compose_command",
        lambda self, command: ["docker", "compose", "-f", "base.yaml", *command],
    )

    with scoped_verifier_proxy(None, tmp_path):
        command = DockerComposeManager.get_docker_compose_command(object(), ["up", "-d"])

    assert command == ["docker", "compose", "-f", "base.yaml", "up", "-d"]
    assert os.environ.get("TERMINALBENCH_VERIFIER_PROXY") is None
    assert json.loads((tmp_path / "verifier-proxy-manifest.json").read_text(encoding="utf-8")) == {
        "enabled": False,
        "proxy": None,
        "no_proxy": None,
        "scope": "official Terminal-Bench verifier container only",
    }
    monkeypatch.setattr(DockerComposeManager, "get_docker_compose_command", original)


def test_terminalbench_verifier_proxy_uses_ephemeral_compose_overlay_and_restores_environment(
    tmp_path: Path, monkeypatch
) -> None:
    from terminal_bench.terminal.docker_compose_manager import DockerComposeManager

    from eval.swebench_work.terminalbench_proxy import scoped_verifier_proxy

    monkeypatch.delenv("TERMINALBENCH_VERIFIER_PROXY", raising=False)
    monkeypatch.delenv("TERMINALBENCH_VERIFIER_NO_PROXY", raising=False)
    original = DockerComposeManager.get_docker_compose_command
    monkeypatch.setattr(
        DockerComposeManager,
        "get_docker_compose_command",
        lambda self, command: ["docker", "compose", "-f", "base.yaml", *command],
    )

    with scoped_verifier_proxy("http://host.docker.internal:7890", tmp_path):
        command = DockerComposeManager.get_docker_compose_command(object(), ["up", "-d"])
        assert command[:6] == [
            "docker",
            "compose",
            "-f",
            "base.yaml",
            "-f",
            str(tmp_path / "terminalbench-verifier-proxy.compose.yaml"),
        ]
        assert os.environ["TERMINALBENCH_VERIFIER_PROXY"] == "http://host.docker.internal:7890"
        assert os.environ["TERMINALBENCH_VERIFIER_NO_PROXY"] == "localhost,127.0.0.1,::1"

    assert os.environ.get("TERMINALBENCH_VERIFIER_PROXY") is None
    assert os.environ.get("TERMINALBENCH_VERIFIER_NO_PROXY") is None
    overlay = (tmp_path / "terminalbench-verifier-proxy.compose.yaml").read_text(encoding="utf-8")
    assert "HTTP_PROXY: ${TERMINALBENCH_VERIFIER_PROXY}" in overlay
    assert "NO_PROXY: ${TERMINALBENCH_VERIFIER_NO_PROXY}" in overlay
    assert json.loads((tmp_path / "verifier-proxy-manifest.json").read_text(encoding="utf-8"))["enabled"] is True
    monkeypatch.setattr(DockerComposeManager, "get_docker_compose_command", original)


def test_terminalbench_verifier_proxy_rejects_credentials(tmp_path: Path) -> None:
    from eval.swebench_work.terminalbench_proxy import scoped_verifier_proxy

    with (
        pytest.raises(ValueError, match="must not contain credentials"),
        scoped_verifier_proxy("http://username:password@proxy.example:7890", tmp_path),
    ):
        pass


def test_terminalbench_receipt_uses_a_short_internal_harness_run_id() -> None:
    from eval.swebench_work.terminalbench_proxy import internal_harness_run_id

    receipt_id = "current-head-20260814-081501-proxy-debug"

    run_id = internal_harness_run_id(receipt_id)

    assert run_id == "tb-" + hashlib.sha256(receipt_id.encode("utf-8")).hexdigest()[:12]
    assert len(run_id) == 15
    assert internal_harness_run_id(receipt_id) == run_id


def test_terminalbench_receipt_runner_maps_the_short_harness_output_to_a_receipt(
    tmp_path: Path,
) -> None:
    from eval.benchmarks.terminalbenchofficial import (
        TerminalBenchOfficialConfig,
        TerminalBenchOfficialRunner,
    )
    from eval.swebench_work.terminalbench_proxy import internal_harness_run_id

    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    receipt_id = "current-head-20260814-082200-proxy"
    internal_id = internal_harness_run_id(receipt_id)
    upstream = tmp_path / "upstream" / internal_id
    _write_official_run(upstream, resolved=False)
    runner = TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=dataset,
            dataset_sha256=hashlib.sha256((dataset / "terminalbench_2.jsonl").read_bytes()).hexdigest(),
            package_version="0.2.18",
            model="openai/gpt-5.6-sol",
            task_id="fixed-task",
            official_command=("fixture-terminal-bench",),
        )
    )

    receipt = _collect_fresh_receipt(runner, upstream, tmp_path / "receipt")

    assert receipt["status"] == "OFFICIAL_FAILURE"
    assert (tmp_path / "receipt" / "scorer" / "terminalbench-results.json").is_file()


def test_terminalbench_receipt_script_wires_trace_before_final_checksums() -> None:
    script = (
        Path(__file__).resolve().parents[2]
        / "scripts"
        / "run-terminalbench-official-receipt.ps1"
    ).read_text(encoding="utf-8")

    assert "from eval.harness.trace_capture import TraceCapture" in script
    assert "from eval.harness.official_receipt_trace import" in script
    assert "capture.install(" in script
    assert "with OfficialReceiptTrace(run_id, TERMINAL_BENCH_TASK_ID)" in script
    assert "write_official_receipt_trace(" in script
    assert "refresh_receipt_checksums(" in script


def test_terminalbench_receipt_driver_fails_closed_for_a_nonpassing_trace_verdict() -> None:
    script = (
        Path(__file__).resolve().parents[2]
        / "scripts"
        / "run-terminalbench-official-receipt.ps1"
    ).read_text(encoding="utf-8")

    driver = script.split("@'\n", maxsplit=1)[1].split(
        "\n'@ | Set-Content -LiteralPath $driverPath", maxsplit=1
    )[0]
    assert 'if trace_report["verdict"] != "PASS":' in driver
    assert "raise SystemExit(1)" in driver


def test_terminalbench_receipt_script_configures_and_reads_back_phoenix() -> None:
    script = (
        Path(__file__).resolve().parents[2]
        / "scripts"
        / "run-terminalbench-official-receipt.ps1"
    ).read_text(encoding="utf-8")

    assert '[string]$PhoenixUrl = "http://127.0.0.1:6006"' in script
    assert 'TERMINALBENCH_PHOENIX_OTLP_ENDPOINT' in script
    assert 'TERMINALBENCH_PHOENIX_START_TIME' in script
    assert 'force_flush' in script
    assert 'phoenix_url=os.environ.get("TERMINALBENCH_PHOENIX_URL", "")' in script
    assert 'phoenix_start_time=os.environ.get("TERMINALBENCH_PHOENIX_START_TIME", "")' in script


def test_terminalbench_receipt_script_requires_environment_credential() -> None:
    script = (
        Path(__file__).resolve().parents[2]
        / "scripts"
        / "run-terminalbench-official-receipt.ps1"
    ).read_text(encoding="utf-8")

    assert '[string]::IsNullOrWhiteSpace($env:LOCAL_LLM_API_KEY)' in script
    assert '$apiKey = $env:LOCAL_LLM_API_KEY' in script
    assert "Get-Content -LiteralPath" not in script


def test_terminalbench_receipt_script_wires_and_clears_the_nonsecret_instance_id() -> None:
    script = (
        Path(__file__).resolve().parents[2]
        / "scripts"
        / "run-terminalbench-official-receipt.ps1"
    ).read_text(encoding="utf-8")

    assert '$env:TERMINALBENCH_INSTANCE_ID = "break-filter-js-from-html"' in script
    assert "Remove-Item Env:TERMINALBENCH_INSTANCE_ID" in script


def test_terminalbench_receipt_script_finalizes_checksums_after_driver_exit() -> None:
    script = (
        Path(__file__).resolve().parents[2]
        / "scripts"
        / "run-terminalbench-official-receipt.ps1"
    ).read_text(encoding="utf-8")

    finalizer = script.split("@'\nparam(\n    [string]$DriverPath", maxsplit=1)[1]
    redacted_invocation = "Invoke-RedactedNativeCommand"
    driver_argument = "-ArgumentList @($DriverPath)"
    assert redacted_invocation in finalizer
    assert driver_argument in finalizer
    assert "C:\\Python312\\python.exe" not in finalizer
    assert "refresh_receipt_checksums" in finalizer
    assert finalizer.index(driver_argument) < finalizer.index(
        "refresh_receipt_checksums"
    )


def test_terminalbench_receipt_script_uses_runner_owned_execution_session() -> None:
    script = (
        Path(__file__).resolve().parents[2]
        / "scripts"
        / "run-terminalbench-official-receipt.ps1"
    ).read_text(encoding="utf-8")

    session_prepare = "execution = receipt_runner.prepare_run(official_run_dir)"
    process_run = "completed = execution.run("
    receipt_collection = "receipt_runner.collect_receipt("
    assert session_prepare in script
    assert process_run in script
    assert "process_returncode=completed.returncode" not in script
    assert "result_existed_before=result_existed_before" not in script
    assert "raise SystemExit(completed.returncode)" in script
    assert script.index(session_prepare) < script.index(process_run) < script.index(receipt_collection)


def test_terminalbench_receipt_driver_rejects_dataset_changed_during_child_run(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """The pin must describe the JSONL bytes that existed before execution."""
    import eval.benchmarks.terminalbench as terminalbench_module
    import eval.benchmarks.terminalbenchofficial as terminalbenchofficial_module
    import eval.harness.official_receipt_trace as receipt_trace_module
    import eval.harness.trace_capture as trace_capture_module
    import eval.swebench_work.terminalbench_proxy as proxy_module

    script = (
        Path(__file__).resolve().parents[2]
        / "scripts"
        / "run-terminalbench-official-receipt.ps1"
    ).read_text(encoding="utf-8")
    driver_source = script.split("@'\n", maxsplit=1)[1].split(
        "\n'@ | Set-Content -LiteralPath $driverPath", maxsplit=1
    )[0]

    fake_repo = tmp_path / "repo"
    dataset_root = (
        fake_repo / "eval" / "benchmark_data" / "terminalbench" / "tasks"
    )
    (dataset_root / "break-filter-js-from-html").mkdir(parents=True)
    (dataset_root / "break-filter-js-from-html" / "instruction.md").write_text(
        "fixed task input\n", encoding="utf-8"
    )
    dataset_file = dataset_root.parent / "terminalbench_2.jsonl"
    dataset_file.write_text('{"name": "before-run"}\n', encoding="utf-8")
    expected_dataset_hash = hashlib.sha256(dataset_file.read_bytes()).hexdigest()
    expected_task_tree_hash = terminalbenchofficial_module.task_tree_sha256(dataset_root)

    output_root = tmp_path / "upstream"
    receipt_root = tmp_path / "receipt"
    internal_run_id = "tb-script-fixture"

    class NoopTraceCapture:
        def install(self, _endpoint: str) -> None:
            return None

        def stop(self) -> None:
            return None

    class NoopReceiptTrace:
        def __init__(self, _run_id: str, _task_id: str) -> None:
            pass

        def __enter__(self) -> Self:
            return self

        def __exit__(self, *_args: object) -> None:
            return None

        def worker_environment(self):
            return nullcontext()

        def scorer(self):
            return nullcontext()

    def run_child_and_replace_dataset(*_args: object, **_kwargs: object) -> SimpleNamespace:
        dataset_file.write_text('{"name": "changed-during-run"}\n', encoding="utf-8")
        official_run = output_root / internal_run_id
        official_run.mkdir(parents=True)
        (official_run / "results.json").write_text(
            json.dumps(
                {
                    "results": [
                        {
                            "task_id": "break-filter-js-from-html",
                            "is_resolved": False,
                        }
                    ]
                }
            ),
            encoding="utf-8",
        )
        (official_run / "run_metadata.json").write_text(
            json.dumps({"agent_name": "fixture-agent", "n_concurrent_trials": 1}),
            encoding="utf-8",
        )
        return SimpleNamespace(returncode=0)

    monkeypatch.setattr(terminalbench_module, "_patch_terminal_bench_windows", lambda: None)
    monkeypatch.setattr(
        terminalbenchofficial_module,
        "TERMINAL_BENCH_DATASET_SHA256",
        expected_dataset_hash,
    )
    monkeypatch.setattr(
        terminalbenchofficial_module,
        "TERMINAL_BENCH_TASK_TREE_SHA256",
        expected_task_tree_hash,
    )
    monkeypatch.setattr(proxy_module, "internal_harness_run_id", lambda _run_id: internal_run_id)
    monkeypatch.setattr(trace_capture_module, "TraceCapture", NoopTraceCapture)
    monkeypatch.setattr(receipt_trace_module, "OfficialReceiptTrace", NoopReceiptTrace)
    monkeypatch.setattr(
        receipt_trace_module,
        "write_official_receipt_trace",
        lambda *_args, **_kwargs: {"verdict": "PASS"},
    )
    monkeypatch.setattr(receipt_trace_module, "refresh_receipt_checksums", lambda *_args: None)
    monkeypatch.setattr(subprocess, "run", run_child_and_replace_dataset)
    monkeypatch.setattr(sys, "argv", [str(tmp_path / "official_driver.py")])
    monkeypatch.setenv("LOCALCODE_REPO_ROOT", str(fake_repo))
    monkeypatch.setenv("LOCAL_LLM_API_KEY", "test-sentinel")
    monkeypatch.setenv("LOCAL_LLM_BASE_URL", "http://127.0.0.1:1/v1")
    monkeypatch.setenv("TERMINALBENCH_RUN_ID", "receipt-driver-fixture")
    monkeypatch.setenv("TERMINALBENCH_OUTPUT_DIR", str(output_root))
    monkeypatch.setenv("TERMINALBENCH_RECEIPT_ROOT", str(receipt_root))
    driver_path = tmp_path / "official_driver.py"
    driver_path.write_text(driver_source, encoding="utf-8")

    with pytest.raises(ValueError, match="dataset hash does not match configured pin"):
        runpy.run_path(str(driver_path), run_name="__main__")

    assert not (receipt_root / "receipt.json").exists()
