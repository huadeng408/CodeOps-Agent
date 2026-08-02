"""Harness runner/budget/artifact tests (plan Task 8.1)."""

from __future__ import annotations

import json
import time
from pathlib import Path

import pytest

from eval.harness.artifacts import RunArtifacts
from eval.harness.budget import Budget, BudgetExceeded, BudgetUsage, check_budget
from eval.harness.runner import (
    ERROR_AGENT,
    ERROR_INFRA,
    ERROR_OOM,
    ERROR_TIMEOUT,
    HarnessRun,
    classify_error,
)


def test_budget_wall_clock_exceeded() -> None:
    budget = Budget(wall_clock_seconds=0.001)
    usage = BudgetUsage()
    time.sleep(0.01)
    with pytest.raises(BudgetExceeded) as exc:
        check_budget(budget, usage)
    assert exc.value.kind == "wall-clock"


def test_budget_token_exceeded() -> None:
    budget = Budget(max_tokens=100)
    usage = BudgetUsage()
    usage.record_tokens(101)
    with pytest.raises(BudgetExceeded) as exc:
        check_budget(budget, usage)
    assert exc.value.kind == "tokens"


def test_budget_cost_exceeded() -> None:
    budget = Budget(max_cost=1.0)
    usage = BudgetUsage()
    usage.record_cost(1.01)
    with pytest.raises(BudgetExceeded) as exc:
        check_budget(budget, usage)
    assert exc.value.kind == "cost"


def test_budget_output_exceeded() -> None:
    budget = Budget(max_output_bytes=10)
    usage = BudgetUsage()
    usage.record_output(11)
    with pytest.raises(BudgetExceeded) as exc:
        check_budget(budget, usage)
    assert exc.value.kind == "output"


def test_classify_error_taxonomy() -> None:
    assert classify_error(TimeoutError()) == ERROR_TIMEOUT
    assert classify_error(MemoryError()) == ERROR_OOM
    assert classify_error(BudgetExceeded("wall-clock", "x")) == ERROR_TIMEOUT
    assert classify_error(BudgetExceeded("output", "x")) == ERROR_OOM
    assert classify_error(RuntimeError("connection refused")) == ERROR_INFRA
    assert classify_error(ValueError("bad value")) == ERROR_AGENT


def test_run_artifacts_append_line_and_atomic_summary(tmp_path: Path) -> None:
    artifacts = RunArtifacts("run-1", tmp_path)
    artifacts.record_instance({"instance_id": "i1"})
    artifacts.record_prediction({"instance_id": "i1", "answer": "x"})
    summary_path = artifacts.write_summary({"total": 1})

    assert summary_path.exists()
    assert json.loads(summary_path.read_text(encoding="utf-8")) == {"total": 1}
    lines = (artifacts.root / "instances.jsonl").read_text(encoding="utf-8").splitlines()
    assert len(lines) == 1


def test_environment_redacts_secrets(tmp_path: Path) -> None:
    artifacts = RunArtifacts("run-1", tmp_path)
    import os

    os.environ["TEST_API_KEY"] = "secret-value"
    os.environ["TEST_NORMAL_VAR"] = "visible"
    path = artifacts.write_environment()
    content = path.read_text(encoding="utf-8")
    assert "secret-value" not in content
    assert "<redacted>" in content
    assert "visible" in content


def test_harness_resume_skips_completed(tmp_path: Path) -> None:
    checkpoint = tmp_path / "checkpoint.txt"
    checkpoint.write_text("i1\n", encoding="utf-8")
    artifacts = RunArtifacts("run-1", tmp_path)
    harness = HarnessRun(run_id="run-1", artifacts=artifacts, checkpoint_path=checkpoint)

    seen: list[str] = []

    def runner(instance: dict, workspace: Path, usage: BudgetUsage) -> dict:
        seen.append(str(instance["instance_id"]))
        return {"answer": "ok"}

    harness.runner = runner
    result = harness.run([{"instance_id": "i1"}, {"instance_id": "i2"}])
    assert seen == ["i2"]  # i1 skipped via checkpoint
    assert result["summary"]["resumed_skipped"] == 1
    assert result["summary"]["completed"] == 1


def test_harness_classifies_failures(tmp_path: Path) -> None:
    artifacts = RunArtifacts("run-1", tmp_path)
    harness = HarnessRun(run_id="run-1", artifacts=artifacts)

    def runner(instance: dict, workspace: Path, usage: BudgetUsage) -> dict:
        if instance["instance_id"] == "bad":
            raise RuntimeError("connection refused")
        if instance["instance_id"] == "slow":
            raise TimeoutError()
        return {"answer": "ok"}

    harness.runner = runner
    result = harness.run([{"instance_id": "good"}, {"instance_id": "bad"}, {"instance_id": "slow"}])
    summary = result["summary"]
    assert summary["by_category"][ERROR_INFRA] == 1
    assert summary["by_category"][ERROR_TIMEOUT] == 1
    assert summary["completed"] == 1
    failures = (artifacts.root / "failures.jsonl").read_text(encoding="utf-8").splitlines()
    assert len(failures) == 2


def test_harness_network_disabled_by_default(tmp_path: Path) -> None:
    artifacts = RunArtifacts("run-1", tmp_path)
    harness = HarnessRun(run_id="run-1", artifacts=artifacts)
    workspaces: list[Path] = []

    def runner(instance: dict, workspace: Path, usage: BudgetUsage) -> dict:
        workspaces.append(workspace)
        assert (workspace / "NETWORK_DISABLED").exists()
        return {}

    harness.runner = runner
    harness.run([{"instance_id": "i1"}])
    assert len(workspaces) == 1
