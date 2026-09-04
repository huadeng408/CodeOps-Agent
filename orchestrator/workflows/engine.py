from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import replace
from uuid import uuid4

from .models import WorkerResult, WorkerSpec, WorkerState, WorkflowRun, WorkflowSpec
from .store import SQLiteWorkflowStore, WorkerLease

WorkerExecutor = Callable[[WorkerSpec, dict[str, WorkerResult]], Awaitable[WorkerResult]]


class LeaseLostError(RuntimeError):
    """Raised when a worker can no longer prove ownership of its lease."""


class WorkflowEngine:
    def __init__(
        self,
        store: SQLiteWorkflowStore,
        execute_worker: WorkerExecutor,
        max_concurrency: int = 4,
        *,
        owner_id: str | None = None,
        lease_ttl_seconds: float = 60.0,
        lease_poll_interval_seconds: float = 0.05,
    ) -> None:
        if max_concurrency <= 0:
            raise ValueError("max_concurrency must be positive")
        if lease_ttl_seconds <= 0:
            raise ValueError("lease ttl must be positive")
        if lease_poll_interval_seconds <= 0:
            raise ValueError("lease poll interval must be positive")
        self._store = store
        self._execute_worker = execute_worker
        self._max_concurrency = max_concurrency
        self._owner_id = owner_id.strip() if owner_id and owner_id.strip() else f"workflow-{uuid4().hex}"
        self._lease_ttl_seconds = float(lease_ttl_seconds)
        self._lease_poll_interval_seconds = float(lease_poll_interval_seconds)
        self._recovered_worker_ids: list[str] = []

    async def run(self, spec: WorkflowSpec) -> WorkflowRun:
        workers = self._validate(spec)
        self._store.reap_expired_leases()
        checkpoint = self._store.load(spec.id)
        run = self._resume_or_create(spec, checkpoint)
        if checkpoint is None:
            self._store.save(run, detail="workflow started")
        else:
            self._store.record_event(spec.id, "", run.state, "workflow resumed")

        while True:
            run = self._refresh_run(run, workers)
            self._block_workers_with_failed_dependencies(run, workers)
            candidates = [
                worker
                for worker in workers.values()
                if run.workers[worker.id].state is WorkerState.PENDING
                and all(run.workers[dependency].state is WorkerState.COMPLETED for dependency in worker.depends_on)
            ]
            if not candidates:
                if all(result.state in {WorkerState.COMPLETED, WorkerState.FAILED, WorkerState.BLOCKED} for result in run.workers.values()):
                    run.state = "completed" if all(result.state is WorkerState.COMPLETED for result in run.workers.values()) else "partial_failure"
                    self._store.save(run, detail="workflow terminal")
                    return run
                await asyncio.sleep(self._lease_poll_interval_seconds)
                continue

            claimed: list[tuple[WorkerSpec, WorkerLease]] = []
            for worker in candidates:
                lease = self._store.acquire_lease(
                    spec.id,
                    worker.id,
                    self._owner_id,
                    self._lease_ttl_seconds,
                )
                if lease is not None:
                    claimed.append((worker, lease))
                if len(claimed) >= self._max_concurrency:
                    break
            if not claimed:
                await asyncio.sleep(self._lease_poll_interval_seconds)
                continue

            batch = [worker for worker, _lease in claimed]
            leases = {worker.id: lease for worker, lease in claimed}
            for worker in batch:
                prior = run.workers[worker.id]
                run.workers[worker.id] = replace(prior, state=WorkerState.RUNNING, attempts=prior.attempts + 1, error="")
                self._store.save(run, worker.id, "worker started")

            tasks = {
                worker.id: asyncio.create_task(
                    self._execute_with_lease(
                        worker,
                        {dependency: run.workers[dependency] for dependency in worker.depends_on},
                        leases[worker.id],
                    )
                )
                for worker in batch
            }
            checkpointed: set[str] = set()

            def checkpoint_completed(
                worker: WorkerSpec,
                task: asyncio.Task[WorkerResult],
                current_run: WorkflowRun = run,
                current_leases: dict[str, WorkerLease] = leases,
                current_checkpointed: set[str] = checkpointed,
            ) -> None:
                if task.cancelled() or task.exception() is not None:
                    return
                outcome = task.result()
                if outcome.state is not WorkerState.COMPLETED:
                    return
                prior = current_run.workers[worker.id]
                current_run.workers[worker.id] = self._completed_result(worker, outcome, prior.attempts)
                self._store.save(current_run, worker.id, "worker completed")
                self._store.release_lease(
                    spec.id, worker.id, self._owner_id, current_leases[worker.id].token
                )
                current_checkpointed.add(worker.id)

            for worker in batch:
                tasks[worker.id].add_done_callback(
                    lambda task, current=worker: checkpoint_completed(current, task)
                )
            try:
                outcomes = await asyncio.gather(*tasks.values(), return_exceptions=True)
            except asyncio.CancelledError:
                for worker_id, task in tasks.items():
                    if worker_id in checkpointed:
                        continue
                    if task.done() and not task.cancelled() and task.exception() is None:
                        run.workers[worker_id] = self._completed_result(
                            workers[worker_id], task.result(), run.workers[worker_id].attempts
                        )
                    else:
                        prior = run.workers[worker_id]
                        run.workers[worker_id] = replace(prior, state=WorkerState.PENDING)
                    self._store.release_lease(spec.id, worker_id, self._owner_id, leases[worker_id].token)
                self._store.save(run, detail="workflow interrupted")
                raise

            for worker, outcome in zip(batch, outcomes, strict=True):
                if worker.id in checkpointed:
                    continue
                prior = run.workers[worker.id]
                if isinstance(outcome, BaseException):
                    if isinstance(outcome, asyncio.CancelledError):
                        run.workers[worker.id] = replace(prior, state=WorkerState.PENDING)
                        self._store.save(run, worker.id, "worker interrupted")
                        self._store.release_lease(spec.id, worker.id, self._owner_id, leases[worker.id].token)
                        raise outcome
                    if prior.attempts < worker.max_attempts:
                        run.workers[worker.id] = replace(
                            prior,
                            state=WorkerState.PENDING,
                            error=str(outcome),
                        )
                        self._store.save(run, worker.id, "worker retry scheduled")
                    else:
                        run.workers[worker.id] = WorkerResult.failed(worker.id, worker.provider, str(outcome), prior.attempts)
                        self._store.save(run, worker.id, "worker failed")
                    self._store.release_lease(spec.id, worker.id, self._owner_id, leases[worker.id].token)
                    continue
                if outcome.state is WorkerState.FAILED and prior.attempts < worker.max_attempts:
                    run.workers[worker.id] = replace(
                        prior,
                        state=WorkerState.PENDING,
                        error=outcome.error,
                    )
                    self._store.save(run, worker.id, "worker retry scheduled")
                else:
                    run.workers[worker.id] = self._completed_result(worker, outcome, prior.attempts)
                    self._store.save(run, worker.id, "worker completed" if outcome.state is WorkerState.COMPLETED else "worker failed")
                self._store.release_lease(spec.id, worker.id, self._owner_id, leases[worker.id].token)

    async def _execute_with_lease(
        self,
        worker: WorkerSpec,
        upstream: dict[str, WorkerResult],
        lease: WorkerLease,
    ) -> WorkerResult:
        execution = asyncio.create_task(self._execute_worker(worker, upstream))
        heartbeat = asyncio.create_task(self._heartbeat(lease))
        try:
            done, _pending = await asyncio.wait(
                {execution, heartbeat}, return_when=asyncio.FIRST_COMPLETED
            )
            if heartbeat in done:
                execution.cancel()
                await asyncio.gather(execution, return_exceptions=True)
                raise LeaseLostError(f"lease lost for worker {worker.id}")
            result = await execution
            if not self._store.renew_lease(
                lease.workflow_id,
                lease.worker_id,
                lease.owner_id,
                lease.token,
                self._lease_ttl_seconds,
            ):
                raise LeaseLostError(f"lease lost for worker {worker.id}")
            return result
        finally:
            if not heartbeat.done():
                heartbeat.cancel()
            await asyncio.gather(heartbeat, return_exceptions=True)

    async def _heartbeat(self, lease: WorkerLease) -> bool:
        interval = max(min(self._lease_ttl_seconds / 3.0, 1.0), 0.01)
        while True:
            await asyncio.sleep(interval)
            if not self._store.renew_lease(
                lease.workflow_id,
                lease.worker_id,
                lease.owner_id,
                lease.token,
                self._lease_ttl_seconds,
            ):
                return False

    def _refresh_run(self, run: WorkflowRun, workers: Mapping[str, WorkerSpec]) -> WorkflowRun:
        latest = self._store.load(run.id)
        if latest is None:
            return run
        for worker in workers.values():
            current = latest.workers.get(worker.id)
            if current is not None and current.state is WorkerState.RUNNING and self._store.get_lease(run.id, worker.id) is None:
                latest.workers[worker.id] = replace(current, state=WorkerState.PENDING)
                self._store.save(latest, worker.id, "worker recovered from previous process")
        return latest

    def _resume_or_create(self, spec: WorkflowSpec, checkpoint: WorkflowRun | None = None) -> WorkflowRun:
        self._recovered_worker_ids = []
        if checkpoint is None:
            return WorkflowRun(
                id=spec.id,
                workers={worker.id: WorkerResult(id=worker.id, provider=worker.provider) for worker in spec.workers},
            )
        for worker in spec.workers:
            existing = checkpoint.workers.get(worker.id)
            if existing is None or existing.provider != worker.provider:
                checkpoint.workers[worker.id] = WorkerResult(id=worker.id, provider=worker.provider)
            elif existing.state is WorkerState.RUNNING and self._store.get_lease(spec.id, worker.id) is None:
                checkpoint.workers[worker.id] = replace(existing, state=WorkerState.PENDING)
                self._recovered_worker_ids.append(worker.id)
                self._store.save(checkpoint, worker.id, "worker recovered from previous process")
            elif existing.state is WorkerState.FAILED and existing.attempts < worker.max_attempts:
                checkpoint.workers[worker.id] = replace(existing, state=WorkerState.PENDING)
        return checkpoint

    def _block_workers_with_failed_dependencies(self, run: WorkflowRun, workers: Mapping[str, WorkerSpec]) -> None:
        for worker in workers.values():
            current = run.workers[worker.id]
            if current.state is not WorkerState.PENDING:
                continue
            failed = [dependency for dependency in worker.depends_on if run.workers[dependency].state in {WorkerState.FAILED, WorkerState.BLOCKED}]
            if failed:
                run.workers[worker.id] = WorkerResult.blocked(
                    worker.id, worker.provider, "dependency did not complete: " + ", ".join(failed)
                )
                self._store.save(run, worker.id, "worker blocked")

    @staticmethod
    def _completed_result(worker: WorkerSpec, outcome: WorkerResult, attempts: int) -> WorkerResult:
        if outcome.state is not WorkerState.COMPLETED:
            return WorkerResult(
                id=worker.id,
                provider=worker.provider,
                state=outcome.state,
                output=outcome.output,
                error=outcome.error,
                attempts=attempts,
            )
        return WorkerResult(
            id=worker.id,
            provider=worker.provider,
            state=WorkerState.COMPLETED,
            output=outcome.output,
            error="",
            attempts=attempts,
        )

    @staticmethod
    def _validate(spec: WorkflowSpec) -> dict[str, WorkerSpec]:
        if not spec.id.strip():
            raise ValueError("workflow id must not be empty")
        workers: dict[str, WorkerSpec] = {}
        for worker in spec.workers:
            if not worker.id.strip() or not worker.title.strip() or not worker.objective.strip():
                raise ValueError("worker id, title, and objective must not be empty")
            if worker.id in workers:
                raise ValueError(f"duplicate worker id: {worker.id}")
            workers[worker.id] = worker
        if not workers:
            raise ValueError("workflow requires at least one worker")
        for worker in workers.values():
            unknown = [dependency for dependency in worker.depends_on if dependency not in workers]
            if unknown:
                raise ValueError(f"worker {worker.id} has unknown dependencies: {', '.join(unknown)}")
        visiting: set[str] = set()
        visited: set[str] = set()

        def visit(worker_id: str) -> None:
            if worker_id in visiting:
                raise ValueError("workflow dependencies contain a cycle")
            if worker_id in visited:
                return
            visiting.add(worker_id)
            for dependency in workers[worker_id].depends_on:
                visit(dependency)
            visiting.remove(worker_id)
            visited.add(worker_id)

        for worker_id in workers:
            visit(worker_id)
        return workers
