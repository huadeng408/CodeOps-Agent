"""Fixed evaluation budget contract tests for the unified Harness path."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.adapter import EvalInstance, EvalResult
from eval.harness import Budget, HarnessRun, RunArtifacts
from eval.harness.budget import budget_contract, budget_contract_sha256
from eval.harness.runner import SCORER_RAW_OUTPUT_KEY


def _pinned_config(**overrides: object) -> dict[str, object]:
    config: dict[str, object] = {
        "git_sha": "a1b2c3d",
        "dirty_hash": "0" * 64,
        "model": "test-model",
        "prompt_hash": "0" * 64,
    }
    config.update(overrides)
    return config


class FixedAdapter:
    def __init__(self, result: EvalResult | None = None) -> None:
        self.result = result
        self.calls = 0

    def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs: object) -> EvalResult:
        del working_dir, kwargs
        self.calls += 1
        return self.result or EvalResult(
            instance_id=instance.instance_id,
            answer="ok",
            tokens_in=3,
            tokens_out=2,
            cost=0.01,
        )


def test_budget_contract_is_canonical_and_hashed() -> None:
    budget = Budget(
        wall_clock_seconds=120,
        instance_wall_clock_seconds=90,
        scorer_reserve_seconds=15,
        max_tokens=10_000,
        max_cost=3.5,
        max_output_bytes=50_000,
        max_processes=2,
    )

    contract = budget_contract(budget)
    assert contract == {
        "version": 1,
        "scope": "run",
        "limits": {
            "wall_clock_seconds": 120.0,
            "instance_wall_clock_seconds": 90.0,
            "scorer_reserve_seconds": 15.0,
            "max_tokens": 10_000,
            "max_cost": 3.5,
            "max_output_bytes": 50_000,
            "max_processes": 2,
        },
    }
    digest = budget_contract_sha256(budget)
    assert len(digest) == 64
    assert digest == budget_contract_sha256(budget)


def test_harness_persists_one_budget_contract_and_consumption_snapshot(tmp_path: Path) -> None:
    budget = Budget(
        wall_clock_seconds=60,
        instance_wall_clock_seconds=45,
        scorer_reserve_seconds=10,
        max_tokens=100,
        max_cost=2.0,
        max_output_bytes=100,
        max_processes=2,
    )
    artifacts = RunArtifacts("budget-contract", tmp_path)
    checkpoint = tmp_path / "budget-contract.checkpoint"
    harness = HarnessRun(
        run_id="budget-contract",
        artifacts=artifacts,
        budget=budget,
        adapter=FixedAdapter(),
        checkpoint_path=checkpoint,
        config=_pinned_config(),
    )

    result = harness.run(
        [
            EvalInstance(instance_id="one", task_description="task"),
            EvalInstance(instance_id="two", task_description="task"),
        ]
    )

    summary = result["summary"]
    manifest = json.loads((artifacts.root / "run-manifest.json").read_text(encoding="utf-8"))
    assert summary["budget"]["contract"] == budget_contract(budget)
    assert summary["budget"]["contract_sha256"] == budget_contract_sha256(budget)
    assert summary["budget"]["instance_count"] == 2
    assert summary["budget"]["usage"]["tokens"] == 10
    assert summary["budget"]["usage"]["cost"] == pytest.approx(0.02)
    assert summary["budget"]["usage"]["output_bytes"] == 4
    assert manifest["budget_contract"] == budget_contract(budget)
    assert manifest["budget_contract_sha256"] == budget_contract_sha256(budget)
    assert manifest["budgets"]["contract"] == manifest["budget_contract"]
    assert manifest["budgets"]["contract_sha256"] == manifest["budget_contract_sha256"]
    assert manifest["budget_instance_count"] == 2
    checkpoint_header = json.loads(checkpoint.read_text(encoding="utf-8").splitlines()[0])
    assert checkpoint_header["contract"]["budget"]["contract"] == manifest["budget_contract"]
    assert checkpoint_header["contract"]["budget"]["contract_sha256"] == manifest[
        "budget_contract_sha256"
    ]
    assert artifacts.verify_checksums() == []


def test_result_output_bytes_are_enforced_by_the_harness(tmp_path: Path) -> None:
    adapter = FixedAdapter(
        EvalResult(instance_id="too-large", answer="12345678901")
    )
    artifacts = RunArtifacts("budget-output", tmp_path)
    harness = HarnessRun(
        run_id="budget-output",
        artifacts=artifacts,
        budget=Budget(max_output_bytes=10),
        adapter=adapter,
        config=_pinned_config(),
    )

    summary = harness.run(
        [EvalInstance(instance_id="too-large", task_description="task")]
    )["summary"]

    assert adapter.calls == 1
    assert summary["completed"] == 0
    assert summary["by_category"]["oom"] == 1
    assert summary["budget"]["usage"]["output_bytes"] == 11
    assert not (artifacts.root / "predictions.jsonl").exists()


def test_declared_budget_contract_mismatch_fails_closed_before_adapter(tmp_path: Path) -> None:
    adapter = FixedAdapter()
    artifacts = RunArtifacts("budget-mismatch", tmp_path)
    config = _pinned_config(
        budget_contract={
            "version": 1,
            "scope": "run",
            "limits": {
                "wall_clock_seconds": 60.0,
                "instance_wall_clock_seconds": None,
                "scorer_reserve_seconds": 0.0,
                "max_tokens": 999,
                "max_cost": 2.0,
                "max_output_bytes": 1_000_000,
                "max_processes": 4,
            },
        }
    )
    harness = HarnessRun(
        run_id="budget-mismatch",
        artifacts=artifacts,
        budget=Budget(max_tokens=100),
        adapter=adapter,
        config=config,
    )

    with pytest.raises(ValueError, match="budget contract"):
        harness.run([EvalInstance(instance_id="one", task_description="task")])

    assert adapter.calls == 0
    assert not (artifacts.root / "run-manifest.json").exists()
    assert not (artifacts.root / "checksums.sha256").exists()


def test_structured_scorer_output_bytes_are_enforced_before_prediction(
    tmp_path: Path,
) -> None:
    artifacts = RunArtifacts("budget-structured-scorer", tmp_path)

    def scorer(*args: object, **kwargs: object) -> dict[str, object]:
        del args, kwargs
        return {"status": "x" * 32}

    harness = HarnessRun(
        run_id="budget-structured-scorer",
        artifacts=artifacts,
        budget=Budget(max_output_bytes=10),
        adapter=FixedAdapter(),
        scorer=scorer,
        config=_pinned_config(),
    )

    summary = harness.run(
        [EvalInstance(instance_id="structured", task_description="task")]
    )["summary"]

    assert summary["completed"] == 0
    assert summary["by_category"]["oom"] == 1
    assert summary["budget"]["usage"]["output_bytes"] > 10
    assert not (artifacts.root / "predictions.jsonl").exists()


def test_raw_scorer_output_is_checked_and_accounted_before_write(
    tmp_path: Path,
) -> None:
    artifacts = RunArtifacts("budget-raw-scorer", tmp_path)

    def scorer(*args: object, **kwargs: object) -> dict[str, object]:
        del args, kwargs
        return {SCORER_RAW_OUTPUT_KEY: {"raw.txt": "bb"}}

    harness = HarnessRun(
        run_id="budget-raw-scorer",
        artifacts=artifacts,
        budget=Budget(max_output_bytes=2),
        adapter=FixedAdapter(
            EvalResult(instance_id="raw", answer="a")
        ),
        scorer=scorer,
        config=_pinned_config(),
    )

    summary = harness.run(
        [EvalInstance(instance_id="raw", task_description="task")]
    )["summary"]

    assert summary["completed"] == 0
    assert summary["by_category"]["oom"] == 1
    assert summary["budget"]["usage"]["output_bytes"] == 3
    assert not (artifacts.root / "scorer" / "raw.txt").exists()
