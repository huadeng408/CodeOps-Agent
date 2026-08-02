"""Terminal-Bench adapter (plan Task 8.3).

Terminal-Bench 2.x evaluates long terminal tasks through its own container
runner. The adapter:
  - only converts benchmark data into the shared eval containers
  - delegates execution to the official terminal-bench runner (container)
  - never fakes a unified scorer; the run manifest/artifact/error taxonomy
    is the only uniform layer

The official repo/license/runner API must be re-verified online before the
first real run (design spec §11: retry until official evidence is pinned).
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from eval.manifest import ALLOWED_LICENSES

# The official runner command. This is the sanctioned execution path only;
# the exact CLI surface must be re-verified against the upstream repo when
# the dataset is pinned (network available).
OFFICIAL_RUNNER_MODULE = "terminal_bench.eval"


@dataclass
class TerminalBenchConfig:
    data_dir: Path
    image_name: str = "terminal-bench"
    timeout_seconds: int = 1800
    max_iterations: int = 30
    license_spdx: str = "MIT"

    def validate(self) -> list[str]:
        issues: list[str] = []
        if not self.data_dir.exists():
            issues.append(f"data_dir {self.data_dir} does not exist")
        if not self.image_name:
            issues.append("image_name required")
        if self.license_spdx not in ALLOWED_LICENSES:
            issues.append(f"license {self.license_spdx!r} not in allowlist")
        return issues


def load_tasks(data_dir: str | Path) -> list[dict[str, Any]]:
    """Load Terminal-Bench tasks from the offline data dir.

    Expects one JSONL file per task family: <family>.jsonl with records
    {name, description, category, tags, setup?, test?}.
    """
    tasks: list[dict[str, Any]] = []
    for path in sorted(Path(data_dir).glob("*.jsonl")):
        for line in path.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            record = json.loads(line)
            record["_family"] = path.stem
            tasks.append(record)
    return tasks


def to_eval_instances(tasks: list[dict[str, Any]]) -> list[dict[str, str]]:
    """Convert tasks into the shared EvalInstance shape."""
    return [
        {
            "instance_id": f"{task.get('_family', 'task')}/{task.get('name', str(i))}",
            "task_description": task.get("description", ""),
        }
        for i, task in enumerate(tasks)
    ]


def official_runner_command(config: TerminalBenchConfig, task_file: Path, output_dir: Path) -> list[str]:
    """The official terminal-bench container runner invocation."""
    return [
        "python",
        "-m",
        OFFICIAL_RUNNER_MODULE,
        "--data-file",
        str(task_file),
        "--image",
        config.image_name,
        "--output-dir",
        str(output_dir),
        "--timeout",
        str(config.timeout_seconds),
        "--max-iterations",
        str(config.max_iterations),
    ]


def env_for_runner() -> dict[str, str]:
    """Sanctioned env for the official runner; never injects API keys."""
    return dict(os.environ)
