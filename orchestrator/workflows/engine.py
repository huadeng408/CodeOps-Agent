from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import replace

from .models import WorkerResult, WorkerSpec, WorkerState, WorkflowRun, WorkflowSpec
from .store import SQLiteWorkflowStore

WorkerExecutor = Callable[[WorkerSpec, dict[str, WorkerResult]], Awaitable[WorkerResult]]


class WorkflowEngine:
    def __init__(self, store: SQLiteWorkflowStore, execute_worker: WorkerExecutor, max_concurrency: int = 4) -> None:
        if max_concurrency <= 0:
            raise ValueError("max_concurrency must be positive")
        self._store = store
        self._execute_worker = execute_worker
        self._max_concurrency = max_concurrency
        self._recovered_worker_ids: list[str] = []

    async def run(self, spec: WorkflowSpec) -> WorkflowRun:
        workers = self._validate(spec)
        checkpoint = self._store.load(spec.id)
        run = self._resume_or_create(spec, checkpoint)
        self._store.save(run, detail="workflow resumed" if checkpoint else "workflow started")
        for worker_id in self._recovered_worker_ids:
            self._store.save(run, worker_id, "worker recovered from previous process")

        while True:
            self._block_workers_with_failed_dependencies(run, workers)
            ready = [
                worker
                for worker in workers.values()
                if run.workers[worker.id].state is WorkerState.PENDING
                and all(run.workers[dependency].state is WorkerState.COMPLETED for dependency in worker.depends_on)
            ]
            if not ready:
                if all(result.state in {WorkerState.COMPLETED, WorkerState.FAILED, WorkerState.BLOCKED} for result in run.workers.values()):
                    run.state = "completed" if all(result.state is WorkerState.COMPLETED for result in run.workers.values()) else "partial_failure"
                    self._store.save(run, detail="workflow terminal")
                    return run
                raise RuntimeError(f"workflow {spec.id} cannot make progress")

            batch = ready[: self._max_concurrency]
            for worker in batch:
                prior = run.workers[worker.id]
                run.workers[worker.id] = replace(prior, state=WorkerState.RUNNING, attempts=prior.attempts + 1, error="")
                self._store.save(run, worker.id, "worker started")

            tasks = {
                worker.id: asyncio.create_task(
                    self._execute_worker(worker, {dependency: run.workers[dependency] for dependency in worker.depends_on})
                )
                for worker in batch
            }
            checkpointed: set[str] = set()

            def checkpoint_completed(worker: WorkerSpec, task: asyncio.Task[WorkerResult]) -> None:
                if task.cancelled() or task.exception() is not None:
                    return
                outcome = task.result()
                if outcome.state is not WorkerState.COMPLETED:
                    return
                prior = run.workers[worker.id]
                run.workers[worker.id] = self._completed_result(worker, outcome, prior.attempts)
                self._store.save(run, worker.id, "worker completed")
                checkpointed.add(worker.id)

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
            elif existing.state is WorkerState.RUNNING:
                checkpoint.workers[worker.id] = replace(existing, state=WorkerState.PENDING)
                self._recovered_worker_ids.append(worker.id)
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
