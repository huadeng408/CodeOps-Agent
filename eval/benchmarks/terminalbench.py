"""Terminal-Bench adapter (plan Task 8.3).

Terminal-Bench 2.x evaluates long terminal tasks through its own container
runner. The adapter:
  - converts benchmark data into the shared eval containers
  - delegates execution to the official ``terminal_bench.Harness`` API
  - never fakes a unified scorer; the run manifest/artifact/error taxonomy
    is the only uniform layer

Verified against ``terminal_bench`` 0.2.18 (installed).
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
    """Check whether the official Terminal-Bench harness can run."""
    try:
        import docker  # noqa: F401
        from terminal_bench.harness import Harness  # noqa: F401
        return True, "terminal_bench + Docker available"
    except ImportError as e:
        return False, f"terminal_bench or Docker not available: {e}"


# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------


@dataclass
class TerminalBenchConfig:
    data_dir: Path
    license_spdx: str = "MIT"

    def validate(self) -> list[str]:
        issues: list[str] = []
        if not self.data_dir.exists():
            issues.append(f"data_dir {self.data_dir} does not exist")
        if self.license_spdx not in ALLOWED_LICENSES:
            issues.append(f"license {self.license_spdx!r} not in allowlist")
        return issues


# ---------------------------------------------------------------------------
# Task loading
# ---------------------------------------------------------------------------


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


# ---------------------------------------------------------------------------
# Module-level load_instances() -- eval/run.py HarnessRun path
# ---------------------------------------------------------------------------

#: Env var naming the offline Terminal-Bench data dir.
DATA_DIR_ENV = "TERMINALBENCH_DATA_DIR"


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


def run(driver: Any, limit: int | None = None, **kwargs: Any) -> list[EvalResult]:
    """Module-level runner aligned with the ``eval.run`` CLI contract.

    Invokes the official ``terminal_bench.Harness`` API to run tasks through
    Docker containers.  Returns one :class:`EvalResult` per task.

    If Docker is unavailable, falls back to a dry-run mode that produces
    an ``ERROR_INFRA`` result per task without running containers.

    Requires ``$TERMINALBENCH_DATA_DIR`` or an explicit ``data_dir`` kwarg.
    """
    data_dir_raw = kwargs.pop("data_dir", os.environ.get(DATA_DIR_ENV, ""))
    if not str(data_dir_raw):
        raise RuntimeError(
            f"terminal-bench cannot run: no data dir provided "
            f"(set {DATA_DIR_ENV} or pass data_dir=...)"
        )
    data_dir = Path(data_dir_raw)
    output_dir = Path(kwargs.pop("output_dir", "."))

    config = TerminalBenchConfig(data_dir=data_dir)
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

    can_score, reason = _can_score_official()
    logger.info("[terminalbench] _can_score_official: %s — %s", can_score, reason)

    if not can_score:
        logger.warning(
            "[terminalbench] Docker not available; returning ERROR_INFRA dry-run results"
        )
        return [
            EvalResult(
                instance_id=str(task.get("name", task.get("task_id", f"task-{i}"))),
                error=f"ERROR_INFRA: {reason}",
            )
            for i, task in enumerate(tasks)
        ]

    # Run via the official terminal_bench.Harness API.
    return _run_with_harness(config, tasks, output_dir)


def _run_with_harness(
    config: TerminalBenchConfig,
    tasks: list[dict[str, Any]],
    output_dir: Path,
) -> list[EvalResult]:
    """Invoke the official ``terminal_bench.Harness`` programmatically."""
    from terminal_bench.agents.agent_name import AgentName
    from terminal_bench.harness import Harness

    output_dir.mkdir(parents=True, exist_ok=True)

    # Extract task IDs from the loaded tasks.
    task_ids: list[str] = []
    for task in tasks:
        tid = task.get("task_id") or task.get("name") or task.get("id")
        if tid:
            task_ids.append(str(tid))

    if not task_ids:
        raise RuntimeError("No task IDs found in loaded tasks")

    # Build and run the harness.
    harness = Harness(
        output_path=output_dir,
        run_id="code-agent-terminalbench",
        agent_name=AgentName.NOP,
        dataset_path=config.data_dir,
        task_ids=task_ids,
        n_concurrent_trials=1,
        n_attempts=1,
        cleanup=False,
    )

    results = harness.run()

    # Convert BenchmarkResults → list[EvalResult]
    eval_results: list[EvalResult] = []
    for trial in results.results:
        error = ""
        if not trial.is_resolved:
            error = f"unresolved (failure_mode={trial.failure_mode})"
        eval_results.append(
            EvalResult(
                instance_id=trial.task_id,
                error=error,
                tokens_in=trial.total_input_tokens or 0,
                tokens_out=trial.total_output_tokens or 0,
            )
        )

    # Write summary artifact.
    summary = {
        "benchmark": "terminal-bench",
        "num_tasks": len(tasks),
        "task_ids": task_ids,
        "n_resolved": results.n_resolved,
        "n_unresolved": results.n_unresolved,
        "accuracy": results.accuracy,
        "pass_at_k": results.pass_at_k,
        "official_runner": "terminal_bench.Harness",
        "upstream_results_dir": str(output_dir),
    }
    summary_path = output_dir / "terminalbench_summary.json"
    summary_path.write_text(json.dumps(summary, indent=2, ensure_ascii=False), encoding="utf-8")
    logger.info("[terminalbench] summary -> %s", summary_path)

    return eval_results
