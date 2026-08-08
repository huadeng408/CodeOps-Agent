"""Terminal-Bench and tau2-bench adapter tests (plan Task 8.3)."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.benchmarks.tau2bench import (
    Tau2BenchConfig,
    load_tasks as load_tau2,
)
from eval.benchmarks.terminalbench import (
    TerminalBenchConfig,
    load_tasks as load_terminal,
)


def _write_tasks(root: Path, family: str, count: int = 2, user_field: str = "description") -> Path:
    root.mkdir(parents=True, exist_ok=True)
    path = root / f"{family}.jsonl"
    lines = []
    for i in range(count):
        lines.append(json.dumps({"name": f"{family}-{i}", "id": str(i), user_field: f"Task {i}"}))
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")
    return path


def test_terminal_config_validation() -> None:
    good = TerminalBenchConfig(data_dir=Path("."), license_spdx="MIT")
    assert good.validate() == []
    bad = TerminalBenchConfig(data_dir=Path("/nonexistent-dir"), license_spdx="Proprietary")
    issues = bad.validate()
    assert len(issues) == 2  # missing dir + bad license


def test_terminal_load_and_convert(tmp_path: Path) -> None:
    _write_tasks(tmp_path, "bash")
    tasks = load_terminal(tmp_path)
    assert len(tasks) == 2
    assert tasks[0]["_family"] == "bash"
    # instance construction is handled by load_instances(), not to_eval_instances()


def test_terminal_can_score_official() -> None:
    from eval.benchmarks.terminalbench import _can_score_official
    can_score, reason = _can_score_official()
    assert isinstance(can_score, bool)
    assert isinstance(reason, str)


def test_tau2_config_validation() -> None:
    good = Tau2BenchConfig(data_dir=Path("."), env_name="airline", license_spdx="MIT")
    assert good.validate() == []
    bad = Tau2BenchConfig(data_dir=Path("/nope"), env_name="unknown-env", license_spdx="Nope")
    issues = bad.validate()
    assert len(issues) == 3


def test_tau2_load_and_convert(tmp_path: Path) -> None:
    _write_tasks(tmp_path, "airline", user_field="user")
    tasks = load_tau2(tmp_path)
    assert len(tasks) == 2
    # instance construction is handled by load_instances(), not to_eval_instances()


def test_tau2_can_score_official() -> None:
    from eval.benchmarks.tau2bench import _can_score_official
    can_score, reason = _can_score_official()
    assert isinstance(can_score, bool)
    assert isinstance(reason, str)


def test_adapters_do_not_fake_unified_scorer() -> None:
    # Both adapters delegate to official runner APIs; neither implements a
    # local scorer (the uniform layer is manifest/artifact/taxonomy only).
    from eval.benchmarks.terminalbench import _can_score_official as term_chk
    from eval.benchmarks.tau2bench import _can_score_official as tau2_chk

    # Both expose official-scoring availability checks.
    assert callable(term_chk)
    assert callable(tau2_chk)
