"""Harness runner/budget/artifact tests (plan Task 8.1 / Phase 4).

Updated for AgentAdapter protocol: tests use FakeAgentAdapter instead of
the old InstanceRunner(dict) callable.
"""

from __future__ import annotations

import json
import time
from pathlib import Path

import pytest

from eval.adapter import EvalInstance, EvalResult
from eval.harness.artifacts import RunArtifacts
from eval.harness.budget import Budget, BudgetExceeded, BudgetUsage, check_budget
from eval.harness.runner import (
    ERROR_AGENT,
    ERROR_INFRA,
    ERROR_OOM,
    ERROR_SCORER,
    ERROR_TIMEOUT,
    HarnessRun,
    ScorerError,
    classify_error,
)


# ---------------------------------------------------------------------------
# FakeAgentAdapter — deterministic fake for harness tests
# ---------------------------------------------------------------------------


class FakeAgentAdapter:
    """Deterministic fake adapter for harness tests."""

    def __init__(self, answers: dict[str, EvalResult] | None = None):
        self._answers = answers or {}
        self.calls: list[tuple[str, str]] = []  # (instance_id, working_dir)

    def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
        self.calls.append((instance.instance_id, working_dir))
        if instance.instance_id in self._answers:
            return self._answers[instance.instance_id]
        return EvalResult(
            instance_id=instance.instance_id,
            answer="ok",
            cost=0.01,
            tokens_in=100,
            tokens_out=50,
            wall_time_s=1.0,
            trace_id=f"trace-{instance.instance_id}",
        )


def _pinned_config(**overrides: object) -> dict[str, object]:
    """Minimal valid harness config — mandatory manifest pins (H3)."""
    config: dict[str, object] = {
        "git_sha": "a1b2c3d",
        "dirty_hash": "0" * 64,
        "model": "test-model",
        "prompt_hash": "0" * 64,
    }
    config.update(overrides)
    return config


# ---------------------------------------------------------------------------
# Budget tests (unchanged)
# ---------------------------------------------------------------------------


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


# ---------------------------------------------------------------------------
# Artifact tests (unchanged)
# ---------------------------------------------------------------------------


def test_run_artifacts_append_line_and_atomic_summary(tmp_path: Path) -> None:
    artifacts = RunArtifacts("run-1", tmp_path)
    artifacts.record_instance({"instance_id": "i1"})
    artifacts.record_prediction({"instance_id": "i1", "answer": "x"})
    summary_path = artifacts.write_summary({"total": 1})

    assert summary_path.exists()
    assert json.loads(summary_path.read_text(encoding="utf-8")) == {"total": 1}
    lines = (artifacts.root / "instances.jsonl").read_text(encoding="utf-8").splitlines()
    assert len(lines) == 1


def test_environment_excludes_process_variables(tmp_path: Path, monkeypatch) -> None:
    artifacts = RunArtifacts("run-1", tmp_path)
    monkeypatch.setenv("TEST_API_KEY", "secret-value")
    monkeypatch.setenv("TEST_NORMAL_VAR", "visible")
    path = artifacts.write_environment()
    content = path.read_text(encoding="utf-8")
    assert "secret-value" not in content
    assert "TEST_API_KEY" not in content
    assert "TEST_NORMAL_VAR" not in content
    assert "visible" not in content


def test_environment_omits_secret_variable_names(tmp_path: Path, monkeypatch) -> None:
    artifacts = RunArtifacts("run-1", tmp_path)
    monkeypatch.setenv("TEST_API_KEY", "secret-value")

    content = artifacts.write_environment().read_text(encoding="utf-8")

    assert "TEST_API_KEY" not in content
    assert "secret-value" not in content


# ---------------------------------------------------------------------------
# HarnessRun tests — updated for EvalInstance / FakeAgentAdapter
# ---------------------------------------------------------------------------


def test_harness_resume_skips_completed(tmp_path: Path) -> None:
    checkpoint = tmp_path / "checkpoint.txt"
    checkpoint.write_text("i1\n", encoding="utf-8")
    artifacts = RunArtifacts("run-1", tmp_path)

    adapter = FakeAgentAdapter()
    harness = HarnessRun(
        run_id="run-1", artifacts=artifacts,
        checkpoint_path=checkpoint, adapter=adapter,
        config=_pinned_config(),
    )

    result = harness.run([
        EvalInstance(instance_id="i1", task_description="task 1"),
        EvalInstance(instance_id="i2", task_description="task 2"),
    ])
    # i1 skipped via checkpoint, only i2 called
    assert adapter.calls == [("i2", adapter.calls[0][1])]
    assert result["summary"]["resumed_skipped"] == 1
    assert result["summary"]["completed"] == 1


def test_harness_classifies_failures_eval_instance(tmp_path: Path) -> None:
    artifacts = RunArtifacts("run-1", tmp_path)

    class FailAdapter:
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            if instance.instance_id == "bad":
                raise RuntimeError("connection refused")
            if instance.instance_id == "slow":
                raise TimeoutError()
            return EvalResult(instance_id=instance.instance_id, answer="ok")

    harness = HarnessRun(
        run_id="run-1", artifacts=artifacts, adapter=FailAdapter(),
        config=_pinned_config(),
    )
    result = harness.run([
        EvalInstance(instance_id="good", task_description=""),
        EvalInstance(instance_id="bad", task_description=""),
        EvalInstance(instance_id="slow", task_description=""),
    ])
    summary = result["summary"]
    assert summary["by_category"][ERROR_INFRA] == 1
    assert summary["by_category"][ERROR_TIMEOUT] == 1
    assert summary["completed"] == 1
    failures = (artifacts.root / "failures.jsonl").read_text(encoding="utf-8").splitlines()
    assert len(failures) == 2


def test_harness_network_disabled_by_default(tmp_path: Path) -> None:
    artifacts = RunArtifacts("run-1", tmp_path)
    workspaces: list[str] = []

    class SpyAdapter:
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            workspaces.append(working_dir)
            assert Path(working_dir, "NETWORK_DISABLED").exists()
            return EvalResult(instance_id=instance.instance_id, answer="ok")

    harness = HarnessRun(
        run_id="run-1", artifacts=artifacts, adapter=SpyAdapter(),
        config=_pinned_config(),
    )
    harness.run([EvalInstance(instance_id="i1", task_description="")])
    assert len(workspaces) == 1


# ---------------------------------------------------------------------------
# NEW tests — fail-closed, pre-start budget, scorer, workspace preservation
# ---------------------------------------------------------------------------


def test_missing_instance_id_fail_closed(tmp_path: Path) -> None:
    """Empty instance_id is recorded as failure, not silently skipped."""
    artifacts = RunArtifacts("run-1", tmp_path)
    adapter = FakeAgentAdapter()
    harness = HarnessRun(
        run_id="run-1", artifacts=artifacts, adapter=adapter,
        config=_pinned_config(),
    )

    instances = [
        EvalInstance(instance_id="", task_description="bad — no id"),
        EvalInstance(instance_id="ok-1", task_description="good"),
    ]
    summary = harness.run(instances)
    assert summary["summary"]["completed"] == 1  # ok-1 succeeded
    assert summary["summary"]["failed"] >= 1  # the empty-id instance counted

    # failure recorded
    failures = [
        json.loads(line)
        for line in (artifacts.root / "failures.jsonl").read_text(encoding="utf-8").splitlines()
    ]
    bad = [f for f in failures if not f["instance_id"]]
    assert len(bad) >= 1
    assert bad[0]["category"] == ERROR_AGENT


def test_pre_start_budget_check(tmp_path: Path) -> None:
    """Budget already exhausted — BudgetExceeded before any adapter call."""
    artifacts = RunArtifacts("run-1", tmp_path)
    adapter = FakeAgentAdapter()
    # Budget with effectively zero wall clock — already exhausted
    tiny_budget = Budget(wall_clock_seconds=0.0, max_tokens=1, max_cost=0.0)
    harness = HarnessRun(
        run_id="run-1", artifacts=artifacts,
        budget=tiny_budget, adapter=adapter,
        config=_pinned_config(),
    )

    result = harness.run([
        EvalInstance(instance_id="i1", task_description="should be blocked"),
    ])
    # Adapter was never called
    assert len(adapter.calls) == 0
    # Failure recorded
    assert result["summary"]["completed"] == 0
    assert result["summary"]["failed"] >= 1


def test_scorer_exception_becomes_error_scorer(tmp_path: Path) -> None:
    """Scorer that raises → classified as ERROR_SCORER."""
    artifacts = RunArtifacts("run-1", tmp_path)

    class OkAdapter:
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            return EvalResult(instance_id=instance.instance_id, answer="ok")

    def bad_scorer(result: EvalResult, instance: EvalInstance, workspace: Path) -> dict:
        raise RuntimeError("scorer crashed")

    harness = HarnessRun(
        run_id="run-1", artifacts=artifacts,
        adapter=OkAdapter(), scorer=bad_scorer,
        config=_pinned_config(),
    )
    harness.run([EvalInstance(instance_id="s-1", task_description="")])

    # scorer exception must be classified as ERROR_SCORER
    summary_path = artifacts.root / "summary.json"
    summary = json.loads(summary_path.read_text(encoding="utf-8"))
    assert summary["by_category"][ERROR_SCORER] >= 1, f"Expected scorer failures, got: {summary['by_category']}"

    failures = [
        json.loads(line)
        for line in (artifacts.root / "failures.jsonl").read_text(encoding="utf-8").splitlines()
    ]
    scorer_failures = [f for f in failures if f["category"] == ERROR_SCORER]
    assert len(scorer_failures) >= 1, f"Expected scorer failures, got: {failures}"


def test_workspace_preserved_on_failure(tmp_path: Path) -> None:
    """Workspace directory survives after instance failure."""
    artifacts = RunArtifacts("run-1", tmp_path)
    workspaces_seen: list[str] = []

    class FailAdapter:
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            workspaces_seen.append(working_dir)
            raise RuntimeError("simulated agent crash")

    harness = HarnessRun(
        run_id="run-1", artifacts=artifacts, adapter=FailAdapter(),
        config=_pinned_config(),
    )
    harness.run([EvalInstance(instance_id="crash-1", task_description="")])

    # workspace must still exist
    assert len(workspaces_seen) == 1
    assert Path(workspaces_seen[0]).exists(), f"workspace deleted: {workspaces_seen[0]}"


def test_scorer_success_merges_into_prediction(tmp_path: Path) -> None:
    """Successful scorer result is merged into the prediction artifact."""
    artifacts = RunArtifacts("run-1", tmp_path)

    class OkAdapter:
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            return EvalResult(instance_id=instance.instance_id, answer="ok")

    def good_scorer(result: EvalResult, instance: EvalInstance, workspace: Path) -> dict:
        return {"score": 0.95, "passed": True}

    harness = HarnessRun(
        run_id="run-1", artifacts=artifacts,
        adapter=OkAdapter(), scorer=good_scorer,
        config=_pinned_config(),
    )
    harness.run([EvalInstance(instance_id="s-1", task_description="")])

    predictions = [
        json.loads(line)
        for line in (artifacts.root / "predictions.jsonl").read_text(encoding="utf-8").splitlines()
    ]
    assert len(predictions) == 1
    assert predictions[0]["score"] == 0.95
    assert predictions[0]["passed"] is True
