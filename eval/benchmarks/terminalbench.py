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
import subprocess
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from eval.adapter import EvalInstance, EvalResult
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


# ---------------------------------------------------------------------------
# Module-level load_instances() -- eval/run.py HarnessRun path
# ---------------------------------------------------------------------------


def load_instances(
    limit: int | None = None,
    **kwargs: Any,
) -> list[EvalInstance]:
    """Module-level instance loader for the HarnessRun path in ``eval/run.py``.

    Loads Terminal-Bench tasks from the offline data dir.  Requires
    ``$TERMINALBENCH_DATA_DIR`` or an explicit ``data_dir`` kwarg.

    Raises:
        RuntimeError: No data dir or no tasks found.
    """
    data_dir_raw = kwargs.pop("data_dir", os.environ.get(DATA_DIR_ENV, ""))
    if not str(data_dir_raw):
        raise RuntimeError(
            f"terminal-bench cannot load instances: no data dir provided "
            f"(set {DATA_DIR_ENV} or pass data_dir=...)"
        )
    data_dir = Path(data_dir_raw)
    config = TerminalBenchConfig(data_dir=data_dir)
    issues = config.validate()
    if issues:
        raise RuntimeError(
            f"terminal-bench cannot run: {'; '.join(issues)}"
        )
    tasks = load_tasks(config.data_dir)
    if not tasks:
        raise RuntimeError(f"no tasks found in {config.data_dir}")
    if limit is not None:
        tasks = tasks[:limit]
    return [
        EvalInstance(
            instance_id=f"{task.get('_family', 'task')}/{task.get('name', str(i))}",
            task_description=task.get("description", ""),
        )
        for i, task in enumerate(tasks)
    ]


# ---------------------------------------------------------------------------
# Module-level run() -- eval/run.py CLI contract
# ---------------------------------------------------------------------------

#: Env var naming the offline Terminal-Bench data dir used by ``run()``.
DATA_DIR_ENV = "TERMINALBENCH_DATA_DIR"


def run(driver: Any, limit: int | None = None, **kwargs: Any) -> list[EvalResult]:
    """Module-level runner aligned with the ``eval.run`` CLI contract.

    Terminal-Bench defers execution to its official container runner
    (:data:`OFFICIAL_RUNNER_MODULE`): this wrapper validates prerequisites,
    materialises the task file, invokes the official runner, and returns one
    :class:`EvalResult` per task.  It never masquerades as a unified scorer --
    the official runner owns scoring.

    Missing data dir / official runner fail loudly *before* any model call
    (per design spec §6).  Data dir defaults to ``$TERMINALBENCH_DATA_DIR``.
    """
    data_dir_raw = kwargs.pop("data_dir", os.environ.get(DATA_DIR_ENV, ""))
    if not str(data_dir_raw):
        raise RuntimeError(
            f"terminal-bench cannot run: no data dir provided "
            f"(set {DATA_DIR_ENV} or pass data_dir=...)"
        )
    data_dir = Path(data_dir_raw)
    output_dir = Path(kwargs.pop("output_dir", "."))
    image_name = kwargs.pop("image_name", "terminal-bench")
    timeout = float(kwargs.pop("timeout", 3600))

    config = TerminalBenchConfig(data_dir=data_dir, image_name=image_name)
    issues = config.validate()
    if issues:
        raise RuntimeError(
            f"terminal-bench cannot run: {'; '.join(issues)} "
            f"(set {DATA_DIR_ENV} to the offline data dir)"
        )

    tasks = load_tasks(config.data_dir)
    if not tasks:
        raise RuntimeError(f"no tasks found in {config.data_dir}")
    if limit is not None:
        tasks = tasks[:limit]

    output_dir.mkdir(parents=True, exist_ok=True)
    task_file = output_dir / "terminalbench_tasks.jsonl"
    with task_file.open("w", encoding="utf-8") as fh:
        for task in tasks:
            fh.write(json.dumps(task, ensure_ascii=False) + "\n")

    cmd = official_runner_command(config, task_file, output_dir)
    print(f"[terminalbench] invoking official runner: {' '.join(cmd)}")
    proc = subprocess.run(
        cmd,
        env=env_for_runner(),
        capture_output=True,
        text=True,
        timeout=timeout,
    )
    if proc.returncode != 0:
        raise RuntimeError(
            f"terminal-bench official runner failed (exit {proc.returncode}): "
            f"{proc.stderr[-1000:]}"
        )

    _write_summary(config, task_file, output_dir, cmd, tasks)
    return [
        EvalResult(instance_id=str(task.get("name", i)), error="")
        for i, task in enumerate(tasks)
    ]


def _write_summary(
    config: TerminalBenchConfig,
    task_file: Path,
    output_dir: Path,
    cmd: list[str],
    tasks: list[dict[str, Any]],
) -> Path:
    """Write the repo-side summary JSON (metadata only, no API keys)."""
    summary = {
        "benchmark": "terminal-bench",
        "num_tasks": len(tasks),
        "image_name": config.image_name,
        "timeout_seconds": config.timeout_seconds,
        "official_runner": OFFICIAL_RUNNER_MODULE,
        "task_file": str(task_file),
        "runner_command": cmd,
        "upstream_results_dir": str(output_dir),
    }
    path = output_dir / "terminalbench_summary.json"
    path.write_text(json.dumps(summary, indent=2, ensure_ascii=False), encoding="utf-8")
    print(f"[terminalbench] summary -> {path}")
    return path
