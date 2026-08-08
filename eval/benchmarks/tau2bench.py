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
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from eval.adapter import EvalInstance, EvalResult
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


# ---------------------------------------------------------------------------
# Module-level load_instances() -- eval/run.py HarnessRun path
# ---------------------------------------------------------------------------


def load_instances(
    limit: int | None = None,
    **kwargs: Any,
) -> list[EvalInstance]:
    """Module-level instance loader for the HarnessRun path in ``eval/run.py``.

    Loads τ²-bench tasks from the offline data dir.  Requires
    ``$TAU2_DATA_DIR`` or an explicit ``data_dir`` kwarg.

    Raises:
        RuntimeError: No data dir or no tasks found.
    """
    data_dir_raw = kwargs.pop("data_dir", os.environ.get(DATA_DIR_ENV, ""))
    if not str(data_dir_raw):
        raise RuntimeError(
            f"tau2-bench cannot load instances: no data dir provided "
            f"(set {DATA_DIR_ENV} or pass data_dir=...)"
        )
    data_dir = Path(data_dir_raw)
    env_name = kwargs.pop("env_name", os.environ.get("TAU2_ENV", "airline"))
    config = Tau2BenchConfig(data_dir=data_dir, env_name=env_name)
    issues = config.validate()
    if issues:
        raise RuntimeError(
            f"tau2-bench cannot run: {'; '.join(issues)}"
        )
    tasks = [t for t in load_tasks(config.data_dir) if t.get("_env") == config.env_name]
    if not tasks:
        raise RuntimeError(
            f"no tasks found for env {config.env_name!r} in {config.data_dir}"
        )
    if limit is not None:
        tasks = tasks[:limit]
    return [
        EvalInstance(
            instance_id=f"{task.get('_env', 'env')}/{task.get('id', str(i))}",
            task_description=task.get("user", task.get("question", "")),
        )
        for i, task in enumerate(tasks)
    ]


# ---------------------------------------------------------------------------
# Module-level run() -- eval/run.py CLI contract
# ---------------------------------------------------------------------------

#: Env var naming the offline τ²-bench data dir used by ``run()``.
DATA_DIR_ENV = "TAU2_DATA_DIR"


def run(driver: Any, limit: int | None = None, **kwargs: Any) -> list[EvalResult]:
    """Module-level runner aligned with the ``eval.run`` CLI contract.

    τ²-bench defers execution to the official domain runner
    (:data:`OFFICIAL_RUNNER_MODULE`): this wrapper validates prerequisites,
    materialises the task file, invokes the official runner, writes a sidecar
    summary JSON, and returns one :class:`EvalResult` per task.  It never
    masquerades as a unified scorer -- the official runner owns scoring.

    Missing data dir / official runner fail loudly *before* any model call
    (per design spec §6).  Data dir defaults to ``$TAU2_DATA_DIR``; ``env_name``
    defaults to ``$TAU2_ENV`` or ``"airline"``.
    """
    data_dir_raw = kwargs.pop("data_dir", os.environ.get(DATA_DIR_ENV, ""))
    if not str(data_dir_raw):
        raise RuntimeError(
            f"tau2-bench cannot run: no data dir provided "
            f"(set {DATA_DIR_ENV} or pass data_dir=...)"
        )
    data_dir = Path(data_dir_raw)
    env_name = kwargs.pop("env_name", os.environ.get("TAU2_ENV", "airline"))
    output_dir = Path(kwargs.pop("output_dir", "."))
    num_turns = int(kwargs.pop("num_turns", 30))
    timeout = float(kwargs.pop("timeout", 3600))

    config = Tau2BenchConfig(
        data_dir=data_dir,
        env_name=env_name,
        num_turns=num_turns,
    )
    issues = config.validate()
    if issues:
        raise RuntimeError(
            f"tau2-bench cannot run: {'; '.join(issues)} "
            f"(set {DATA_DIR_ENV} to the offline data dir)"
        )

    tasks = [t for t in load_tasks(config.data_dir) if t.get("_env") == config.env_name]
    if not tasks:
        raise RuntimeError(
            f"no tasks found for env {config.env_name!r} in {config.data_dir}"
        )
    if limit is not None:
        tasks = tasks[:limit]

    output_dir.mkdir(parents=True, exist_ok=True)
    task_file = output_dir / f"{config.env_name}_tasks.jsonl"
    with task_file.open("w", encoding="utf-8") as fh:
        for task in tasks:
            fh.write(json.dumps(task, ensure_ascii=False) + "\n")

    cmd = official_runner_command(config, task_file, output_dir)
    print(f"[tau2bench] invoking official runner: {' '.join(cmd)}")
    proc = subprocess.run(
        cmd,
        env=env_for_runner(),
        capture_output=True,
        text=True,
        timeout=timeout,
    )
    if proc.returncode != 0:
        raise RuntimeError(
            f"tau2 official runner failed (exit {proc.returncode}): "
            f"{proc.stderr[-1000:]}"
        )

    _write_summary(config, task_file, output_dir, cmd, tasks)
    return [
        EvalResult(instance_id=str(task.get("id", i)), error="")
        for i, task in enumerate(tasks)
    ]


def _write_summary(
    config: Tau2BenchConfig,
    task_file: Path,
    output_dir: Path,
    cmd: list[str],
    tasks: list[dict[str, Any]],
) -> Path:
    """Write the repo-side summary JSON (metadata only, no API keys)."""
    summary = {
        "benchmark": "tau2-bench",
        "domain": config.env_name,
        "num_tasks": len(tasks),
        "num_turns": config.num_turns,
        "official_runner": OFFICIAL_RUNNER_MODULE,
        "task_file": str(task_file),
        "runner_command": cmd,
        "upstream_results_dir": str(output_dir),
    }
    path = output_dir / "tau2bench_summary.json"
    path.write_text(json.dumps(summary, indent=2, ensure_ascii=False), encoding="utf-8")
    print(f"[tau2bench] summary -> {path}")
    return path
