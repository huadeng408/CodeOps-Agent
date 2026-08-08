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
import shutil
import sys
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
# Task-dir materialization (TB 2.0 layout -> terminal_bench.Harness layout)
# ---------------------------------------------------------------------------

#: docker-compose for a task whose Dockerfile lives under ``environment/``.
#: Env vars are provided by the official ``DockerComposeManager`` at runtime.
_COMPOSE_TEMPLATE = """\
# Materialized by the localcode eval adapter (TB 2.0 -> terminal_bench.Harness).
# Builds the task environment from environment/ and adds tmux/asciinema, which
# the official harness requires inside the container.
services:
  client:
    build:
      context: environment
      dockerfile: Dockerfile.tb-harness
    image: ${T_BENCH_TASK_DOCKER_CLIENT_IMAGE_NAME}
    container_name: ${T_BENCH_TASK_DOCKER_CLIENT_CONTAINER_NAME}
    command: [ "sh", "-c", "sleep infinity" ]
    environment:
      - TEST_DIR=${T_BENCH_TEST_DIR}
    volumes:
      - ${T_BENCH_TASK_LOGS_PATH}:${T_BENCH_CONTAINER_LOGS_PATH}
      - ${T_BENCH_TASK_AGENT_LOGS_PATH}:${T_BENCH_CONTAINER_AGENT_LOGS_PATH}
"""

#: Layer appended to the task Dockerfile.  terminal_bench.Harness hard-requires
#: tmux in the container and asciinema unless the task disables recording.
_HARNESS_LAYER = """\
# --- localcode harness shim: required by terminal_bench.Harness ---
RUN apt-get update && apt-get install -y --no-install-recommends tmux asciinema \\
    && rm -rf /var/lib/apt/lists/*
"""


def _materialize_task_dir(task_dir: Path) -> list[str]:
    """Convert one TB 2.0 task dir to the layout ``terminal_bench.Harness``
    (0.2.18) expects, writing only the files that are missing.

    The offline TB 2.0 layout keeps the Dockerfile under ``environment/``,
    the test runner under ``tests/test.sh`` and the solution under
    ``solution/solve.sh``, while the harness requires ``docker-compose.yaml``,
    ``run-tests.sh`` and ``solution.sh`` at the task root.

    Returns the list of files written (for logging).
    """
    written: list[str] = []

    # 1. docker-compose.yaml at the task root.
    compose_path = task_dir / "docker-compose.yaml"
    if not compose_path.exists():
        compose_path.write_text(_COMPOSE_TEMPLATE, encoding="utf-8")
        written.append("docker-compose.yaml")

    # 2. environment/Dockerfile.tb-harness: task Dockerfile + tmux/asciinema.
    env_dockerfile = task_dir / "environment" / "Dockerfile"
    harness_dockerfile = task_dir / "environment" / "Dockerfile.tb-harness"
    if not harness_dockerfile.exists() and env_dockerfile.exists():
        harness_dockerfile.write_text(
            env_dockerfile.read_text(encoding="utf-8") + _HARNESS_LAYER,
            encoding="utf-8",
        )
        written.append("environment/Dockerfile.tb-harness")

    # 3. run-tests.sh: the harness copies this (with tests/) to /tests and
    #    runs `bash /tests/run-tests.sh`.
    run_tests = task_dir / "run-tests.sh"
    if not run_tests.exists():
        test_sh = task_dir / "tests" / "test.sh"
        if test_sh.exists():
            shutil.copyfile(test_sh, run_tests)
            written.append("run-tests.sh")
        else:
            run_tests.write_text(
                "#!/bin/bash\n# Placeholder: no tests/test.sh found in this task.\n",
                encoding="utf-8",
            )
            written.append("run-tests.sh (placeholder)")

    # 4. solution.sh at the task root (harness TaskPaths may probe it).
    if not (task_dir / "solution.sh").exists() and not (task_dir / "solution.yaml").exists():
        solve_sh = task_dir / "solution" / "solve.sh"
        if solve_sh.exists():
            shutil.copyfile(solve_sh, task_dir / "solution.sh")
            written.append("solution.sh")

    return written


def _patch_terminal_bench_windows() -> None:
    """Monkeypatch ``terminal_bench`` container-path handling for Windows hosts.

    The official package derives container paths from ``pathlib.Path``, which
    stringifies to ``\\tmp`` / ``\\tests`` on Windows; the Docker API then
    404s on ``put_archive`` (``Could not find the file \\tmp ...``) and the
    in-container test command becomes ``bash \\tests\\run-tests.sh``, which
    bash mangles.  The adapter normalizes container dirs to POSIX separators
    and uses ``PurePosixPath`` for the container-side constants.
    """
    from pathlib import PurePosixPath

    try:
        from terminal_bench.terminal.docker_compose_manager import DockerComposeManager
    except ImportError:  # pragma: no cover - official package not installed
        return

    if getattr(DockerComposeManager, "_localcode_patched", False):
        return

    _orig_copy = DockerComposeManager.copy_to_container

    def _copy_posix(
        container: Any,
        paths: Any,
        container_dir: str | None = None,
        container_filename: str | None = None,
    ) -> None:
        if container_dir:
            container_dir = str(container_dir).replace("\\", "/")
        return _orig_copy(container, paths, container_dir, container_filename)

    DockerComposeManager.copy_to_container = staticmethod(_copy_posix)
    DockerComposeManager.CONTAINER_TEST_DIR = PurePosixPath("/tests")

    try:
        from terminal_bench.terminal.tmux_session import TmuxSession

        TmuxSession._GET_ASCIINEMA_TIMESTAMP_SCRIPT_CONTAINER_PATH = PurePosixPath(
            "/tmp/get-asciinema-timestamp.sh"
        )
    except ImportError:  # pragma: no cover
        pass

    DockerComposeManager._localcode_patched = True


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
    # The Dataset expects task directories directly under dataset_path, but our
    # offline layout has them under a tasks/ subdirectory.
    dataset_path = config.data_dir / "tasks"
    if not dataset_path.exists():
        dataset_path = config.data_dir

    # Materialize the harness-required per-task layout (TB 2.0 -> harness).
    for task_id in task_ids:
        task_dir = dataset_path / task_id
        if not task_dir.is_dir():
            logger.warning(
                "[terminalbench] task dir missing for %s — harness will fail it", task_id
            )
            continue
        for fname in _materialize_task_dir(task_dir):
            logger.info("[terminalbench] materialized %s/%s", task_id, fname)

    harness = Harness(
        output_path=output_dir,
        run_id="code-agent-terminalbench",
        agent_name=AgentName.NOP,
        dataset_path=dataset_path,
        task_ids=task_ids,
        n_concurrent_trials=1,
        n_attempts=1,
        cleanup=False,
    )

    # terminal_bench's rich progress bar emits non-ASCII glyphs (e.g. U+2717);
    # on a GBK-locale Windows console that crashes the whole run with a
    # UnicodeEncodeError. Force UTF-8 output for the harness duration.
    for stream in (sys.stdout, sys.stderr):
        try:
            enc = (stream.encoding or "").lower().replace("-", "")
            if stream is not None and enc not in ("", "utf8"):
                stream.reconfigure(encoding="utf-8", errors="replace")
        except (AttributeError, ValueError):
            pass

    _patch_terminal_bench_windows()

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
