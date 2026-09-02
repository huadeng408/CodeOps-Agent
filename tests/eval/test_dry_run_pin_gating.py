"""``--dry-run`` must be gated by the H0/H3 pin preflight.

The dry-run branch returned *before* ``_validate_benchmark_pins()`` ran, so it
printed::

    dry-run OK: manifest validated, no instances will be solved

and exited 0 for a benchmark whose dataset/scorer pins were missing entirely.
It had validated nothing: no manifest was built on that path and no pin was
read.  A preflight that cannot fail is not a preflight, and here it actively
misreported -- the one command a person runs to check "am I set up correctly"
was the command that could not detect the setup being wrong.

The reproducibility contract requires an unpinned run to fail before an instance is
solved.  ``--dry-run`` is the cheapest way to reach that check, so it must
enforce it rather than skip it.

Pure CLI-level assertions: no Docker, no network, no model.
"""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

import eval.run as run_mod  # noqa: E402


class _UnpinnedBench:
    """An adapter whose reproducibility pins are missing."""

    def validate_pins(self):
        return ["dataset_revision", "scorer_name"]

    def prepare(self, *a, **k):  # pragma: no cover - must never be reached
        raise AssertionError("prepare() must not run during a dry run")

    def score(self, *a, **k):  # pragma: no cover - must never be reached
        raise AssertionError("score() must not run during a dry run")


class _PinnedBench:
    """A fully pinned adapter."""

    def validate_pins(self):
        return []

    def prepare(self, *a, **k):  # pragma: no cover - must never be reached
        raise AssertionError("prepare() must not run during a dry run")

    def score(self, *a, **k):  # pragma: no cover - must never be reached
        raise AssertionError("score() must not run during a dry run")


@pytest.fixture
def no_driver(monkeypatch):
    """Fail loudly if a dry run tries to build a driver (would need a model)."""
    import eval.driver_headless as drv

    def explode(*a, **k):
        raise AssertionError("create_driver() must not run during a dry run")

    monkeypatch.setattr(drv, "create_driver", explode)
    return explode


ARGV = ["--benchmark", "swebench", "--model", "stub-model", "--dry-run", "--limit", "1"]


def test_dry_run_fails_when_pins_are_missing(monkeypatch, capsys, tmp_path, no_driver):
    monkeypatch.setattr(run_mod, "_find_agent_benchmark", lambda mod: _UnpinnedBench())
    rc = run_mod.main([*ARGV, "--output-dir", str(tmp_path)])
    err = capsys.readouterr().err
    assert rc == 1, "an unpinned benchmark must not report a successful dry run"
    assert "incomplete reproducibility" in err
    assert "dataset_revision" in err and "scorer_name" in err


def test_dry_run_does_not_claim_success_when_pins_are_missing(
    monkeypatch, capsys, tmp_path, no_driver
):
    """The exact regression: a success line printed for an unpinned benchmark."""
    monkeypatch.setattr(run_mod, "_find_agent_benchmark", lambda mod: _UnpinnedBench())
    run_mod.main([*ARGV, "--output-dir", str(tmp_path)])
    out = capsys.readouterr().out
    assert "dry-run OK" not in out
    assert "validated" not in out


def test_dry_run_succeeds_when_pins_are_complete(monkeypatch, capsys, tmp_path, no_driver):
    monkeypatch.setattr(run_mod, "_find_agent_benchmark", lambda mod: _PinnedBench())
    rc = run_mod.main([*ARGV, "--output-dir", str(tmp_path)])
    out = capsys.readouterr().out
    assert rc == 0
    assert "dry-run OK: pins validated" in out


def test_dry_run_solves_no_instances(monkeypatch, capsys, tmp_path, no_driver):
    """The fixtures assert it: prepare/score/create_driver all raise if reached."""
    monkeypatch.setattr(run_mod, "_find_agent_benchmark", lambda mod: _PinnedBench())
    rc = run_mod.main([*ARGV, "--output-dir", str(tmp_path)])
    assert rc == 0
    assert "no instances will be solved" in capsys.readouterr().out


def test_missing_validate_pins_contract_also_blocks_dry_run(
    monkeypatch, capsys, tmp_path, no_driver
):
    """An adapter predating the pin contract must not pass a dry run either."""

    class Legacy:
        pass

    monkeypatch.setattr(run_mod, "_find_agent_benchmark", lambda mod: Legacy())
    rc = run_mod.main([*ARGV, "--output-dir", str(tmp_path)])
    assert rc == 1
    assert "validate_pins" in capsys.readouterr().err


def test_real_swebench_adapter_passes_the_dry_run_gate(capsys, tmp_path, no_driver):
    """Not a stub: the shipped adapter must actually satisfy its own pins."""
    rc = run_mod.main([*ARGV, "--output-dir", str(tmp_path)])
    assert rc == 0, "the shipped SWE-bench adapter should be fully pinned"
    assert "dry-run OK: pins validated" in capsys.readouterr().out


@pytest.mark.parametrize("benchmark", ["beir", "miracl", "bright"])
def test_retrieval_dry_run_reaches_dataset_preflight(
    benchmark, capsys, tmp_path, no_driver
):
    rc = run_mod.main(
        [
            "--benchmark",
            benchmark,
            "--dry-run",
            "--cache-dir",
            str(tmp_path / "cache"),
            "--output-dir",
            str(tmp_path / "out"),
        ]
    )
    captured = capsys.readouterr()
    assert rc == 1
    assert "has no load_instances" not in captured.err
    assert "not pinned" in captured.err or "artifact missing" in captured.err
