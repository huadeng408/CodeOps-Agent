"""τ²-bench adapter (plan Task 8.3).

τ²-bench evaluates tool-use / state-consistency / multi-turn tasks through
its native domain runner. The adapter defers execution to the official
``tau_bench.run.run(RunConfig)`` API and never masquerades as a unified
scorer. The unified layer is the run manifest / artifact tree / error
taxonomy.

Verified against ``tau_bench`` 0.1.0 (editable, D:\\vscode\\tau-bench).
"""

from __future__ import annotations

import json
import logging
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from eval.adapter import EvalInstance, EvalResult
from eval.manifest import ALLOWED_LICENSES

logger = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# Official runner availability
# ---------------------------------------------------------------------------


def _can_score_official() -> tuple[bool, str]:
    """Check whether the official τ²-bench runner can run."""
    try:
        from tau_bench.run import run  # noqa: F401
        from tau_bench.types import RunConfig  # noqa: F401
        return True, "tau_bench available"
    except ImportError as e:
        return False, f"tau_bench not available: {e}"


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

    Invokes the official ``tau_bench.run.run(RunConfig)`` API.  Requires
    ``$TAU2_DATA_DIR`` or an explicit ``data_dir`` kwarg.

    If the official runner is unavailable, returns ``ERROR_INFRA`` results.
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
    model_name = kwargs.pop("model_name", os.environ.get("TAU2_MODEL", "deepseek-v4"))
    num_trials = int(kwargs.pop("num_trials", 1))
    max_concurrency = int(kwargs.pop("max_concurrency", 1))
    task_split = kwargs.pop("task_split", "test")

    config = Tau2BenchConfig(
        data_dir=data_dir,
        env_name=env_name,
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

    can_score, reason = _can_score_official()
    logger.info("[tau2bench] _can_score_official: %s — %s", can_score, reason)

    if not can_score:
        logger.warning(
            "[tau2bench] tau_bench not available; returning ERROR_INFRA dry-run results"
        )
        return [
            EvalResult(
                instance_id=str(task.get("id", task.get("task_id", f"task-{i}"))),
                error=f"ERROR_INFRA: {reason}",
            )
            for i, task in enumerate(tasks)
        ]

    # Run via the official tau_bench.run() API.
    return _run_with_config(config, tasks, output_dir, model_name, num_trials,
                           max_concurrency, task_split)


def _run_with_config(
    config: Tau2BenchConfig,
    tasks: list[dict[str, Any]],
    output_dir: Path,
    model_name: str,
    num_trials: int,
    max_concurrency: int,
    task_split: str,
) -> list[EvalResult]:
    """Invoke the official ``tau_bench.run.run()`` programmatically."""
    from tau_bench.run import run as tau_run
    from tau_bench.types import EnvRunResult, RunConfig

    output_dir.mkdir(parents=True, exist_ok=True)

    task_ids = [
        int(task.get("id", task.get("task_id", i)))
        for i, task in enumerate(tasks)
    ]

    run_config = RunConfig(
        model_provider="deepseek",
        user_model_provider="deepseek",
        model=model_name,
        user_model=model_name,
        num_trials=num_trials,
        env=config.env_name,
        task_split=task_split,
        task_ids=task_ids,
        log_dir=str(output_dir),
        max_concurrency=max_concurrency,
    )

    env_results: list[EnvRunResult] = tau_run(run_config)

    # Convert EnvRunResult → list[EvalResult]
    eval_results: list[EvalResult] = []
    for er in env_results:
        error = ""
        if er.reward < 1.0:
            error = f"reward={er.reward:.2f}"
        eval_results.append(
            EvalResult(
                instance_id=str(er.task_id),
                error=error,
            )
        )

    # Write summary artifact.
    summary = {
        "benchmark": "tau2-bench",
        "domain": config.env_name,
        "num_tasks": len(tasks),
        "task_ids": task_ids,
        "num_trials": num_trials,
        "model": model_name,
        "official_runner": "tau_bench.run.run",
        "upstream_results_dir": str(output_dir),
    }
    summary_path = output_dir / "tau2bench_summary.json"
    summary_path.write_text(json.dumps(summary, indent=2, ensure_ascii=False), encoding="utf-8")
    logger.info("[tau2bench] summary -> %s", summary_path)

    return eval_results
