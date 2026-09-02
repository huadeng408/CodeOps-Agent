"""The official scorer must never fail silently into a scored result (H5).

``SWEBenchAdapter.score`` returned ``{"resolved": False, "scorer_status":
"failed: ..."}`` when the official scorer could not run at all.  The harness
merges that dict into the prediction and counts the instance as **completed**,
so a run in which the official scorer never executed reported ``ok: 1,
failed: 0`` and exited 0.

That collapses two very different facts into one indistinguishable output:

* the official scorer ran and the agent's patch did not fix the bug
  (a real, publishable measurement: ``resolved=False``), versus
* the official scorer never ran — missing Docker, dead proxy, WSL failure —
  so nothing whatsoever was measured.

``tests/integration/test_unified_harness_official_paths.py`` already proves
ERROR_SCORER is reachable *with a mocked crashing scorer*.  These tests cover
the production path: the REAL swebench scorer must raise on infrastructure
failure so that classification actually fires, while a genuine unresolved
verdict from a scorer that really ran must still succeed.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest

from eval.adapter import EvalInstance, EvalResult


def _instance(instance_id: str = "astropy__astropy-12907") -> EvalInstance:
    return EvalInstance(
        instance_id=instance_id,
        task_description="issue text",
        metadata={"repo": "astropy/astropy", "base_commit": "d16bfe0"},
    )


def _result(
    patch: str = "diff --git a/x b/x\n",
    instance_id: str = "astropy__astropy-12907",
) -> EvalResult:
    return EvalResult(instance_id=instance_id, model_patch=patch)


# ---------------------------------------------------------------------------
# Infrastructure failure must RAISE, not return a scored dict
# ---------------------------------------------------------------------------


def test_score_raises_when_official_scorer_unavailable(
    tmp_path: Path, monkeypatch
) -> None:
    """Scorer unavailable (no Docker/WSL) is an infra fault, not a verdict."""
    from eval.benchmarks import swebench as mod

    monkeypatch.setattr(
        mod, "_can_score_official", lambda: (False, "Docker not available in WSL")
    )

    adapter = mod.SWEBenchAdapter()
    with pytest.raises(mod.OfficialScorerUnavailable) as exc:
        adapter.score(_result(), _instance(), tmp_path)

    assert "Docker not available in WSL" in str(exc.value)


def test_score_raises_when_official_scoring_subprocess_fails(
    tmp_path: Path, monkeypatch
) -> None:
    """A scorer that ran and errored measured nothing; it must not report."""
    from eval.benchmarks import swebench as mod

    monkeypatch.setattr(mod, "_can_score_official", lambda: (True, "available"))
    monkeypatch.setattr(
        mod,
        "_run_official_scoring",
        lambda *a, **k: (False, "WSL2 scoring failed (exit 1): ConnectionError"),
    )

    adapter = mod.SWEBenchAdapter()
    with pytest.raises(mod.OfficialScorerUnavailable) as exc:
        adapter.score(_result(), _instance(), tmp_path)

    assert "ConnectionError" in str(exc.value)


def test_score_raises_when_report_missing_after_successful_run(
    tmp_path: Path, monkeypatch
) -> None:
    """No report means no verdict — inconclusive must never read as unresolved."""
    from eval.benchmarks import swebench as mod

    monkeypatch.setattr(mod, "_can_score_official", lambda: (True, "available"))
    monkeypatch.setattr(mod, "_run_official_scoring", lambda *a, **k: (True, "done"))
    monkeypatch.setattr(mod, "_read_official_resolution", lambda **k: None)

    adapter = mod.SWEBenchAdapter()
    with pytest.raises(mod.OfficialScorerUnavailable):
        adapter.score(_result(), _instance(), tmp_path)


# ---------------------------------------------------------------------------
# A scorer that really ran must still report its verdict, either way
# ---------------------------------------------------------------------------


@pytest.mark.parametrize("resolved", [True, False])
def test_score_returns_real_verdict_when_scorer_ran(
    tmp_path: Path, monkeypatch, resolved: bool
) -> None:
    """resolved=False from a scorer that ran is a MEASUREMENT, not a failure."""
    from eval.benchmarks import swebench as mod

    monkeypatch.setattr(mod, "_can_score_official", lambda: (True, "available"))
    monkeypatch.setattr(mod, "_run_official_scoring", lambda *a, **k: (True, "done"))
    monkeypatch.setattr(mod, "_read_official_resolution", lambda **k: resolved)

    adapter = mod.SWEBenchAdapter()
    out = adapter.score(_result(), _instance(), tmp_path)

    assert out["resolved"] is resolved
    assert out["scorer_status"].startswith("official:")
    # predictions.jsonl for the instance is still written next to the workspace
    assert (tmp_path / "predictions.jsonl").exists()


def test_score_passes_harness_deadline_to_official_scorer(
    tmp_path: Path, monkeypatch
) -> None:
    """SWE-bench cannot replace the Harness deadline with its 3600s default."""
    from eval.benchmarks import swebench as mod

    seen: dict[str, float] = {}

    def run_official(*args, **kwargs):
        del args
        seen["timeout"] = float(kwargs["timeout"])
        return True, "done"

    monkeypatch.setattr(mod, "_can_score_official", lambda: (True, "available"))
    monkeypatch.setattr(mod, "_run_official_scoring", run_official)
    monkeypatch.setattr(mod, "_read_official_resolution", lambda **k: False)

    adapter = mod.SWEBenchAdapter()
    out = adapter.score(_result(), _instance(), tmp_path, timeout_s=12.5)

    assert out["resolved"] is False
    assert seen["timeout"] == 12.5


def test_score_reuses_official_capability_probe_for_one_adapter(
    tmp_path: Path, monkeypatch
) -> None:
    """A multi-instance run must not pay the WSL probe timeout per instance."""
    from eval.benchmarks import swebench as mod

    calls = 0

    def unavailable_probe():
        nonlocal calls
        calls += 1
        return False, "probe unavailable"

    monkeypatch.setattr(mod, "_can_score_official", unavailable_probe)
    adapter = mod.SWEBenchAdapter()

    for instance_id in ("case-1", "case-2", "case-3"):
        with pytest.raises(mod.OfficialScorerUnavailable, match="probe unavailable"):
            adapter.score(_result(instance_id), _instance(instance_id), tmp_path)

    assert calls == 1


# ---------------------------------------------------------------------------
# End-to-end through the harness: infra fault -> ERROR_SCORER, non-zero exit
# ---------------------------------------------------------------------------


def test_harness_classifies_unavailable_scorer_as_error_scorer(
    tmp_path: Path, monkeypatch
) -> None:
    """The production scorer path must make ERROR_SCORER reachable."""
    from eval.benchmarks import swebench as mod
    from eval.harness import Budget, HarnessRun, RunArtifacts

    monkeypatch.setattr(
        mod, "_can_score_official", lambda: (False, "Docker not available in WSL")
    )

    class Adapter:
        def solve_instance(
            self, instance: EvalInstance, working_dir: str, **kwargs: Any
        ) -> EvalResult:
            return EvalResult(
                instance_id=instance.instance_id, model_patch="diff --git a/x b/x\n"
            )

    run_id = "test-scorer-infra"
    root = tmp_path / run_id
    harness = HarnessRun(
        run_id=run_id,
        artifacts=RunArtifacts(run_id=run_id, root=str(tmp_path)),
        budget=Budget(wall_clock_seconds=60, max_tokens=10_000),
        adapter=Adapter(),
        scorer=mod.SWEBenchAdapter().score,
        config={
            "git_sha": "a1b2c3d",
            "dirty_hash": "0" * 64,
            "model": "test-model",
            "prompt_hash": "0" * 64,
        },
    )

    summary = harness.run([_instance()])["summary"]

    assert summary["ok"] == 0, "a run whose scorer never executed is not ok"
    assert summary["by_category"]["scorer"] == 1
    assert summary["failed"] >= 1

    failures = root.joinpath("failures.jsonl").read_text(encoding="utf-8")
    assert "astropy__astropy-12907" in failures
    assert "Docker not available in WSL" in failures
