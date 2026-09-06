from __future__ import annotations

import asyncio
import json
from pathlib import Path

import pytest

from orchestrator.workflows import (
    ProviderWorkerExecutor,
    SQLiteWorkflowStore,
    WorkerResult,
    WorkerSpec,
    WorkerState,
    WorkflowRun,
    WorkflowEngine,
    WorkflowSpec,
)
from orchestrator.llm.client import ChatResponse, ToolCall
from orchestrator.llm.router import ModelInfo, ProviderRouter
from orchestrator.context import TokenBudget
from orchestrator.graph.main_graph import build_graph
from orchestrator.memory.manager import MemoryManager
from orchestrator.runtime.conversation import ConversationRunner
from orchestrator.runtime.tools import ToolRegistry
from orchestrator.skills.manager import SkillManager
from orchestrator.todo.manager import TodoManager


def test_runs_ready_workers_in_parallel_across_providers(tmp_path: Path) -> None:
    active = 0
    peak_active = 0
    providers: list[str] = []

    async def execute(worker: WorkerSpec, _upstream: dict[str, WorkerResult]) -> WorkerResult:
        nonlocal active, peak_active
        providers.append(worker.provider)
        active += 1
        peak_active = max(peak_active, active)
        await asyncio.sleep(0.02)
        active -= 1
        return WorkerResult.completed(worker.id, worker.provider, f"{worker.provider}:{worker.id}")

    engine = WorkflowEngine(SQLiteWorkflowStore(tmp_path / "workflows.sqlite"), execute, max_concurrency=2)
    result = asyncio.run(
        engine.run(
            WorkflowSpec(
                id="providers-parallel",
                workers=[
                    WorkerSpec(id="analysis", title="Analyze", objective="inspect", provider="openai"),
                    WorkerSpec(id="review", title="Review", objective="review", provider="anthropic"),
                ],
            )
        )
    )

    assert peak_active == 2
    assert providers == ["openai", "anthropic"]
    assert {item.state for item in result.workers.values()} == {WorkerState.COMPLETED}


def test_provider_worker_executor_selects_the_named_llm_client() -> None:
    class FakeProvider:
        def __init__(self, name: str) -> None:
            self.name = name
            self.requests = []

        async def chat(self, request):
            self.requests.append(request)
            return ChatResponse(text=self.name + " response")

    openai = FakeProvider("openai")
    anthropic = FakeProvider("anthropic")
    executor = ProviderWorkerExecutor({"openai": openai, "anthropic": anthropic})

    result = asyncio.run(
        executor(
            WorkerSpec(id="review", title="Review", objective="review", provider="anthropic"),
            {},
        )
    )

    assert result.output == "anthropic response"
    assert len(openai.requests) == 0
    assert len(anthropic.requests) == 1


@pytest.mark.parametrize("response", [ChatResponse(text=""), ChatResponse(tool_calls=[ToolCall(id="call-1", name="Read", arguments={})])])
def test_provider_worker_executor_rejects_non_final_response(response) -> None:
    class FakeProvider:
        async def chat(self, request):
            return response

    executor = ProviderWorkerExecutor({"default": FakeProvider()})
    with pytest.raises(RuntimeError, match="empty or tool-only"):
        asyncio.run(
            executor(
                WorkerSpec(id="worker", title="Worker", objective="produce a result"),
                {},
            )
        )


def test_provider_worker_executor_uses_router_model_selection() -> None:
    class FakeProvider:
        model = "wire-default"

        def __init__(self) -> None:
            self.requests = []

        async def chat(self, request):
            self.requests.append(request)
            return ChatResponse(text="routed")

    provider = FakeProvider()
    router = ProviderRouter()
    router.register(
        "relay",
        provider,
        models=[ModelInfo(provider="relay", id="pinned-model")],
    )
    executor = ProviderWorkerExecutor(router)

    result = asyncio.run(
        executor(
            WorkerSpec(
                id="review",
                title="Review",
                objective="review",
                provider="relay",
                context={"model": "pinned-model"},
            ),
            {},
        )
    )

    assert result.output == "routed"
    assert provider.requests[0].model == "pinned-model"


def test_runs_pipeline_after_dependency_output_is_checkpointed(tmp_path: Path) -> None:
    execution_order: list[str] = []

    async def execute(worker: WorkerSpec, upstream: dict[str, WorkerResult]) -> WorkerResult:
        execution_order.append(worker.id)
        if worker.id == "implement":
            assert upstream["plan"].output == "implementation plan"
        output = "implementation plan" if worker.id == "plan" else "patch ready"
        return WorkerResult.completed(worker.id, worker.provider, output)

    store = SQLiteWorkflowStore(tmp_path / "workflows.sqlite")
    engine = WorkflowEngine(store, execute)
    result = asyncio.run(
        engine.run(
            WorkflowSpec(
                id="pipeline",
                workers=[
                    WorkerSpec(id="plan", title="Plan", objective="plan", provider="openai"),
                    WorkerSpec(
                        id="implement",
                        title="Implement",
                        objective="change",
                        provider="local",
                        depends_on=("plan",),
                    ),
                ],
            )
        )
    )

    assert execution_order == ["plan", "implement"]
    assert result.workers["implement"].state is WorkerState.COMPLETED
    assert store.load("pipeline").workers["plan"].output == "implementation plan"


def test_resumes_from_checkpoint_without_repeating_completed_workers(tmp_path: Path) -> None:
    calls: list[str] = []
    interrupt = True

    async def execute(worker: WorkerSpec, _upstream: dict[str, WorkerResult]) -> WorkerResult:
        nonlocal interrupt
        calls.append(worker.id)
        if worker.id == "second" and interrupt:
            interrupt = False
            raise asyncio.CancelledError()
        return WorkerResult.completed(worker.id, worker.provider, worker.id + " complete")

    store = SQLiteWorkflowStore(tmp_path / "workflows.sqlite")
    spec = WorkflowSpec(
        id="resume",
        workers=[
            WorkerSpec(id="first", title="First", objective="first", provider="openai"),
            WorkerSpec(id="second", title="Second", objective="second", provider="anthropic", depends_on=("first",)),
        ],
    )

    with pytest.raises(asyncio.CancelledError):
        asyncio.run(WorkflowEngine(store, execute).run(spec))

    result = asyncio.run(WorkflowEngine(store, execute).run(spec))

    assert calls == ["first", "second", "second"]
    assert result.workers["first"].state is WorkerState.COMPLETED
    assert result.workers["second"].state is WorkerState.COMPLETED


def test_records_running_worker_recovery_before_resuming(tmp_path: Path) -> None:
    calls: list[str] = []
    store = SQLiteWorkflowStore(tmp_path / "workflows.sqlite")
    spec = WorkflowSpec(
        id="running-recovery",
        workers=[WorkerSpec(id="worker", title="Worker", objective="resume")],
    )
    store.save(
        WorkflowRun(
            id=spec.id,
            workers={
                "worker": WorkerResult(
                    id="worker",
                    provider="default",
                    state=WorkerState.RUNNING,
                    attempts=1,
                )
            },
        ),
        "worker",
        "worker started",
    )

    async def execute(worker: WorkerSpec, _upstream: dict[str, WorkerResult]) -> WorkerResult:
        calls.append(worker.id)
        return WorkerResult.completed(worker.id, worker.provider, "resumed")

    result = asyncio.run(WorkflowEngine(store, execute).run(spec))

    assert calls == ["worker"]
    assert result.workers["worker"].state is WorkerState.COMPLETED
    assert any(
        worker_id == "worker" and detail == "worker recovered from previous process"
        for _sequence, worker_id, _state, detail in store.events("running-recovery")
    )


def test_isolates_failed_worker_and_blocks_only_its_dependents(tmp_path: Path) -> None:
    async def execute(worker: WorkerSpec, _upstream: dict[str, WorkerResult]) -> WorkerResult:
        if worker.id == "broken":
            raise RuntimeError("provider unavailable")
        return WorkerResult.completed(worker.id, worker.provider, worker.id + " complete")

    engine = WorkflowEngine(SQLiteWorkflowStore(tmp_path / "workflows.sqlite"), execute, max_concurrency=3)
    result = asyncio.run(
        engine.run(
            WorkflowSpec(
                id="failure-isolation",
                workers=[
                    WorkerSpec(id="broken", title="Broken", objective="fail", provider="openai"),
                    WorkerSpec(id="independent", title="Independent", objective="continue", provider="anthropic"),
                    WorkerSpec(
                        id="dependent",
                        title="Dependent",
                        objective="must not run",
                        provider="local",
                        depends_on=("broken",),
                    ),
                ],
            )
        )
    )

    assert result.workers["broken"].state is WorkerState.FAILED
    assert result.workers["independent"].state is WorkerState.COMPLETED
    assert result.workers["dependent"].state is WorkerState.BLOCKED
    assert "provider unavailable" in result.workers["broken"].error


def test_isolates_worker_that_returns_an_explicit_failure_result(tmp_path: Path) -> None:
    async def execute(worker: WorkerSpec, _upstream: dict[str, WorkerResult]) -> WorkerResult:
        if worker.id == "broken":
            return WorkerResult.failed(worker.id, worker.provider, "validation failed")
        return WorkerResult.completed(worker.id, worker.provider, worker.id + " complete")

    result = asyncio.run(
        WorkflowEngine(SQLiteWorkflowStore(tmp_path / "workflows.sqlite"), execute).run(
            WorkflowSpec(
                id="explicit-failure",
                workers=[
                    WorkerSpec(id="broken", title="Broken", objective="fail"),
                    WorkerSpec(
                        id="dependent",
                        title="Dependent",
                        objective="must not run",
                        depends_on=("broken",),
                    ),
                ],
            )
        )
    )

    assert result.workers["broken"].state is WorkerState.FAILED
    assert result.workers["broken"].error == "validation failed"
    assert result.workers["dependent"].state is WorkerState.BLOCKED


def test_retries_transient_worker_failure_within_attempt_budget(tmp_path: Path) -> None:
    calls: list[str] = []

    async def execute(worker: WorkerSpec, _upstream: dict[str, WorkerResult]) -> WorkerResult:
        calls.append(worker.id)
        if len(calls) == 1:
            raise RuntimeError("transient provider failure")
        return WorkerResult.completed(worker.id, worker.provider, "recovered")

    result = asyncio.run(
        WorkflowEngine(SQLiteWorkflowStore(tmp_path / "workflows.sqlite"), execute).run(
            WorkflowSpec(
                id="retry-budget",
                workers=[
                    WorkerSpec(
                        id="worker",
                        title="Worker",
                        objective="retry",
                        max_attempts=2,
                    )
                ],
            )
        )
    )

    assert calls == ["worker", "worker"]
    assert result.workers["worker"].state is WorkerState.COMPLETED
    assert result.workers["worker"].attempts == 2


def test_does_not_retry_after_attempt_budget_is_exhausted(tmp_path: Path) -> None:
    calls: list[str] = []

    async def execute(worker: WorkerSpec, _upstream: dict[str, WorkerResult]) -> WorkerResult:
        calls.append(worker.id)
        raise RuntimeError("permanent provider failure")

    result = asyncio.run(
        WorkflowEngine(SQLiteWorkflowStore(tmp_path / "workflows.sqlite"), execute).run(
            WorkflowSpec(
                id="retry-exhausted",
                workers=[
                    WorkerSpec(
                        id="worker",
                        title="Worker",
                        objective="fail",
                        max_attempts=2,
                    )
                ],
            )
        )
    )

    assert calls == ["worker", "worker"]
    assert result.workers["worker"].state is WorkerState.FAILED
    assert result.workers["worker"].attempts == 2


def test_conversation_exposes_and_runs_persisted_provider_workflow(tmp_path: Path) -> None:
    class FakeProvider:
        model = "fake-model"

        async def chat(self, request):
            return ChatResponse(text="completed " + request.messages[0].content.splitlines()[0])

    runner = ConversationRunner(
        graph=build_graph(),
        llm=FakeProvider(),
        tool_registry=ToolRegistry(str(tmp_path)),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        token_budget=TokenBudget(),
        provider_clients={"openai": FakeProvider(), "anthropic": FakeProvider()},
    )

    workflow = runner._decode_workflow(
        json.dumps(
            {
            "id": "conversation-workflow",
            "workers": [
                {"id": "research", "title": "Research", "objective": "inspect", "provider": "openai"},
                {
                    "id": "review",
                    "title": "Review",
                    "objective": "review",
                    "provider": "anthropic",
                    "depends_on": ["research"],
                },
            ],
            }
        )
    )
    result = runner._run_workflow(workflow)

    assert runner.tool_registry.get("RunWorkflow") is not None
    assert result["state"] == "completed"
    assert result["workers"]["research"]["provider"] == "openai"
    assert result["workers"]["review"]["state"] == "completed"
    assert (tmp_path / ".agent" / "workflows.sqlite").exists()


def test_conversation_workflow_decoder_preserves_attempt_budget(tmp_path: Path) -> None:
    runner = ConversationRunner(
        graph=build_graph(),
        llm=None,
        tool_registry=ToolRegistry(),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
    )

    workflow = runner._decode_workflow(
        json.dumps(
            {
                "id": "attempt-budget-decoder",
                "workers": [
                    {
                        "id": "worker",
                        "title": "Worker",
                        "objective": "retry",
                        "max_attempts": 3,
                    }
                ],
            }
        )
    )

    assert workflow.workers[0].max_attempts == 3
