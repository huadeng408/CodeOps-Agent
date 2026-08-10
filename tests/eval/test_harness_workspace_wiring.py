"""HarnessRun workspace-setup wiring (H5 blocker).

``HarnessRun`` exposes a ``setup_workspace`` hook and invokes it before
``adapter.solve_instance`` (``eval/harness/runner.py``), and every
:class:`AgentBenchmark` implements ``prepare(instance, workspace)`` to
populate that workspace (for SWE-bench: clone the repo at ``base_commit``).

Nothing connected the two.  ``eval/run.py`` built ``HarnessRun`` with
``adapter=`` and ``scorer=`` but never ``setup_workspace=``, so the agent
was handed an EMPTY temp directory: it could not read the repository, the
captured git diff was necessarily empty, and the official scorer was fed
``model_patch: ""``.  The run still exited 0 and produced a complete-looking
artifact tree, which is exactly the "looks complete, measures nothing"
failure the evidence rules exist to catch.

These tests pin the wiring, the call order, and — most importantly — that a
failed workspace setup is recorded as a FAILURE rather than degrading into a
scored empty-patch prediction.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from eval.adapter import EvalInstance, EvalResult


def _pinned_config(**overrides: object) -> dict[str, object]:
    config: dict[str, object] = {
        "git_sha": "a1b2c3d",
        "dirty_hash": "0" * 64,
        "model": "test-model",
        "prompt_hash": "0" * 64,
    }
    config.update(overrides)
    return config


# ---------------------------------------------------------------------------
# 1. eval/run.py must wire setup_workspace to the benchmark's prepare()
# ---------------------------------------------------------------------------


def test_run_cli_wires_setup_workspace_to_agent_benchmark_prepare(
    tmp_path: Path, monkeypatch
) -> None:
    """The CLI must hand the benchmark's prepare() to the harness.

    Without this the SWE-bench agent runs against an empty directory.
    """
    import eval.harness as harness_mod
    import eval.benchmarks.swebench as swebench_mod
    import eval.driver_headless as driver_mod
    from eval import run as run_mod

    captured: dict[str, Any] = {}

    class CapturingHarness:
        def __init__(self, **kwargs: Any) -> None:
            captured.update(kwargs)

        def run(self, instances: list[EvalInstance]) -> dict[str, Any]:
            captured["instances"] = instances
            return {
                "summary": {"total": len(instances), "ok": len(instances), "failed": 0},
                "summary_path": str(tmp_path / "summary.json"),
            }

    class FakeDriver:
        model = "fake-model"

        def solve_instance(
            self, instance: EvalInstance, working_dir: str = "", **kwargs: Any
        ) -> EvalResult:
            return EvalResult(instance_id=instance.instance_id, answer="")

    monkeypatch.setattr(harness_mod, "HarnessRun", CapturingHarness)
    monkeypatch.setattr(driver_mod, "create_driver", lambda **kwargs: FakeDriver())
    monkeypatch.setattr(
        swebench_mod,
        "load_instances",
        lambda limit=None, **kwargs: [
            EvalInstance(
                instance_id="astropy__astropy-12907",
                task_description="issue text",
                metadata={"repo": "astropy/astropy", "base_commit": "d16bfe0"},
            )
        ],
    )

    rc = run_mod.main(
        ["-b", "swebench", "-m", "fake-model", "--smoke", "-o", str(tmp_path)]
    )
    assert rc == 0

    setup = captured.get("setup_workspace")
    assert setup is not None, (
        "HarnessRun was built without setup_workspace: the agent would run in an "
        "empty temp dir and could only ever produce an empty patch"
    )
    assert callable(setup)
    # It must be the benchmark adapter's prepare, not an unrelated callable.
    assert getattr(setup, "__name__", "") == "prepare"
    assert isinstance(
        getattr(setup, "__self__", None), swebench_mod.SWEBenchAdapter
    ), "setup_workspace must be bound to the SWEBenchAdapter instance"


# ---------------------------------------------------------------------------
# 2. The hook must run before the agent, and receive the real workspace
# ---------------------------------------------------------------------------


def test_harness_invokes_setup_workspace_before_solve(tmp_path: Path) -> None:
    from eval.harness import Budget, HarnessRun, RunArtifacts

    order: list[str] = []
    seen: dict[str, str] = {}

    def setup(instance: EvalInstance, workspace: str) -> None:
        order.append("setup")
        seen["workspace"] = workspace
        # Prove the agent can observe what setup wrote.
        Path(workspace, "cloned.txt").write_text("repo", encoding="utf-8")

    class Adapter:
        def solve_instance(
            self, instance: EvalInstance, working_dir: str, **kwargs: Any
        ) -> EvalResult:
            order.append("solve")
            assert Path(working_dir, "cloned.txt").exists(), (
                "agent must see the populated workspace"
            )
            assert working_dir == seen["workspace"]
            return EvalResult(instance_id=instance.instance_id, model_patch="diff --git")

    run_id = "test-setup-order"
    harness = HarnessRun(
        run_id=run_id,
        artifacts=RunArtifacts(run_id=run_id, root=str(tmp_path)),
        budget=Budget(wall_clock_seconds=60, max_tokens=10_000),
        adapter=Adapter(),
        setup_workspace=setup,
        config=_pinned_config(),
    )

    summary = harness.run(
        [EvalInstance(instance_id="inst-1", task_description="t")]
    )
    assert summary["summary"]["ok"] == 1
    assert order == ["setup", "solve"]


# ---------------------------------------------------------------------------
# 3. Honesty guard: a failed setup is a FAILURE, never a scored empty patch
# ---------------------------------------------------------------------------


def test_setup_workspace_failure_is_recorded_not_scored_as_empty_patch(
    tmp_path: Path,
) -> None:
    """A clone failure must abort the instance, not produce a prediction.

    If setup raises and the harness still called the agent and the scorer,
    the artifact tree would carry a scored ``model_patch: ""`` prediction —
    an infrastructure fault wearing the costume of an honest agent miss.
    """
    from eval.harness import Budget, HarnessRun, RunArtifacts

    scorer_calls: list[str] = []
    solve_calls: list[str] = []

    def setup(instance: EvalInstance, workspace: str) -> None:
        raise RuntimeError("git clone failed: repository not found")

    class Adapter:
        def solve_instance(
            self, instance: EvalInstance, working_dir: str, **kwargs: Any
        ) -> EvalResult:
            solve_calls.append(instance.instance_id)
            return EvalResult(instance_id=instance.instance_id, model_patch="")

    def scorer(
        result: EvalResult, instance: EvalInstance, workspace: Path
    ) -> dict[str, Any]:
        scorer_calls.append(instance.instance_id)
        return {"resolved": False}

    run_id = "test-setup-failure"
    root = tmp_path / run_id
    harness = HarnessRun(
        run_id=run_id,
        artifacts=RunArtifacts(run_id=run_id, root=str(tmp_path)),
        budget=Budget(wall_clock_seconds=60, max_tokens=10_000),
        adapter=Adapter(),
        scorer=scorer,
        setup_workspace=setup,
        config=_pinned_config(),
    )

    summary = harness.run(
        [EvalInstance(instance_id="inst-fail", task_description="t")]
    )

    assert summary["summary"]["ok"] == 0
    assert summary["summary"]["failed"] >= 1
    assert solve_calls == [], "agent must not run on an unprepared workspace"
    assert scorer_calls == [], "scorer must not score an unprepared workspace"

    failures = [
        json.loads(line)
        for line in root.joinpath("failures.jsonl")
        .read_text(encoding="utf-8")
        .strip()
        .splitlines()
    ]
    assert any(f["instance_id"] == "inst-fail" for f in failures)
    assert any("clone failed" in f.get("message", "") for f in failures)

    predictions = root / "predictions.jsonl"
    if predictions.exists():
        recorded = [
            json.loads(line)
            for line in predictions.read_text(encoding="utf-8").strip().splitlines()
            if line.strip()
        ]
        assert not any(p["instance_id"] == "inst-fail" for p in recorded), (
            "a failed workspace setup must not leave a scored prediction behind"
        )
