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
import sys
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path
from typing import TYPE_CHECKING, Any

from eval.adapter import EvalInstance, EvalResult
from eval.benchmarks.base import AgentBenchmark
from eval.harness.runner import SCORER_RAW_OUTPUT_KEY
from eval.manifest import ALLOWED_LICENSES

#: tau2-bench is a tool-using conversation benchmark over its own domain
#: runner: no retrieval, no reranking, no corpus.
TRACE_CAPABILITIES: tuple[str, ...] = ()

if TYPE_CHECKING:  # import-time only — the AgentAdapter protocol is never used at runtime here
    from eval.adapter import AgentAdapter

logger = logging.getLogger(__name__)


def _ensure_utf8_console(streams: tuple[Any, ...] | None = None) -> None:
    """Avoid Windows GBK failures from the official runner's Unicode output."""
    for stream in streams or (sys.stdout, sys.stderr):
        try:
            encoding = (stream.encoding or "").lower().replace("-", "")
            if encoding and encoding != "utf8":
                stream.reconfigure(encoding="utf-8", errors="replace")
        except (AttributeError, ValueError):
            pass


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


# ---------------------------------------------------------------------------
# AgentBenchmark implementation (unified harness lifecycle)
# ---------------------------------------------------------------------------

#: Official scorer name; identical to the value written into the
#: ``_run_with_config`` summary artifact.
_SCORER_NAME = "tau_bench.run.run"


def _as_int(value: Any) -> int | None:
    """Best-effort int conversion for τ²-bench task ids (JSONL ``id``)."""
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def _sha256_prefix(path: Path, length: int = 12) -> str:
    """Short sha256 hex prefix of a file, pinned per run for reproducibility."""
    import hashlib

    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()[:length]


class Tau2BenchAdapter(AgentBenchmark):
    """τ²-bench adapter for the unified :class:`AgentBenchmark` contract.

    Reuses the existing τ²-bench helpers (:func:`_can_score_official`,
    :func:`load_tasks`, :class:`Tau2BenchConfig`) and defers execution to
    the OFFICIAL ``tau_bench.run.run(RunConfig)`` black-box:

    - :meth:`prepare` is a no-op — τ²-bench tasks run inside the official
      domain runner and need no workspace setup.
    - :meth:`solve` builds a :class:`RunConfig` for the single instance
      (model config read from env vars, mirroring the module-level
      :func:`run`), delegates to the official runner and converts the
      returned :class:`EnvRunResult` into an :class:`EvalResult`.  The
      official reward is persisted to ``workspace/score.json`` so
      :meth:`score` can report it without re-running anything.
    - :meth:`score` returns the official resolution read from that
      sidecar — the official scorer is never faked.  Missing evidence
      fails closed to ``resolved: False``.

    The pins reflect exactly what the offline loader (:func:`load_tasks`)
    and the official runner use: the offline τ²-bench JSONL family under
    the data dir (sha256-pinned) and ``tau_bench.run.run``.
    """

    name = "tau2-bench"

    def __init__(
        self,
        data_dir: str | Path | None = None,
        env_name: str | None = None,
        model_name: str | None = None,
        model_provider: str | None = None,
        base_url: str | None = None,
        api_key: str | None = None,
        num_trials: int | None = None,
        max_concurrency: int | None = None,
        task_split: str | None = None,
    ) -> None:
        """Configure the adapter from explicit args or env vars.

        Falls back to ``$TAU2_DATA_DIR`` (offline data dir), ``$TAU2_ENV``
        (default ``airline``) and ``$TAU2_MODEL`` (default ``deepseek-v4``)
        — the same env-var contract as the module-level :func:`run`.
        """
        raw_dir = data_dir or os.environ.get(DATA_DIR_ENV) or str(
            Path(__file__).resolve().parent.parent / "benchmark_data" / "tau2bench"
        )
        self.data_dir = Path(raw_dir).resolve() if raw_dir else None
        self.env_name = env_name or os.environ.get("TAU2_ENV", "airline")
        self.model_name = model_name or os.environ.get("TAU2_MODEL", "deepseek-v4")
        self.model_provider = model_provider or os.environ.get("TAU2_MODEL_PROVIDER", "deepseek")
        self.base_url = base_url or os.environ.get("OPENAI_BASE_URL", "")
        self.api_key = api_key or os.environ.get("OPENAI_API_KEY", "")
        configured_temperature = os.environ.get("TAU2_TEMPERATURE")
        self.temperature = (
            float(configured_temperature)
            if configured_temperature is not None
            else (1.0 if self.model_name.startswith("gpt-5") else 0.0)
        )
        self.num_trials = int(num_trials or 1)
        self.max_concurrency = int(max_concurrency or 1)
        self.task_split = task_split or "test"

    # ------------------------------------------------------------------
    # AgentBenchmark contract
    # ------------------------------------------------------------------

    def prepare(self, instance: EvalInstance, workspace: Path) -> None:
        """No-op: τ²-bench runs inside the official domain runner and
        requires no workspace setup."""

    def solve(
        self,
        instance: EvalInstance,
        workspace: Path,
        adapter: "AgentAdapter",
        **kwargs: Any,
    ) -> EvalResult:
        """Run the single instance through the official runner.

        Builds a :class:`RunConfig` for the instance (mirroring
        :func:`_run_with_config`, but per-instance so the official reward
        is preserved), delegates to ``tau_bench.run.run`` and converts the
        :class:`EnvRunResult` into an :class:`EvalResult`.  The official
        outcome is written to ``workspace/score.json`` so :meth:`score`
        can report it without re-running anything.

        Never returns ``None``: unavailable official runner, missing data
        dir and missing task records all produce fail-closed
        ``EvalResult`` with a non-empty ``error``.
        """
        workspace = Path(workspace)
        task_id = _as_int(instance.metadata.get("id", instance.instance_id.rsplit("/", 1)[-1]))

        can_score, reason = _can_score_official()
        if not can_score:
            return EvalResult(
                instance_id=instance.instance_id,
                error=f"ERROR_INFRA: {reason}",
            )
        if task_id is None:
            return EvalResult(
                instance_id=instance.instance_id,
                error=f"ERROR_SETUP: cannot parse task id from {instance.instance_id!r}",
            )
        if self.data_dir is None:
            return EvalResult(
                instance_id=instance.instance_id,
                error=(
                    f"ERROR_SETUP: no data dir "
                    f"(set {DATA_DIR_ENV} or pass data_dir=...)"
                ),
            )

        config = Tau2BenchConfig(data_dir=self.data_dir, env_name=self.env_name)
        issues = config.validate()
        if issues:
            return EvalResult(
                instance_id=instance.instance_id,
                error=f"ERROR_SETUP: {'; '.join(issues)}",
            )

        tasks = [t for t in load_tasks(config.data_dir) if t.get("_env") == config.env_name]
        task = next(
            (t for t in tasks if _as_int(t.get("id", t.get("task_id"))) == task_id),
            None,
        )
        if task is None:
            return EvalResult(
                instance_id=instance.instance_id,
                error=(
                    f"ERROR_SETUP: task {task_id} not found in {config.data_dir} "
                    f"(env {config.env_name!r})"
                ),
            )

        output_dir = workspace / "tau2_runs"
        output_dir.mkdir(parents=True, exist_ok=True)

        from tau_bench.run import run as tau_run
        from tau_bench.types import EnvRunResult, RunConfig

        run_config = RunConfig(
            model_provider=self.model_provider,
            user_model_provider=self.model_provider,
            model=self.model_name,
            user_model=self.model_name,
            num_trials=self.num_trials,
            env=config.env_name,
            task_split=self.task_split,
            task_ids=[task_id],
            log_dir=str(output_dir),
            max_concurrency=self.max_concurrency,
            temperature=self.temperature,
        )

        logger.info(
            "[tau2bench-adapter] solving %s via %s (env=%s, model=%s)",
            instance.instance_id,
            _SCORER_NAME,
            config.env_name,
            self.model_name,
        )
        _ensure_utf8_console()
        with _official_model_environment(
            model_provider=self.model_provider,
            base_url=self.base_url,
            api_key=self.api_key,
        ):
            env_results: list[EnvRunResult] = tau_run(run_config)

        if not env_results:
            return EvalResult(
                instance_id=instance.instance_id,
                error="ERROR_RUN: official runner returned no results",
            )

        er = env_results[0]
        error = ""
        if er.reward < 1.0:
            error = f"reward={er.reward:.2f}"

        result = EvalResult(
            instance_id=instance.instance_id,
            error=error,
        )

        # Persist the official outcome for score(); reward is resolved the
        # same way tau_bench.run.display_metrics defines success.
        resolved = er.reward >= 1.0 - 1e-6
        sidecar = {
            "resolved": resolved,
            "reward": er.reward,
            "failure_mode": None,
            "info": er.info,
        }
        (workspace / "score.json").write_text(
            json.dumps(sidecar, indent=2, ensure_ascii=False), encoding="utf-8"
        )
        return result

    def score(
        self,
        result: EvalResult,
        instance: EvalInstance,
        workspace: Path,
    ) -> dict[str, Any]:
        """Return the official resolution embedded in :meth:`solve`.

        Reads the per-run ``workspace/score.json`` sidecar written by
        :meth:`solve` from the official :class:`EnvRunResult`.  Missing
        sidecar means no official evidence -> fail-closed ``resolved: False``
        (the official scorer is never faked).
        """
        workspace = Path(workspace)
        sidecar_path = workspace / "score.json"
        if sidecar_path.exists():
            try:
                raw_sidecar = sidecar_path.read_text(encoding="utf-8")
                sidecar = json.loads(raw_sidecar)
                payload = {
                    "resolved": bool(sidecar.get("resolved", False)),
                    "reward": float(sidecar.get("reward", 0.0)),
                    "scorer": _SCORER_NAME,
                    "failure_mode": sidecar.get("failure_mode"),
                    "scorer_status": "official: reward embedded in solve()",
                }
                payload[SCORER_RAW_OUTPUT_KEY] = {
                    f"{instance.instance_id}-score.json": raw_sidecar
                }
                for checkpoint in sorted((workspace / "tau2_runs").glob("*.json")):
                    payload[SCORER_RAW_OUTPUT_KEY][
                        f"{instance.instance_id}-official-checkpoint.json"
                    ] = checkpoint.read_text(encoding="utf-8")
                return payload
            except (json.JSONDecodeError, OSError, ValueError):
                pass
        return {
            "resolved": False,
            "reward": 0.0,
            "scorer": _SCORER_NAME,
            "failure_mode": None,
            "scorer_status": "no official run record (sidecar missing)",
        }

    # ------------------------------------------------------------------
    # Pins
    # ------------------------------------------------------------------

    @property
    def pins(self) -> dict[str, str]:
        """Reproducibility pins for benchmark/dataset/scorer.

        ``dataset_revision`` is the sha256 prefix of the offline JSONL
        family actually consumed, so every run is pinned to the exact
        offline snapshot on disk.
        """
        revision = ""
        dataset_name = f"tau2-bench offline (env {self.env_name})"
        if self.data_dir is not None:
            jsonl = self.data_dir / f"{self.env_name}.jsonl"
            if jsonl.exists():
                revision = _sha256_prefix(jsonl)
                dataset_name = f"tau2-bench offline {self.env_name}.jsonl"
        return {
            "benchmark": self.name,
            "dataset_name": dataset_name,
            "dataset_revision": revision,
            "scorer_name": _SCORER_NAME,
            "env_name": self.env_name,
            "model_name": self.model_name,
            "model_provider": self.model_provider,
            "model_base_url": self.base_url,
            "temperature": str(self.temperature),
        }


@contextmanager
def _official_model_environment(
    *, model_provider: str, base_url: str, api_key: str
):
    """Temporarily expose the CLI connection only to the official runner.

    tau2's OpenAI model reads these standard LiteLLM environment variables;
    restoring the prior process environment prevents credentials or endpoint
    choices from leaking into later benchmark instances.
    """
    names = {
        "OPENAI_API_KEY": api_key,
        "OPENAI_BASE_URL": base_url,
        "OPENAI_API_BASE": base_url,
    }
    previous = {name: os.environ.get(name) for name in names}
    try:
        for name, value in names.items():
            if value:
                os.environ[name] = value
        yield
    finally:
        for name, value in previous.items():
            if value is None:
                os.environ.pop(name, None)
            else:
                os.environ[name] = value
