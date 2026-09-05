"""Unified Terminal-Bench adapter lifecycle contracts."""

from __future__ import annotations

import json
import sys
from pathlib import Path
from types import ModuleType, SimpleNamespace

import pytest

from eval.adapter import EvalInstance


def _instance(data_dir: Path) -> EvalInstance:
    return EvalInstance(
        instance_id="fixed-task",
        task_description="run the task",
        metadata={"task_id": "fixed-task", "data_dir": str(data_dir)},
    )


def _write_task(data_dir: Path) -> None:
    task_dir = data_dir / "tasks" / "fixed-task"
    task_dir.mkdir(parents=True)
    (task_dir / "instruction.md").write_text("fixed task\n", encoding="utf-8")


def test_unresolved_official_trial_remains_a_scoreable_result(
    tmp_path: Path, monkeypatch
) -> None:
    """Official ``resolved=False`` is a measurement, not an adapter error."""
    from eval.benchmarks import terminalbench as module

    data_dir = tmp_path / "terminalbench"
    _write_task(data_dir)

    class FakeHarness:
        def __init__(self, **kwargs: object) -> None:
            del kwargs

        def run(self) -> object:
            return SimpleNamespace(
                results=[
                    SimpleNamespace(
                        task_id="fixed-task",
                        is_resolved=False,
                        failure_mode="TEST_FAILURE",
                        total_input_tokens=7,
                        total_output_tokens=5,
                    )
                ]
            )

    terminal_harness = ModuleType("terminal_bench.harness")
    terminal_harness.Harness = FakeHarness
    terminal_package = ModuleType("terminal_bench")
    terminal_package.harness = terminal_harness
    monkeypatch.setitem(sys.modules, "terminal_bench", terminal_package)
    monkeypatch.setitem(sys.modules, "terminal_bench.harness", terminal_harness)

    monkeypatch.setattr(module, "_can_score_official", lambda: (True, "available"))
    monkeypatch.setattr(module, "_patch_terminal_bench_windows", lambda: None)

    adapter = module.TerminalBenchAdapter(
        data_dir=data_dir,
        agent_import_path="fixture:Agent",
    )
    workspace = tmp_path / "workspace"
    result = adapter.solve(_instance(data_dir), workspace, object())

    assert result.error == ""
    sidecar = json.loads((workspace / "score.json").read_text(encoding="utf-8"))
    assert sidecar["resolved"] is False
    assert sidecar["failure_mode"] == "TEST_FAILURE"


def test_harness_records_official_unresolved_trial_as_completed_measurement(
    tmp_path: Path, monkeypatch
) -> None:
    """The unified lifecycle must preserve an official false verdict."""
    from eval.benchmarks import terminalbench as module
    from eval.harness import Budget, HarnessRun, RunArtifacts

    data_dir = tmp_path / "terminalbench"
    _write_task(data_dir)

    class FakeHarness:
        def __init__(self, **kwargs: object) -> None:
            del kwargs

        def run(self) -> object:
            return SimpleNamespace(
                results=[
                    SimpleNamespace(
                        task_id="fixed-task",
                        is_resolved=False,
                        failure_mode="TEST_FAILURE",
                        total_input_tokens=7,
                        total_output_tokens=5,
                    )
                ]
            )

    terminal_harness = ModuleType("terminal_bench.harness")
    terminal_harness.Harness = FakeHarness
    terminal_package = ModuleType("terminal_bench")
    terminal_package.harness = terminal_harness
    monkeypatch.setitem(sys.modules, "terminal_bench", terminal_package)
    monkeypatch.setitem(sys.modules, "terminal_bench.harness", terminal_harness)
    monkeypatch.setattr(module, "_can_score_official", lambda: (True, "available"))
    monkeypatch.setattr(module, "_patch_terminal_bench_windows", lambda: None)

    benchmark = module.TerminalBenchAdapter(
        data_dir=data_dir,
        agent_import_path="fixture:Agent",
    )

    class BenchmarkDriver:
        def solve_instance(
            self, instance: EvalInstance, working_dir: str, **kwargs: object
        ) -> object:
            return benchmark.solve(instance, Path(working_dir), object(), **kwargs)

    run_id = "terminalbench-unresolved"
    artifacts = RunArtifacts(run_id, tmp_path / "artifacts")
    harness = HarnessRun(
        run_id=run_id,
        artifacts=artifacts,
        budget=Budget(max_output_bytes=100_000),
        adapter=BenchmarkDriver(),
        scorer=benchmark.score,
        setup_workspace=benchmark.prepare,
        config={
            "git_sha": "a1b2c3d",
            "dirty_hash": "0" * 64,
            "model": "test-model",
            "prompt_hash": "0" * 64,
            "benchmark": "terminal-bench",
            "trace_capabilities": (),
        },
    )

    summary = harness.run([_instance(data_dir)])["summary"]

    assert summary["completed"] == 1
    assert summary["failed"] == 0
    prediction = json.loads(
        (artifacts.root / "predictions.jsonl").read_text(encoding="utf-8")
    )
    assert prediction["resolved"] is False
    assert prediction["scorer_status"].startswith("official:")
    assert prediction["instance_id"] == "fixed-task"
    assert prediction["failure_mode"] == "TEST_FAILURE"
    raw_scorer = artifacts.root / "scorer" / "fixed-task-score.json"
    assert raw_scorer.is_file()
    assert json.loads(raw_scorer.read_text(encoding="utf-8"))["resolved"] is False


def test_terminalbench_score_fails_closed_when_official_sidecar_is_missing(
    tmp_path: Path,
) -> None:
    """Missing official evidence must not become a synthetic false verdict."""
    from eval.benchmarks.terminalbench import TerminalBenchAdapter

    with pytest.raises(RuntimeError, match="official trial record missing"):
        TerminalBenchAdapter().score(
            type("Result", (), {"instance_id": "fixed-task"})(),
            type("Instance", (), {"instance_id": "fixed-task"})(),
            tmp_path,
        )
