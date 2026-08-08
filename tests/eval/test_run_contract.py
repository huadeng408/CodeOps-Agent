"""Module-level run() contract tests (eval/run.py CLI alignment).

Each benchmark module in ``eval/benchmarks/`` must expose a module-level
``run(driver, limit=None, **kwargs)`` that returns a list of
:class:`EvalResult`-shaped records, so the unified CLI
(``python -m eval.run -b <name>``) can drive every benchmark through the
same code path.
"""

from __future__ import annotations

import importlib
import json
from pathlib import Path

import pytest

from eval.adapter import EvalResult

FOUR_BENCHMARKS = ("evalplus", "swebench", "tau2bench", "terminalbench")


class FakeDriver:
    """Minimal AgentAdapter stand-in: returns a canned EvalResult."""

    model = "fake-model"

    def solve_instance(self, instance, working_dir: str = "", **kwargs) -> EvalResult:
        return EvalResult(
            instance_id=instance.instance_id,
            answer="pass",
            cost=0.0,
            tokens_in=0,
            tokens_out=0,
        )


# ---------------------------------------------------------------------------
# Module contract: every benchmark exposes a callable run()
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("name", FOUR_BENCHMARKS)
def test_module_exposes_run(name: str) -> None:
    mod = importlib.import_module(f"eval.benchmarks.{name}")
    assert callable(getattr(mod, "run", None))


# ---------------------------------------------------------------------------
# evalplus.run(): builds EvalPlusBenchmark, returns per-instance results
# ---------------------------------------------------------------------------


def test_evalplus_run_returns_eval_result_list(tmp_path: Path, monkeypatch) -> None:
    from eval.benchmarks import evalplus as mod

    # Hermetic: stub problem loading and scoring; the wrapper must still
    # return the per-instance EvalResult list and honor limit.
    monkeypatch.setattr(
        mod,
        "_load_problems",
        lambda dataset: {
            f"HumanEval/{i}": {
                "prompt": f"def f{i}():\n    pass\n",
                "entry_point": f"f{i}",
                "canonical_solution": f"def f{i}():\n    pass\n",
            }
            for i in range(5)
        },
    )
    monkeypatch.setattr(
        mod,
        "_score",
        lambda **kw: mod.EvalPlusStats(dataset="humaneval", num_instances=2),
    )

    results = mod.run(FakeDriver(), limit=2, output_dir=str(tmp_path))
    assert len(results) == 2
    assert all(isinstance(r, EvalResult) for r in results)
    assert [r.instance_id for r in results] == ["HumanEval/0", "HumanEval/1"]
    assert all(r.answer == "pass" for r in results)


def test_evalplus_run_defaults_to_full_dataset(tmp_path: Path, monkeypatch) -> None:
    from eval.benchmarks import evalplus as mod

    monkeypatch.setattr(
        mod,
        "_load_problems",
        lambda dataset: {
            f"HumanEval/{i}": {
                "prompt": "x",
                "entry_point": "f",
                "canonical_solution": "pass",
            }
            for i in range(5)
        },
    )
    monkeypatch.setattr(
        mod,
        "_score",
        lambda **kw: mod.EvalPlusStats(dataset="humaneval", num_instances=5),
    )

    results = mod.run(FakeDriver(), output_dir=str(tmp_path))
    assert len(results) == 5  # no limit -> all problems


# ---------------------------------------------------------------------------
# swebench.run(): builds SWEBenchRunner, returns run_all() results
# ---------------------------------------------------------------------------


def test_swebench_run_returns_eval_result_list(tmp_path: Path) -> None:
    from eval.benchmarks import swebench as mod

    results = mod.run(
        FakeDriver(),
        limit=1,
        dry_run=True,  # synthetic instances only; no Docker / network
        output_dir=str(tmp_path),
    )
    assert len(results) == 1
    assert isinstance(results[0], EvalResult)
    assert results[0].instance_id.startswith("synthetic__")

    predictions = tmp_path / "predictions.jsonl"
    assert predictions.exists()
    entry = json.loads(predictions.read_text(encoding="utf-8").splitlines()[0])
    assert entry["instance_id"] == results[0].instance_id
    assert "model_patch" in entry


# ---------------------------------------------------------------------------
# tau2bench / terminalbench run(): delegate to official runner
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    ("name", "env_var"),
    [("tau2bench", "TAU2_DATA_DIR"), ("terminalbench", "TERMINALBENCH_DATA_DIR")],
)
def test_runner_raises_when_data_dir_missing(name: str, env_var: str, monkeypatch) -> None:
    mod = importlib.import_module(f"eval.benchmarks.{name}")
    monkeypatch.delenv(env_var, raising=False)
    with pytest.raises(RuntimeError) as exc:
        mod.run(FakeDriver(), limit=1)
    assert "cannot run" in str(exc.value)
    assert env_var in str(exc.value)


def test_tau2_run_delegates_to_official_runner(tmp_path: Path, monkeypatch) -> None:
    from eval.benchmarks import tau2bench as mod

    data_dir = tmp_path / "data"
    data_dir.mkdir()
    (data_dir / "airline.jsonl").write_text(
        "\n".join(
            json.dumps({"id": i, "user": f"task {i}"}) for i in range(2)
        )
        + "\n",
        encoding="utf-8",
    )

    invoked: dict = {}

    def fake_tau_run(config, tasks, output_dir, model_name, num_trials, max_concurrency, task_split):
        invoked["config"] = config
        invoked["model"] = model_name
        from eval.adapter import EvalResult
        return [EvalResult(instance_id="0", error="")]

    monkeypatch.setattr(mod, "_can_score_official", lambda: (True, "mock"))
    monkeypatch.setattr(mod, "_run_with_config", fake_tau_run)

    results = mod.run(FakeDriver(), limit=1, data_dir=str(data_dir), output_dir=str(tmp_path))
    assert len(results) == 1
    assert results[0].instance_id == "0"
    assert invoked["config"].env_name == "airline"
    assert invoked["model"] == "deepseek-v4"


def test_terminalbench_run_delegates_to_real_api(tmp_path: Path, monkeypatch) -> None:
    from eval.benchmarks import terminalbench as mod

    data_dir = tmp_path / "data"
    data_dir.mkdir()
    (data_dir / "bash.jsonl").write_text(
        json.dumps({"name": "task-0", "description": "do the thing"}) + "\n",
        encoding="utf-8",
    )

    invoked: dict = {}

    def fake_harness_run(config, tasks, output_dir):
        invoked["tasks"] = tasks
        invoked["output_dir"] = output_dir
        from eval.adapter import EvalResult
        return [EvalResult(instance_id="task-0", error="")]

    monkeypatch.setattr(mod, "_can_score_official", lambda: (True, "mock"))
    monkeypatch.setattr(mod, "_run_with_harness", fake_harness_run)

    results = mod.run(FakeDriver(), data_dir=str(data_dir), output_dir=str(tmp_path))
    assert len(results) == 1
    assert results[0].instance_id == "task-0"
    assert invoked["tasks"][0]["name"] == "task-0"


# ---------------------------------------------------------------------------
# eval.run CLI: -b lists the four benchmarks
# ---------------------------------------------------------------------------


def test_run_cli_dash_b_lists_four_benchmarks(capsys) -> None:
    from eval import run as run_mod

    assert run_mod.main(["-b", "list"]) == 0
    out = capsys.readouterr().out
    for name in FOUR_BENCHMARKS:
        assert name in out


def test_run_cli_dash_b_without_value_lists(capsys) -> None:
    from eval import run as run_mod

    assert run_mod.main(["-b"]) == 0
    out = capsys.readouterr().out
    for name in FOUR_BENCHMARKS:
        assert name in out


# ---------------------------------------------------------------------------
# driver_headless: api_key env precedence
# ---------------------------------------------------------------------------


def test_driver_api_key_env_wins_over_placeholder(monkeypatch) -> None:
    from eval.driver_headless import HeadlessDriver, create_driver

    monkeypatch.setenv("LOCAL_LLM_API_KEY", "env-key")
    assert HeadlessDriver().api_key == "env-key"  # env beats "ollama" placeholder
    assert HeadlessDriver(api_key="explicit").api_key == "explicit"  # explicit beats env
    assert create_driver().api_key == "env-key"
    assert create_driver(api_key="explicit").api_key == "explicit"

    monkeypatch.delenv("LOCAL_LLM_API_KEY")
    assert HeadlessDriver().api_key == "ollama"  # placeholder fallback
    assert create_driver().api_key == "ollama"


# ---------------------------------------------------------------------------
# HarnessRun integration tests — Phase 4 (AgentAdapter protocol)
# ---------------------------------------------------------------------------


def test_evalplus_through_harness_run(tmp_path: Path) -> None:
    """evalplus benchmark routed through HarnessRun produces full artifact tree."""
    from eval.harness import HarnessRun, Budget, RunArtifacts
    from eval.adapter import EvalInstance, EvalResult

    run_id = "test-harness-001"
    root = tmp_path / "eval_results" / run_id

    class FakeEvalPlusAdapter:
        """Minimal adapter: returns canned EvalResults for known instances."""
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            return EvalResult(
                instance_id=instance.instance_id,
                answer=f"def {instance.instance_id}(): pass",
                cost=0.005,
                tokens_in=150,
                tokens_out=80,
                wall_time_s=0.5,
                trace_id=f"trace-{instance.instance_id}",
            )

    adapter = FakeEvalPlusAdapter()
    artifacts = RunArtifacts(run_id=run_id, root=str(tmp_path / "eval_results"))
    harness = HarnessRun(
        run_id=run_id,
        artifacts=artifacts,
        budget=Budget(wall_clock_seconds=60, max_tokens=10_000),
        adapter=adapter,
    )

    instances = [
        EvalInstance(instance_id="HumanEval/0", task_description="def foo(): ..."),
        EvalInstance(instance_id="HumanEval/1", task_description="def bar(): ..."),
        EvalInstance(instance_id="HumanEval/2", task_description="def baz(): ..."),
    ]
    summary = harness.run(instances)

    # Assertions: full artifact tree exists
    assert summary["summary"]["ok"] == 3
    assert summary["summary"]["failed"] == 0
    # Check files exist
    assert (root / "instances.jsonl").exists()
    assert (root / "predictions.jsonl").exists()
    assert (root / "events.jsonl").exists()
    assert (root / "summary.json").exists()
    assert (root / "environment.txt").exists()
    # failures.jsonl must exist (empty is fine — or may not exist if no failures)
    # instances.jsonl has 3 lines
    instances_lines = root.joinpath("instances.jsonl").read_text(encoding="utf-8").strip().split("\n")
    assert len(instances_lines) == 3
    # predictions.jsonl has 3 lines with trace_id
    pred_lines = root.joinpath("predictions.jsonl").read_text(encoding="utf-8").strip().split("\n")
    assert len(pred_lines) == 3
    for line in pred_lines:
        pred = json.loads(line)
        assert "trace_id" in pred
        assert pred["trace_id"].startswith("trace-")

    # Manifest assertions (Task 3)
    assert (root / "run-manifest.json").exists(), "run-manifest.json must exist"
    manifest = json.loads(root.joinpath("run-manifest.json").read_text(encoding="utf-8"))
    assert manifest["run_id"] == run_id
    assert "synthetic" in manifest
    assert "budgets" in manifest
    assert manifest["budgets"]["wall_clock_seconds"] == 60
    assert manifest["budgets"]["max_tokens"] == 10_000
    assert manifest["network_policy"] == "disabled"


def test_harness_run_missing_instance_id_fail_closed(tmp_path: Path) -> None:
    """Empty instance_id is recorded as failure, not silently skipped."""
    from eval.harness import HarnessRun, Budget, RunArtifacts
    from eval.adapter import EvalInstance, EvalResult

    run_id = "test-fail-closed-001"
    root = tmp_path / "eval_results" / run_id

    class OkAdapter:
        def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
            return EvalResult(instance_id=instance.instance_id, answer="ok")

    adapter = OkAdapter()
    artifacts = RunArtifacts(run_id=run_id, root=str(tmp_path / "eval_results"))
    harness = HarnessRun(run_id=run_id, artifacts=artifacts, adapter=adapter)

    instances = [
        EvalInstance(instance_id="", task_description="bad — no id"),
        EvalInstance(instance_id="ok-1", task_description="good"),
    ]
    summary = harness.run(instances)
    assert summary["summary"]["ok"] == 1
    assert summary["summary"]["failed"] >= 1  # the empty-id instance
    # failure recorded
    failures = [json.loads(line) for line in root.joinpath("failures.jsonl").read_text(encoding="utf-8").strip().split("\n")]
    bad_ids = [f for f in failures if not f["instance_id"]]
    assert len(bad_ids) >= 1
