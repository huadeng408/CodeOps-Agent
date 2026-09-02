"""The uplift must reach the agent by the path the runner actually takes.

Defect 22. Every earlier test called ``_augment_for_uplift`` directly, or called
``SWEBenchAdapter.solve()``, and they all passed. Meanwhile ``HarnessRun`` calls
``solve_instance()`` on the *driver* and uses the benchmark only for ``prepare``
and ``score`` -- so ``solve()`` is never invoked on the runner path, and the
augmentation placed there ran for nobody.

The first arm B run produced a complete, well-formed artifact tree with zero
localization records and no grading contract in any prompt: a second baseline
wearing the optimized arm's directory name. Nothing raised, and the numbers
would have supported a comparison that measured run-to-run noise.

So these tests do not check that the augmentation works -- other files do that.
They check that it is *wired to the code path under test*, which is the thing a
unit test of the augmentation cannot see.
"""

from __future__ import annotations

import inspect
from pathlib import Path

import pytest

from eval.adapter import EvalInstance, EvalResult
from eval.benchmarks.swebench import SWEBenchAdapter
from eval.harness.runner import HarnessRun
from eval.harness.uplift import UPLIFT_ENV
from eval.run import _OfficialBenchmarkDriver

TASK = "Separability matrix is wrong for nested CompoundModels"


def _instance() -> EvalInstance:
    return EvalInstance(
        instance_id="astropy__astropy-12907",
        task_description=TASK,
        metadata={"repo": "astropy/astropy", "base_commit": "deadbeef", "synthetic": True},
    )


# --------------------------------------------------- the wiring, structurally


def test_runner_calls_solve_instance_not_solve():
    """Pin the fact that made defect 22 possible, so it cannot drift back.

    If the runner ever starts calling ``benchmark.solve()``, this test fails and
    whoever changes it has to decide where the augmentation belongs, rather than
    discovering months later that it stopped running.
    """
    source = inspect.getsource(HarnessRun._execute_instance)
    assert "adapter.solve_instance(" in source
    assert "adapter.solve(" not in source


def test_benchmark_solve_is_not_on_the_runner_path():
    """SWEBenchAdapter.solve exists for the standalone CLI, not for the runner."""
    source = inspect.getsource(HarnessRun._execute_instance)
    assert "self.benchmark" not in source


def test_prepare_is_the_hook_the_runner_uses():
    source = inspect.getsource(HarnessRun._execute_instance)
    assert "self.setup_workspace" in source


def test_prepare_performs_the_augmentation():
    """The augmentation must live in prepare, which the runner does call."""
    source = inspect.getsource(SWEBenchAdapter.prepare)
    assert "_augment_for_uplift" in source


# ------------------------------------------------------ the wiring, in effect


@pytest.fixture()
def synthetic_repo(tmp_path: Path) -> Path:
    """A repo shaped enough for localization to rank something in it."""
    pkg = tmp_path / "astropy" / "modeling"
    pkg.mkdir(parents=True)
    (pkg / "separable.py").write_text(
        '"""Separability matrix computation for compound models."""\n'
        "\n"
        "def separability_matrix(transform):\n"
        '    """Compute the separability matrix of a CompoundModel."""\n'
        "    return _coord_matrix(transform)\n",
        encoding="utf-8",
    )
    (tmp_path / "setup.py").write_text("# nothing\n", encoding="utf-8")
    return tmp_path


def test_prepare_augments_the_prompt_the_agent_will_see(
    synthetic_repo: Path, monkeypatch
):
    """Arm B: prepare must leave the augmented text on the runner's own object.

    The runner ignores prepare's return value and hands *its* reference to the
    driver, so an augmentation that returns a copy is an augmentation nobody
    reads. This asserts against the caller's instance for that reason.
    """
    monkeypatch.setenv(UPLIFT_ENV, "1")
    instance = _instance()
    adapter = SWEBenchAdapter()
    adapter.prepare(instance, synthetic_repo)

    assert TASK in instance.task_description, "original problem text must survive"
    assert "How this task is graded" in instance.task_description
    assert instance.metadata.get("localization"), "no ranking recorded"


def test_prepare_leaves_the_baseline_prompt_untouched(
    synthetic_repo: Path, monkeypatch
):
    monkeypatch.delenv(UPLIFT_ENV, raising=False)
    instance = _instance()
    SWEBenchAdapter().prepare(instance, synthetic_repo)

    assert instance.task_description == TASK
    assert "localization" not in instance.metadata


def test_prepare_records_the_ranking_for_attribution(
    synthetic_repo: Path, monkeypatch
):
    monkeypatch.setenv(UPLIFT_ENV, "1")
    instance = _instance()
    SWEBenchAdapter().prepare(instance, synthetic_repo)

    record = instance.metadata["localization"]
    assert any("separable.py" in path for path in record["files"])


def test_augmentation_failure_does_not_break_prepare(tmp_path: Path, monkeypatch):
    """prepare's real job is the worktree; the uplift is an accelerator."""
    monkeypatch.setenv(UPLIFT_ENV, "1")
    (tmp_path / "README.md").write_text("no python here", encoding="utf-8")
    instance = _instance()
    SWEBenchAdapter().prepare(instance, tmp_path)
    assert TASK in instance.task_description


def test_tool_rounds_reads_the_uplift_on_the_driver_path(monkeypatch):
    """The turn budget was already wired to the driver, unlike the prompt.

    Worth pinning explicitly: this asymmetry is what made the void arm B a
    *partial* arm rather than an exact baseline -- 24 rounds with a baseline
    prompt -- which is harder to spot than no change at all.
    """
    import eval.driver_headless as D

    source = inspect.getsource(D.HeadlessDriver)
    assert "uplift_config().tool_rounds" in source


def test_official_driver_does_not_augment_prepared_prompt_twice(
    synthetic_repo: Path, monkeypatch
):
    """The prepare -> bridge -> solve path must add the grading contract once."""
    monkeypatch.setenv(UPLIFT_ENV, "1")
    instance = _instance()
    benchmark = SWEBenchAdapter()
    benchmark.prepare(instance, synthetic_repo)

    class RecordingDriver:
        def __init__(self) -> None:
            self.task_description = ""

        def solve_instance(self, observed, working_dir, **kwargs):
            self.task_description = observed.task_description
            return EvalResult(instance_id=observed.instance_id, model_patch="")

    driver = RecordingDriver()
    bridge = _OfficialBenchmarkDriver(benchmark, driver)
    bridge.solve_instance(instance, str(synthetic_repo))

    assert driver.task_description.count("## How this task is graded") == 1
