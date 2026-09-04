from __future__ import annotations

import asyncio
from pathlib import Path

from orchestrator.workflows import (
    SQLiteWorkflowStore,
    WorkerResult,
    WorkerSpec,
    WorkerState,
    WorkflowEngine,
    WorkflowSpec,
)


def test_workflow_lease_is_exclusive_renewable_and_reclaimable(tmp_path: Path) -> None:
    store = SQLiteWorkflowStore(tmp_path / "leases.sqlite")
    try:
        first = store.acquire_lease("workflow", "worker", "owner-a", ttl_seconds=10, now=100.0)
        assert first is not None
        assert first.owner_id == "owner-a"
        assert store.acquire_lease("workflow", "worker", "owner-b", ttl_seconds=10, now=101.0) is None
        assert not store.renew_lease("workflow", "worker", "owner-b", first.token, ttl_seconds=10, now=102.0)
        assert store.renew_lease("workflow", "worker", "owner-a", first.token, ttl_seconds=10, now=102.0)
        assert store.release_lease("workflow", "worker", "owner-b", first.token) is False
        assert store.release_lease("workflow", "worker", "owner-a", first.token) is True

        reclaimed = store.acquire_lease("workflow", "worker", "owner-b", ttl_seconds=10, now=103.0)
        assert reclaimed is not None
        assert reclaimed.owner_id == "owner-b"
    finally:
        store.close()


def test_workflow_lease_can_be_claimed_after_expiry(tmp_path: Path) -> None:
    store = SQLiteWorkflowStore(tmp_path / "leases-expiry.sqlite")
    try:
        first = store.acquire_lease("workflow", "worker", "owner-a", ttl_seconds=5, now=100.0)
        assert first is not None
        reclaimed = store.acquire_lease("workflow", "worker", "owner-b", ttl_seconds=5, now=105.001)
        assert reclaimed is not None
        assert reclaimed.owner_id == "owner-b"
    finally:
        store.close()


def test_two_workflow_engines_do_not_duplicate_a_claimed_worker(tmp_path: Path) -> None:
    calls: list[str] = []

    async def execute(worker: WorkerSpec, _upstream: dict[str, WorkerResult]) -> WorkerResult:
        calls.append(worker.id)
        await asyncio.sleep(0.05)
        return WorkerResult.completed(worker.id, worker.provider, "done")

    spec = WorkflowSpec(id="shared-lease", workers=[WorkerSpec(id="worker", title="Worker", objective="run")])

    async def run_both():
        first_store = SQLiteWorkflowStore(tmp_path / "shared.sqlite")
        second_store = SQLiteWorkflowStore(tmp_path / "shared.sqlite")
        try:
            first = WorkflowEngine(first_store, execute, owner_id="engine-a", lease_ttl_seconds=1)
            second = WorkflowEngine(second_store, execute, owner_id="engine-b", lease_ttl_seconds=1)
            return await asyncio.gather(first.run(spec), second.run(spec))
        finally:
            first_store.close()
            second_store.close()

    results = asyncio.run(run_both())
    assert calls == ["worker"]
    assert all(result.workers["worker"].state is WorkerState.COMPLETED for result in results)


def test_worker_is_failed_closed_when_lease_is_revoked_during_execution(tmp_path: Path) -> None:
    calls: list[str] = []

    async def execute(worker: WorkerSpec, _upstream: dict[str, WorkerResult]) -> WorkerResult:
        calls.append(worker.id)
        await asyncio.sleep(0.2)
        return WorkerResult.completed(worker.id, worker.provider, "must not commit")

    async def run_and_revoke() -> WorkerState:
        engine_store = SQLiteWorkflowStore(tmp_path / "revoked.sqlite")
        revoker_store = SQLiteWorkflowStore(tmp_path / "revoked.sqlite")
        try:
            engine = WorkflowEngine(
                engine_store,
                execute,
                owner_id="engine",
                lease_ttl_seconds=0.06,
                lease_poll_interval_seconds=0.01,
            )
            spec = WorkflowSpec(id="revoked", workers=[WorkerSpec(id="worker", title="Worker", objective="run")])

            async def revoke() -> None:
                for _ in range(20):
                    await asyncio.sleep(0.01)
                    lease = revoker_store.get_lease("revoked", "worker")
                    if lease is not None:
                        assert revoker_store.release_lease("revoked", "worker", lease.owner_id, lease.token)
                        return
                raise AssertionError("worker lease was never acquired")

            result, _ = await asyncio.gather(engine.run(spec), revoke())
            return result.workers["worker"].state
        finally:
            engine_store.close()
            revoker_store.close()

    assert asyncio.run(run_and_revoke()) is WorkerState.FAILED
    assert calls == ["worker"]
