from __future__ import annotations

import json
import threading
from pathlib import Path

import pytest

from orchestrator.agents.process import ProcessAgentExecutor


def test_process_agent_executor_runs_versioned_child_session(tmp_path: Path) -> None:
    (tmp_path / "README.md").write_text("child workspace", encoding="utf-8")
    executor = ProcessAgentExecutor(project_root=tmp_path, working_dir=tmp_path)

    result = executor.run(
        kind="review",
        title="Review files",
        objective="inspect the repository",
        context={"files": ["README.md"]},
        request_id="request-1",
        parent_session_id="session-1",
        child_session_id="session-1/subagent/request-1",
    )

    assert result.status == "completed"
    assert "review completed: Review files" in result.summary
    assert "existing files" in " ".join(result.artifacts)
    assert "protocol_version: agent.v1" in result.notes


def test_process_agent_executor_fails_closed_when_child_times_out(tmp_path: Path) -> None:
    executor = ProcessAgentExecutor(project_root=tmp_path, working_dir=tmp_path, timeout=0.01)

    with pytest.raises(TimeoutError, match="sub-agent process timed out"):
        executor.run(
            kind="review",
            title="Review files",
            objective="inspect the repository",
            context={},
            request_id="request-timeout",
            parent_session_id="session-1",
            child_session_id="session-1/subagent/request-timeout",
        )


def test_child_protocol_round_trip_contains_no_parent_prompt_echo(tmp_path: Path) -> None:
    executor = ProcessAgentExecutor(project_root=tmp_path, working_dir=tmp_path)
    result = executor.run(
        kind="general",
        title="Summarize",
        objective="summarize the context",
        context={"marker": "structured-context"},
        request_id="request-2",
        parent_session_id="session-2",
        child_session_id="session-2/subagent/request-2",
    )

    encoded = json.dumps({"summary": result.summary, "artifacts": result.artifacts}, ensure_ascii=False)
    assert "structured-context" in encoded
    assert "request-2" not in result.summary


def test_process_agent_executor_accepts_harness_assigned_worktree(tmp_path: Path) -> None:
    child = tmp_path / ".agent" / "worktrees" / "agent-request-3"
    child.mkdir(parents=True)
    (child / "child.txt").write_text("isolated", encoding="utf-8")
    executor = ProcessAgentExecutor(project_root=tmp_path, working_dir=tmp_path)

    result = executor.run(
        kind="review",
        title="Review isolated tree",
        objective="inspect the isolated checkout",
        context={"files": ["child.txt"]},
        request_id="request-3",
        parent_session_id="session-3",
        child_session_id="session-3/subagent/request-3",
        worktree_path=child,
        require_worktree=True,
    )

    assert result.status == "completed"
    assert any("child.txt" in artifact for artifact in result.artifacts)


def test_process_agent_executor_requires_declared_child_artifacts(tmp_path: Path) -> None:
    executor = ProcessAgentExecutor(project_root=tmp_path, working_dir=tmp_path)

    with pytest.raises(RuntimeError, match="required artifact"):
        executor.run(
            kind="general",
            title="Produce report",
            objective="write the report",
            context={"required_artifacts": ["report.md"]},
            request_id="request-artifact-missing",
            parent_session_id="session-artifact",
            child_session_id="session-artifact/subagent/request-artifact-missing",
        )


def test_process_agent_executor_returns_declared_artifact_receipt(tmp_path: Path) -> None:
    (tmp_path / "report.md").write_text("completed", encoding="utf-8")
    executor = ProcessAgentExecutor(project_root=tmp_path, working_dir=tmp_path)

    result = executor.run(
        kind="general",
        title="Verify report",
        objective="inspect the report",
        context={"required_artifacts": ["report.md"]},
        request_id="request-artifact-present",
        parent_session_id="session-artifact",
        child_session_id="session-artifact/subagent/request-artifact-present",
    )

    assert result.status == "completed"
    assert any("artifact: report.md" in artifact for artifact in result.artifacts)


def test_process_agent_executor_applies_explicit_create_file_objective(tmp_path: Path) -> None:
    executor = ProcessAgentExecutor(project_root=tmp_path, working_dir=tmp_path)

    result = executor.run(
        kind="general",
        title="Create marker",
        objective="create file .agent-demo/restore-marker.txt with content RESTORE_E2E_MARKER",
        context={},
        request_id="request-artifact-create",
        parent_session_id="session-artifact",
        child_session_id="session-artifact/subagent/request-artifact-create",
    )

    marker = tmp_path / ".agent-demo" / "restore-marker.txt"
    assert marker.read_text(encoding="utf-8") == "RESTORE_E2E_MARKER"
    assert any("created artifact: .agent-demo/restore-marker.txt" in artifact for artifact in result.artifacts)


def test_process_agent_executor_applies_chinese_create_file_objective(tmp_path: Path) -> None:
    executor = ProcessAgentExecutor(project_root=tmp_path, working_dir=tmp_path)

    result = executor.run(
        kind="general",
        title="创建 marker",
        objective="在 managed worktree 中创建文件 .agent-demo/restore-marker.txt，内容只写 RESTORE_E2E_MARKER",
        context={},
        request_id="request-artifact-create-zh",
        parent_session_id="session-artifact",
        child_session_id="session-artifact/subagent/request-artifact-create-zh",
    )

    marker = tmp_path / ".agent-demo" / "restore-marker.txt"
    assert marker.read_text(encoding="utf-8") == "RESTORE_E2E_MARKER"
    assert any("created artifact: .agent-demo/restore-marker.txt" in artifact for artifact in result.artifacts)


def test_process_agent_executor_rejects_canceled_parent_before_spawn(tmp_path: Path) -> None:
    executor = ProcessAgentExecutor(project_root=tmp_path, working_dir=tmp_path)
    canceled = threading.Event()
    canceled.set()

    with pytest.raises(RuntimeError, match="sub-agent process canceled"):
        executor.run(
            kind="general",
            title="Canceled task",
            objective="do not start",
            context={},
            request_id="request-canceled",
            parent_session_id="session-canceled",
            child_session_id="session-canceled/subagent/request-canceled",
            cancel_event=canceled,
        )
