"""Pinned receipt boundary for real Terminal-Bench official runs.

The official ``terminal_bench.Harness`` remains responsible for execution and
scoring.  This module only validates the fixed public input and preserves its
raw output in the repository's canonical artifact shape.
"""

from __future__ import annotations

import hashlib
import json
import shutil
from dataclasses import dataclass
from pathlib import Path
from typing import Any


TERMINAL_BENCH_PACKAGE = "terminal-bench"


@dataclass(frozen=True)
class TerminalBenchOfficialConfig:
    dataset_root: Path
    dataset_sha256: str
    package_version: str
    model: str
    task_id: str
    max_concurrency: int = 1


class TerminalBenchOfficialRunner:
    """Validate and archive one fixed official Terminal-Bench result."""

    def __init__(self, config: TerminalBenchOfficialConfig) -> None:
        self.config = config

    @property
    def pins(self) -> dict[str, str]:
        return {
            "benchmark": "terminal-bench",
            "dataset_name": "terminal-bench-v2 offline public snapshot",
            "dataset_revision": self.config.dataset_sha256,
            "scorer_name": "terminal_bench.Harness results.json",
            "runner_version": self.config.package_version,
        }

    def validate(self) -> list[str]:
        problems: list[str] = []
        if not self.config.dataset_root.is_dir():
            problems.append("dataset_root does not exist")
        if len(self.config.dataset_sha256) != 64 or any(
            character not in "0123456789abcdef" for character in self.config.dataset_sha256
        ):
            problems.append("dataset_sha256 must be a lowercase 64-character SHA-256")
        if not self.config.package_version:
            problems.append("package_version is required")
        if "/" not in self.config.model:
            problems.append("model must include a LiteLLM provider prefix")
        if not self.config.task_id:
            problems.append("task_id is required")
        if not 1 <= self.config.max_concurrency <= 10:
            problems.append("max_concurrency must be between 1 and 10")
        return problems

    def collect_receipt(self, official_run_dir: Path, artifact_root: Path) -> dict[str, Any]:
        """Copy upstream raw JSON and summarize its official resolution.

        The run directory must have been produced by ``terminal_bench.Harness``.
        Missing or malformed output fails closed; no local scorer is used.
        """
        problems = self.validate()
        if problems:
            raise ValueError("; ".join(problems))
        raw_results = official_run_dir / "results.json"
        raw_metadata = official_run_dir / "run_metadata.json"
        if not raw_results.is_file() or not raw_metadata.is_file():
            raise FileNotFoundError("official Terminal-Bench results.json or run_metadata.json is missing")
        try:
            parsed = json.loads(raw_results.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            raise ValueError("official Terminal-Bench results.json is not valid JSON") from exc
        trials = parsed.get("results") if isinstance(parsed, dict) else None
        if not isinstance(trials, list):
            raise ValueError("official Terminal-Bench results.json has no results list")
        task_trials = [trial for trial in trials if isinstance(trial, dict) and trial.get("task_id") == self.config.task_id]
        if len(task_trials) != 1:
            raise ValueError("official Terminal-Bench receipt must contain exactly one configured task")

        scorer_dir = artifact_root / "scorer"
        scorer_dir.mkdir(parents=True, exist_ok=True)
        copied_results = scorer_dir / "terminalbench-results.json"
        copied_metadata = scorer_dir / "terminalbench-run-metadata.json"
        shutil.copyfile(raw_results, copied_results)
        shutil.copyfile(raw_metadata, copied_metadata)

        trial = task_trials[0]
        status = "OFFICIAL_PASS" if trial.get("is_resolved") is True else "OFFICIAL_FAILURE"
        receipt = {
            "benchmark": "terminal-bench",
            "package": TERMINAL_BENCH_PACKAGE,
            "package_version": self.config.package_version,
            "dataset_root": self.config.dataset_root.as_posix(),
            "dataset_sha256": self.config.dataset_sha256,
            "model": self.config.model,
            "task_ids": [self.config.task_id],
            "max_concurrency": self.config.max_concurrency,
            "status": status,
            "failure_mode": trial.get("failure_mode"),
            "official_output_sha256": _sha256(copied_results),
            "official_metadata_sha256": _sha256(copied_metadata),
            "official_output": copied_results.as_posix(),
        }
        (artifact_root / "receipt.json").write_text(
            json.dumps(receipt, indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
        )
        _write_checksums(artifact_root)
        return receipt


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _write_checksums(root: Path) -> None:
    entries = [
        f"{_sha256(path)}  {path.relative_to(root).as_posix()}"
        for path in sorted(root.rglob("*"))
        if path.is_file() and path.name != "checksums.sha256"
    ]
    (root / "checksums.sha256").write_text("\n".join(entries) + "\n", encoding="utf-8")
