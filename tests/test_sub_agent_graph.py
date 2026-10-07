from __future__ import annotations

from pathlib import Path
from orchestrator.agents.types import AgentResult
from orchestrator.graph.sub_agent_graph import SubAgentState, build_sub_agent_graph
from orchestrator.runtime.conversation import ConversationRunner


def test_sub_agent_graph_runs_plan_delegate_collect_verify(tmp_path: Path) -> None:
    calls: list[str] = []

    def spawn(task):
        calls.append(task["id"])
        return {"task_id": task["id"], "status": "completed", "output": "ok"}

    graph = build_sub_agent_graph(
        checkpoint_path=tmp_path / "sub-agent.sqlite",
        delegate=spawn,
        verify=lambda result: result["output"] == "ok",
    )
    try:
        state = graph.run(SubAgentState(goal="inspect the repository"), thread_id="agent-1")
        assert state.status == "completed"
        assert state.metadata == {
            "planned": True,
            "delegated": 1,
            "collected": 1,
            "verified": True,
        }
        assert calls == ["task-1"]
        assert graph.get_checkpoint("agent-1").status == "completed"
    finally:
        graph.close()


def test_sub_agent_graph_retry_does_not_delegate_completed_task() -> None:
    calls: list[str] = []

    def spawn(task):
        calls.append(task["id"])
        return {"task_id": task["id"], "status": "completed"}

    graph = build_sub_agent_graph(delegate=spawn)
    state = graph.run(
        SubAgentState(
            tasks=[{"id": "stable", "objective": "one"}],
            delegated={"stable": {"task_id": "stable", "status": "completed"}},
        )
    )
    assert state.status == "completed"
    assert calls == []


def test_compat_runner_routes_process_executor_through_graph() -> None:
    calls: list[str] = []

    class Executor:
        def run(self, **kwargs):
            calls.append(str(kwargs["objective"]))
            return AgentResult(summary="done", artifacts=["report.md"])

    runner = object.__new__(ConversationRunner)
    runner.project_root = "."
    runner.working_dir = "."
    runner.sub_agent_executor = Executor()
    runner.legacy_sub_agent_manager = None
    runner.require_harness_worktree = False

    result = runner._run_sub_agent(
        {
            "kind": "general",
            "title": "inspect",
            "objective": "inspect the repository",
            "context_json": "{}",
        },
        request_id="request-1",
        parent_session_id="parent-1",
        child_session_id="child-1",
    )

    assert result["status"] == "completed"
    assert result["artifacts"] == ["report.md"]
    assert calls == ["inspect the repository"]
