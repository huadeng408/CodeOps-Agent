"""Pinned CLI boundary for official tau2-bench text smoke runs.

This module intentionally does not replace the historical ``tau_bench``
adapter.  It wraps a separately checked out, pinned tau2-bench release and
preserves its raw result bytes as the official evidence.
"""

from __future__ import annotations

import hashlib
import json
import shutil
from dataclasses import dataclass
from pathlib import Path
from typing import Any


TAU2_V101_COMMIT = "fc0055dc4e0a316c3f83133267fbd6faaa770992"
TAU2_V101_TAG = "v1.0.1"
TAU2_REPOSITORY = "https://github.com/sierra-research/tau2-bench"


@dataclass(frozen=True)
class Tau2OfficialConfig:
    checkout: Path
    source_commit: str
    data_tree_sha256: str
    model: str
    seed: int = 42
    max_concurrency: int = 1
    domain: str = "mock"


class Tau2OfficialRunner:
    """Create commands and receipts for a fixed official tau2 CLI release."""

    def __init__(self, config: Tau2OfficialConfig) -> None:
        self.config = config

    @property
    def pins(self) -> dict[str, str]:
        return {
            "benchmark": "tau2-bench",
            "dataset_name": "tau2-bench-v1.0.1-bundled-data",
            "dataset_revision": self.config.source_commit,
            "scorer_name": "tau2 CLI results.json",
        }

    def validate(self) -> list[str]:
        problems: list[str] = []
        if not self.config.checkout.is_dir():
            problems.append("checkout does not exist")
        if len(self.config.source_commit) != 40 or any(c not in "0123456789abcdef" for c in self.config.source_commit):
            problems.append("source_commit must be a lowercase 40-character SHA-1")
        if len(self.config.data_tree_sha256) != 64 or any(c not in "0123456789abcdef" for c in self.config.data_tree_sha256):
            problems.append("data_tree_sha256 must be a lowercase 64-character SHA-256")
        if "/" not in self.config.model:
            problems.append("model must include a LiteLLM provider prefix")
        if not 1 <= self.config.max_concurrency <= 10:
            problems.append("max_concurrency must be between 1 and 10")
        if self.config.domain != "mock":
            problems.append("official smoke domain must be mock")
        return problems

    def command(self, run_name: str) -> list[str]:
        problems = self.validate()
        if problems:
            raise ValueError("; ".join(problems))
        return [
            "uv", "run", "tau2", "run", "--domain", self.config.domain,
            "--agent-llm", self.config.model, "--user-llm", self.config.model,
            "--num-trials", "1", "--num-tasks", "1",
            "--max-concurrency", str(self.config.max_concurrency),
            "--seed", str(self.config.seed), "--save-to", run_name,
        ]

    def collect_receipt(self, run_name: str, artifact_root: Path) -> dict[str, Any]:
        """Copy raw official output and return a minimal evidence summary."""
        problems = self.validate()
        if problems:
            raise ValueError("; ".join(problems))
        raw = self.config.checkout / "data" / "simulations" / run_name / "results.json"
        if not raw.is_file():
            raise FileNotFoundError(f"official tau2 result is missing: {raw}")
        try:
            parsed = json.loads(raw.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as exc:
            raise ValueError("official tau2 result is not valid JSON") from exc
        info = parsed.get("info") if isinstance(parsed, dict) else None
        if not isinstance(info, dict) or info.get("git_commit") != self.config.source_commit:
            raise ValueError("official tau2 result source commit does not match pin")
        simulations = parsed.get("simulations") if isinstance(parsed, dict) else None
        rewards = [
            item.get("reward_info", {}).get("reward")
            for item in simulations or []
            if isinstance(item, dict)
        ]
        status = "OFFICIAL_PASS" if rewards and all(reward == 1.0 for reward in rewards) else "OFFICIAL_FAILURE"
        destination = artifact_root / "scorer" / "tau2-results.json"
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(raw, destination)
        return {
            "benchmark": "tau2-bench",
            "upstream_repository": TAU2_REPOSITORY,
            "upstream_tag": TAU2_V101_TAG,
            "source_commit": self.config.source_commit,
            "data_tree_sha256": self.config.data_tree_sha256,
            "seed": self.config.seed,
            "model": self.config.model,
            "max_concurrency": self.config.max_concurrency,
            "status": status,
            "rewards": rewards,
            "official_output_sha256": hashlib.sha256(destination.read_bytes()).hexdigest(),
            "official_output": destination.as_posix(),
        }
