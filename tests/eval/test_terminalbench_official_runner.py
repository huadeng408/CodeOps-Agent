"""Contract tests for the pinned Terminal-Bench official receipt boundary."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path


def _write_dataset(root: Path) -> None:
    task = root / "tasks" / "fixed-task"
    task.mkdir(parents=True)
    (root / "terminalbench_2.jsonl").write_text(
        '{"name": "fixed-task", "description": "fixture"}\n', encoding="utf-8"
    )


def _write_official_run(root: Path, *, resolved: bool) -> None:
    root.mkdir(parents=True)
    (root / "results.json").write_text(
        json.dumps({"results": [{"task_id": "fixed-task", "is_resolved": resolved}]}),
        encoding="utf-8",
    )
    (root / "run_metadata.json").write_text(
        json.dumps({"agent_name": "fixture-agent", "n_concurrent_trials": 1}),
        encoding="utf-8",
    )


def test_terminalbench_official_runner_copies_raw_results_and_marks_failure(tmp_path: Path) -> None:
    from eval.benchmarks.terminalbenchofficial import (
        TerminalBenchOfficialConfig,
        TerminalBenchOfficialRunner,
    )

    dataset = tmp_path / "dataset"
    _write_dataset(dataset)
    source_hash = hashlib.sha256((dataset / "terminalbench_2.jsonl").read_bytes()).hexdigest()
    raw_run = tmp_path / "upstream-run"
    _write_official_run(raw_run, resolved=False)
    runner = TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=dataset,
            dataset_sha256=source_hash,
            package_version="0.2.18",
            model="openai/gpt-5.6-sol",
            task_id="fixed-task",
        )
    )

    receipt = runner.collect_receipt(raw_run, tmp_path / "receipt")

    copied = tmp_path / "receipt" / "scorer" / "terminalbench-results.json"
    assert copied.read_bytes() == (raw_run / "results.json").read_bytes()
    assert receipt["status"] == "OFFICIAL_FAILURE"
    assert receipt["task_ids"] == ["fixed-task"]
    assert receipt["official_output_sha256"] == hashlib.sha256(copied.read_bytes()).hexdigest()
    assert (tmp_path / "receipt" / "checksums.sha256").is_file()


def test_terminalbench_official_runner_rejects_invalid_model_and_concurrency(tmp_path: Path) -> None:
    from eval.benchmarks.terminalbenchofficial import (
        TerminalBenchOfficialConfig,
        TerminalBenchOfficialRunner,
    )

    runner = TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=tmp_path,
            dataset_sha256="a" * 64,
            package_version="0.2.18",
            model="gpt-5.6-sol",
            task_id="fixed-task",
            max_concurrency=11,
        )
    )

    assert runner.validate() == [
        "model must include a LiteLLM provider prefix",
        "max_concurrency must be between 1 and 10",
    ]
