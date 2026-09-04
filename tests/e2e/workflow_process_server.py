from __future__ import annotations

import argparse
import asyncio
from pathlib import Path

from orchestrator.workflows import (
    SQLiteWorkflowStore,
    WorkerResult,
    WorkerSpec,
    WorkflowEngine,
    WorkflowSpec,
)


def _record_call(path: Path, worker_id: str) -> None:
    with path.open("a", encoding="utf-8") as stream:
        stream.write(worker_id + "\n")
        stream.flush()


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", required=True)
    parser.add_argument("--calls", required=True)
    parser.add_argument("--wait", action="store_true")
    parser.add_argument("--lease-ttl", type=float, default=60.0)
    parser.add_argument("--delay-ms", type=float, default=0.0)
    return parser.parse_args()


def build_spec() -> WorkflowSpec:
    return WorkflowSpec(
        id="workflow-process-e2e",
        workers=[
            WorkerSpec(id="done", title="Done", objective="complete quickly"),
            WorkerSpec(
                id="long",
                title="Long",
                objective="survive process recovery",
                max_attempts=2,
            ),
            WorkerSpec(
                id="final",
                title="Final",
                objective="consume recovered outputs",
                depends_on=("done", "long"),
            ),
        ],
    )


async def run(args: argparse.Namespace) -> None:
    calls_path = Path(args.calls)

    async def execute(worker: WorkerSpec, _upstream: dict[str, WorkerResult]) -> WorkerResult:
        await asyncio.to_thread(_record_call, calls_path, worker.id)
        if worker.id == "long" and args.wait:
            await asyncio.sleep(120)
        elif args.delay_ms > 0:
            await asyncio.sleep(args.delay_ms / 1000.0)
        return WorkerResult.completed(worker.id, worker.provider, worker.id + " complete")

    store = SQLiteWorkflowStore(args.db)
    try:
        result = await WorkflowEngine(
            store,
            execute,
            max_concurrency=2,
            lease_ttl_seconds=args.lease_ttl,
        ).run(build_spec())
        print("WORKFLOW_COMPLETED", result.to_dict(), flush=True)
    finally:
        store.close()


if __name__ == "__main__":
    asyncio.run(run(parse_args()))
