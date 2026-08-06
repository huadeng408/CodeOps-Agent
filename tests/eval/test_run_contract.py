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

    def fake_command(config, task_file: Path, output_dir: Path) -> list[str]:
        invoked["task_file"] = task_file
        return ["python", "-m", "tau_bench.eval", "--env", config.env_name]

    def fake_run(cmd, **kw):
        invoked["cmd"] = cmd
        invoked["kw"] = kw

        class Proc:
            returncode = 0
            stdout = ""
            stderr = ""

        return Proc()

    monkeypatch.setattr(mod, "official_runner_command", fake_command)
    monkeypatch.setattr(mod.subprocess, "run", fake_run)

    results = mod.run(FakeDriver(), limit=1, data_dir=str(data_dir), output_dir=str(tmp_path))
    assert len(results) == 1
    assert results[0].instance_id == "0"
    assert invoked["cmd"][:3] == ["python", "-m", "tau_bench.eval"]
    assert invoked["task_file"].exists()

    summary = json.loads((tmp_path / "tau2bench_summary.json").read_text(encoding="utf-8"))
    assert summary["domain"] == "airline"
    assert summary["num_tasks"] == 1
    assert "api_key" not in summary and "key" not in str(summary.values()).lower()


def test_terminalbench_run_delegates_to_official_runner(tmp_path: Path, monkeypatch) -> None:
    from eval.benchmarks import terminalbench as mod

    data_dir = tmp_path / "data"
    data_dir.mkdir()
    (data_dir / "bash.jsonl").write_text(
        json.dumps({"name": "task-0", "description": "do the thing"}) + "\n",
        encoding="utf-8",
    )

    invoked: dict = {}

    def fake_command(config, task_file: Path, output_dir: Path) -> list[str]:
        invoked["task_file"] = task_file
        return ["python", "-m", "terminal_bench.eval", "--image", config.image_name]

    def fake_run(cmd, **kw):
        invoked["cmd"] = cmd

        class Proc:
            returncode = 0
            stdout = ""
            stderr = ""

        return Proc()

    monkeypatch.setattr(mod, "official_runner_command", fake_command)
    monkeypatch.setattr(mod.subprocess, "run", fake_run)

    results = mod.run(FakeDriver(), data_dir=str(data_dir), output_dir=str(tmp_path))
    assert len(results) == 1
    assert results[0].instance_id == "task-0"
    assert invoked["cmd"][:3] == ["python", "-m", "terminal_bench.eval"]

    summary = json.loads((tmp_path / "terminalbench_summary.json").read_text(encoding="utf-8"))
    assert summary["num_tasks"] == 1
    assert summary["official_runner"] == "terminal_bench.eval"


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
