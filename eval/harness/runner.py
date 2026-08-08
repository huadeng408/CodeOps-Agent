"""Eval harness runner (plan Task 8.1 / Phase 4 rewrite).

Drives one instance at a time with: independent workspace (temp dir per
instance), budget enforcement, checkpoint resume (completed instances are
never re-run), error classification (timeout/OOM/infra/agent/scorer) and
canonical artifacts. Network is OFF by default; the benchmark explicitly
enables it via allowlist when needed.

Phase 4 (2026-08-08): Replaced dead ``InstanceRunner(dict)`` protocol with
``AgentAdapter.solve_instance(EvalInstance, working_dir, **kwargs) →
EvalResult``.  Added scorer callback, pre-start budget check, fail-closed
on missing instance_id, workspace preservation on failure, and reachable
ERROR_SCORER.
"""

from __future__ import annotations

import shutil
import subprocess
import tempfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable

from eval.adapter import AgentAdapter, EvalInstance, EvalResult
from eval.harness.artifacts import RunArtifacts
from eval.harness.budget import Budget, BudgetExceeded, BudgetUsage, check_budget

# Error taxonomy — infra failures are never counted as model failures and are
# never silently removed from the denominator.
ERROR_TIMEOUT = "timeout"
ERROR_OOM = "oom"
ERROR_INFRA = "infra"
ERROR_AGENT = "agent"
ERROR_SCORER = "scorer"

# Scorer callback: called after each successful solve_instance.
# Returns a dict that gets merged into the prediction artifact.
ScorerCallback = Callable[[EvalResult, EvalInstance], dict[str, Any]]


def classify_error(exc: BaseException) -> str:
    if isinstance(exc, BudgetExceeded):
        if exc.kind == "wall-clock":
            return ERROR_TIMEOUT
        if exc.kind == "output":
            return ERROR_OOM
        return ERROR_AGENT
    if isinstance(exc, subprocess.TimeoutExpired) or isinstance(exc, TimeoutError):
        return ERROR_TIMEOUT
    if isinstance(exc, MemoryError):
        return ERROR_OOM
    if "connection" in str(exc).lower() or "network" in str(exc).lower():
        return ERROR_INFRA
    return ERROR_AGENT


@dataclass
class HarnessRun:
    run_id: str
    artifacts: RunArtifacts
    budget: Budget = field(default_factory=Budget)
    checkpoint_path: Path | None = None
    network_allowed: bool = False
    adapter: AgentAdapter | None = None
    scorer: ScorerCallback | None = None
    config: dict[str, Any] = field(default_factory=dict)

    def __post_init__(self) -> None:
        self._completed: set[str] = set()
        self._global_usage = BudgetUsage()
        if self.checkpoint_path is not None and self.checkpoint_path.exists():
            for line in self.checkpoint_path.read_text(encoding="utf-8").splitlines():
                if line.strip():
                    self._completed.add(line.strip())

    def _mark_completed(self, instance_id: str) -> None:
        self._completed.add(instance_id)
        if self.checkpoint_path is not None:
            self.checkpoint_path.parent.mkdir(parents=True, exist_ok=True)
            with self.checkpoint_path.open("a", encoding="utf-8") as handle:
                handle.write(instance_id + "\n")

    def run(self, instances: list[EvalInstance]) -> dict[str, Any]:
        """Run all *instances* through the adapter, recording artifacts.

        Parameters
        ----------
        instances:
            List of :class:`EvalInstance` objects to evaluate.

        Returns
        -------
        dict
            ``{"summary": {...}, "summary_path": "..."}``.
        """
        summary: dict[str, Any] = {
            "run_id": self.run_id,
            "total": len(instances),
            "completed": 0,
            "resumed_skipped": 0,
            "by_category": {
                ERROR_TIMEOUT: 0,
                ERROR_OOM: 0,
                ERROR_INFRA: 0,
                ERROR_AGENT: 0,
                ERROR_SCORER: 0,
            },
        }
        for instance in instances:
            instance_id = instance.instance_id
            # Fail closed: missing/empty instance_id is never silently skipped
            if not instance_id:
                summary["by_category"][ERROR_AGENT] += 1
                self.artifacts.record_failure(
                    "", ERROR_AGENT,
                    f"instance_id is empty or missing; instance={instance!r}",
                )
                self.artifacts.record_event("", "failed-missing-instance-id")
                continue

            # Pre-start budget check — fail before creating workspace
            try:
                check_budget(self.budget, self._global_usage)
            except BudgetExceeded as exc:
                category = classify_error(exc)
                summary["by_category"][category] += 1
                self.artifacts.record_failure(instance_id, category, str(exc)[:500])
                self.artifacts.record_event(instance_id, f"failed-{category}")
                continue

            # Resume skip
            if instance_id in self._completed:
                summary["resumed_skipped"] += 1
                self.artifacts.record_event(instance_id, "skipped-resume")
                continue

            workspace = Path(tempfile.mkdtemp(prefix=f"eval-{self.run_id}-"))
            usage = BudgetUsage()
            try:
                if not self.network_allowed:
                    _block_network(workspace)

                # Call adapter
                if self.adapter is not None:
                    result = self.adapter.solve_instance(instance, str(workspace))
                else:
                    result = EvalResult(
                        instance_id=instance_id,
                        error="no adapter configured",
                    )

                # Feed budget from result
                usage.record_tokens(result.tokens_in + result.tokens_out)
                usage.record_cost(result.cost)
                # Wall clock is auto-captured by BudgetUsage.started_at

                # Post-solve budget check
                check_budget(self.budget, usage)
                # Update global usage for pre-start checks on next instances
                self._global_usage.record_tokens(usage.tokens)
                self._global_usage.record_cost(usage.cost)

                # Record instance
                self.artifacts.record_instance({
                    "instance_id": instance_id,
                    "task_description": instance.task_description,
                    "metadata": instance.metadata,
                })

                # Build prediction dict
                prediction: dict[str, Any] = {
                    "instance_id": instance_id,
                    "model_patch": result.model_patch,
                    "answer": result.answer,
                    "cost": result.cost,
                    "tokens_in": result.tokens_in,
                    "tokens_out": result.tokens_out,
                    "trace_id": result.trace_id,
                    "error": result.error,
                    "wall_time_s": result.wall_time_s,
                }

                # Scorer
                if self.scorer is not None:
                    try:
                        scorer_result = self.scorer(result, instance)
                        prediction.update(scorer_result)
                    except Exception as scorer_exc:
                        # scorer exception → ERROR_SCORER (now reachable!)
                        raise ScorerError(str(scorer_exc)) from scorer_exc

                self.artifacts.record_prediction(prediction)
                self.artifacts.record_event(instance_id, "completed")
                self._mark_completed(instance_id)
                summary["completed"] += 1

                # Success — clean up workspace
                try:
                    shutil.rmtree(workspace, ignore_errors=True)
                except Exception:
                    pass

            except ScorerError as exc:
                category = ERROR_SCORER
                summary["by_category"][category] += 1
                self.artifacts.record_failure(instance_id, category, str(exc)[:500])
                self.artifacts.record_event(instance_id, f"failed-{category}")
                # Preserve workspace on failure for post-mortem
            except BaseException as exc:  # noqa: BLE001 - classify and record
                category = classify_error(exc)
                summary["by_category"][category] += 1
                self.artifacts.record_failure(
                    instance_id, category,
                    f"{str(exc)[:500]} | workspace: {workspace}",
                )
                self.artifacts.record_event(instance_id, f"failed-{category}")
                # Preserve workspace on failure for post-mortem

        summary["ok"] = summary["completed"]
        summary["failed"] = sum(summary["by_category"].values())
        summary["skipped"] = summary["resumed_skipped"]
        path = self.artifacts.write_summary(summary)
        self.artifacts.write_environment()

        # Write run manifest (Phase 4: RunPin contract)
        manifest = _build_manifest(self, summary)
        self.artifacts.write_manifest(manifest)

        return {"summary": summary, "summary_path": str(path)}


class ScorerError(Exception):
    """Raised when the scorer callback fails; classified as ERROR_SCORER."""


def _build_manifest(harness: HarnessRun, summary: dict[str, Any]) -> dict[str, Any]:
    """Build run-manifest.json from harness config and run summary."""
    config = harness.config
    return {
        "run_id": harness.run_id,
        "mode": config.get("mode", "official"),
        "synthetic": config.get("synthetic", False),
        "git_sha": config.get("git_sha", ""),
        "dirty_hash": config.get("dirty_hash", ""),
        "model": config.get("model", ""),
        "model_revision": config.get("model_revision", ""),
        "prompt_hash": config.get("prompt_hash", ""),
        "corpus_generation": config.get("corpus_generation", ""),
        "qrels_hash": config.get("qrels_hash", ""),
        "physical_index": config.get("index_name", ""),
        "index_mapping_hash": config.get("index_mapping_hash", ""),
        "budgets": {
            "wall_clock_seconds": harness.budget.wall_clock_seconds,
            "max_tokens": harness.budget.max_tokens,
            "max_cost": harness.budget.max_cost,
            "max_output_bytes": harness.budget.max_output_bytes,
            "max_processes": harness.budget.max_processes,
        },
        "seed": config.get("seed", 42),
        "start_time": config.get("start_time", ""),
        "network_policy": "disabled" if not harness.network_allowed else "allowed",
        "summary": {
            "total": summary["total"],
            "completed": summary["completed"],
            "failed": summary["failed"],
            "skipped": summary.get("skipped", summary.get("resumed_skipped", 0)),
        },
    }


def _block_network(workspace: Path) -> None:
    """Best-effort network block for the instance workspace on Windows.

    Full sandboxing is the benchmark's Docker/container responsibility; this
    marks the run as network-disabled and records the intent in the
    workspace so infra failures are distinguishable.
    """
    # TODO: real network isolation (H2)
    (workspace / "NETWORK_DISABLED").write_text("network allowlist not granted\n", encoding="utf-8")
