"""Eval harness runner (plan Task 8.1).

Drives one instance at a time with: independent workspace (temp dir per
instance), budget enforcement, checkpoint resume (completed instances are
never re-run), error classification (timeout/OOM/infra/agent/scorer) and
canonical artifacts. Network is OFF by default; the benchmark explicitly
enables it via allowlist when needed.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import tempfile
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable, Protocol

from eval.harness.artifacts import RunArtifacts
from eval.harness.budget import Budget, BudgetExceeded, BudgetUsage, check_budget

# Error taxonomy — infra failures are never counted as model failures and are
# never silently removed from the denominator.
ERROR_TIMEOUT = "timeout"
ERROR_OOM = "oom"
ERROR_INFRA = "infra"
ERROR_AGENT = "agent"
ERROR_SCORER = "scorer"


class InstanceRunner(Protocol):
    def __call__(self, instance: dict[str, Any], workspace: Path, budget_usage: BudgetUsage) -> dict[str, Any]:
        """Run one instance in workspace; returns a prediction dict."""


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
    runner: InstanceRunner | None = None

    def __post_init__(self) -> None:
        self._completed: set[str] = set()
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

    def run(self, instances: list[dict[str, Any]]) -> dict[str, Any]:
        summary: dict[str, Any] = {
            "run_id": self.run_id,
            "total": len(instances),
            "completed": 0,
            "resumed_skipped": 0,
            "by_category": {ERROR_TIMEOUT: 0, ERROR_OOM: 0, ERROR_INFRA: 0, ERROR_AGENT: 0, ERROR_SCORER: 0},
        }
        for instance in instances:
            instance_id = str(instance.get("instance_id", ""))
            if not instance_id:
                continue
            if instance_id in self._completed:
                summary["resumed_skipped"] += 1
                self.artifacts.record_event(instance_id, "skipped-resume")
                continue

            workspace = Path(tempfile.mkdtemp(prefix=f"eval-{self.run_id}-"))
            usage = BudgetUsage()
            try:
                if not self.network_allowed:
                    _block_network(workspace)
                prediction = self.runner(instance, workspace, usage) if self.runner else {}
                check_budget(self.budget, usage)
                self.artifacts.record_prediction({"instance_id": instance_id, **prediction})
                self.artifacts.record_event(instance_id, "completed")
                self._mark_completed(instance_id)
                summary["completed"] += 1
            except BaseException as exc:  # noqa: BLE001 - classify and record
                category = classify_error(exc)
                summary["by_category"][category] += 1
                self.artifacts.record_failure(instance_id, category, str(exc)[:500])
                self.artifacts.record_event(instance_id, f"failed-{category}")
            finally:
                shutil.rmtree(workspace, ignore_errors=True)

        summary["ok"] = summary["completed"]
        summary["failed"] = sum(summary["by_category"].values())
        summary["skipped"] = summary["resumed_skipped"]
        path = self.artifacts.write_summary(summary)
        return {"summary": summary, "summary_path": str(path)}


def _block_network(workspace: Path) -> None:
    """Best-effort network block for the instance workspace on Windows.

    Full sandboxing is the benchmark's Docker/container responsibility; this
    marks the run as network-disabled and records the intent in the
    workspace so infra failures are distinguishable.
    """
    (workspace / "NETWORK_DISABLED").write_text("network allowlist not granted\n", encoding="utf-8")
