"""τ²-bench adapter (plan Task 8.3).

τ²-bench evaluates tool-use / state-consistency / multi-turn tasks through
its native domain runner. Like Terminal-Bench, this adapter only converts
formats and defers execution to the official runner — it never masquerades
as a unified scorer. The unified layer is the run manifest / artifact tree /
error taxonomy.
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from eval.manifest import ALLOWED_LICENSES

# Official τ²-bench runner entry (re-verify upstream when pinned).
OFFICIAL_RUNNER_MODULE = "tau_bench.eval"


@dataclass
class Tau2BenchConfig:
    data_dir: Path
    env_name: str = "airline"
    num_turns: int = 30
    license_spdx: str = "MIT"

    def validate(self) -> list[str]:
        issues: list[str] = []
        if not self.data_dir.exists():
            issues.append(f"data_dir {self.data_dir} does not exist")
        if self.env_name not in ("airline", "retail", "media"):
            issues.append(f"env_name {self.env_name!r} not in airline/retail/media")
        if self.license_spdx not in ALLOWED_LICENSES:
            issues.append(f"license {self.license_spdx!r} not in allowlist")
        return issues


def load_tasks(data_dir: str | Path) -> list[dict[str, Any]]:
    """Load τ²-bench tasks from the offline data dir (one JSONL per env)."""
    tasks: list[dict[str, Any]] = []
    for path in sorted(Path(data_dir).glob("*.jsonl")):
        for line in path.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            record = json.loads(line)
            record["_env"] = path.stem
            tasks.append(record)
    return tasks


def to_eval_instances(tasks: list[dict[str, Any]]) -> list[dict[str, str]]:
    return [
        {
            "instance_id": f"{task.get('_env', 'env')}/{task.get('id', str(i))}",
            "task_description": task.get("user", task.get("question", "")),
        }
        for i, task in enumerate(tasks)
    ]


def official_runner_command(config: Tau2BenchConfig, task_file: Path, output_dir: Path) -> list[str]:
    """The official τ²-bench native domain runner invocation."""
    return [
        "python",
        "-m",
        OFFICIAL_RUNNER_MODULE,
        "--env",
        config.env_name,
        "--task-file",
        str(task_file),
        "--output-dir",
        str(output_dir),
        "--num-turns",
        str(config.num_turns),
    ]


def env_for_runner() -> dict[str, str]:
    return dict(os.environ)
