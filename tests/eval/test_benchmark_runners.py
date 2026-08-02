"""Terminal-Bench and tau2-bench adapter tests (plan Task 8.3)."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.benchmarks.tau2bench import (
    Tau2BenchConfig,
    load_tasks as load_tau2,
    official_runner_command as tau2_command,
    to_eval_instances as tau2_instances,
)
from eval.benchmarks.terminalbench import (
    TerminalBenchConfig,
    load_tasks as load_terminal,
    official_runner_command as terminal_command,
    to_eval_instances as terminal_instances,
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
    good = TerminalBenchConfig(data_dir=Path("."), image_name="terminal-bench", license_spdx="MIT")
    assert good.validate() == []
    bad = TerminalBenchConfig(data_dir=Path("/nonexistent-dir"), image_name="", license_spdx="Proprietary")
    issues = bad.validate()
    assert len(issues) == 3


def test_terminal_load_and_convert(tmp_path: Path) -> None:
    _write_tasks(tmp_path, "bash")
    tasks = load_terminal(tmp_path)
    assert len(tasks) == 2
    assert tasks[0]["_family"] == "bash"
    instances = terminal_instances(tasks)
    assert instances[0]["instance_id"] == "bash/bash-0"
    assert instances[0]["task_description"] == "Task 0"


def test_terminal_official_runner_command(tmp_path: Path) -> None:
    config = TerminalBenchConfig(data_dir=tmp_path, image_name="terminal-bench")
    cmd = terminal_command(config, tmp_path / "bash.jsonl", tmp_path / "out")
    assert cmd[0] == "python"
    assert "terminal_bench.eval" in cmd
    assert "--image" in cmd and "terminal-bench" in cmd


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
    instances = tau2_instances(tasks)
    # tau2 tasks are keyed by numeric id and carry a "user" field.
    assert instances[0]["instance_id"] == "airline/0"
    assert instances[0]["task_description"] == "Task 0"


def test_tau2_official_runner_command(tmp_path: Path) -> None:
    config = Tau2BenchConfig(data_dir=tmp_path, env_name="retail")
    cmd = tau2_command(config, tmp_path / "retail.jsonl", tmp_path / "out")
    assert "tau_bench.eval" in cmd
    assert "--env" in cmd and "retail" in cmd


def test_adapters_do_not_fake_unified_scorer() -> None:
    # Both adapters delegate to official runners; neither implements a
    # local scorer (the uniform layer is manifest/artifact/taxonomy only).
    from eval.benchmarks.terminalbench import OFFICIAL_RUNNER_MODULE as term_mod
    from eval.benchmarks.tau2bench import OFFICIAL_RUNNER_MODULE as tau2_mod

    assert term_mod.startswith("terminal_bench")
    assert tau2_mod.startswith("tau_bench")
