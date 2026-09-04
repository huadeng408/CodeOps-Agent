from __future__ import annotations

import asyncio
import time
from pathlib import Path

from orchestrator.workflows import (
    SQLiteWorkflowStore,
    WorkerResult,
    WorkerSpec,
    WorkerState,
    WorkflowEngine,
    WorkflowSpec,
)


def test_pipeline_fills_a_freed_parallel_slot_before_slow_worker_finishes(tmp_path: Path) -> None:
    started: dict[str, float] = {}
    finished: dict[str, float] = {}

    async def execute(worker: WorkerSpec, _upstream: dict[str, WorkerResult]) -> WorkerResult:
        started[worker.id] = time.monotonic()
        if worker.id == "slow":
            await asyncio.sleep(0.2)
        else:
            await asyncio.sleep(0.01)
        finished[worker.id] = time.monotonic()
        return WorkerResult.completed(worker.id, worker.provider, worker.id)

    spec = WorkflowSpec(
        id="dynamic-pipeline",
        workers=[
            WorkerSpec(id="slow", title="Slow", objective="hold one slot"),
            WorkerSpec(id="fast", title="Fast", objective="free one slot"),
            WorkerSpec(id="dependent", title="Dependent", objective="start after fast", depends_on=("fast",)),
        ],
    )
    store = SQLiteWorkflowStore(tmp_path / "dynamic-pipeline.sqlite")
    try:
        result = asyncio.run(WorkflowEngine(store, execute, max_concurrency=2).run(spec))
    finally:
        store.close()

    assert result.state == "completed"
    assert result.workers["dependent"].state is WorkerState.COMPLETED
    assert started["dependent"] < finished["slow"]
