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


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--db", required=True)
    parser.add_argument("--calls", required=True)
    parser.add_argument("--wait", action="store_true")
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
        with calls_path.open("a", encoding="utf-8") as stream:
            stream.write(worker.id + "\n")
            stream.flush()
        if worker.id == "long" and args.wait:
            await asyncio.sleep(120)
        return WorkerResult.completed(worker.id, worker.provider, worker.id + " complete")

    store = SQLiteWorkflowStore(args.db)
    try:
        result = await WorkflowEngine(store, execute, max_concurrency=2).run(build_spec())
        print("WORKFLOW_COMPLETED", result.to_dict(), flush=True)
    finally:
        store.close()


if __name__ == "__main__":
    asyncio.run(run(parse_args()))
