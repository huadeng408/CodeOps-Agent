from __future__ import annotations

import json
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
