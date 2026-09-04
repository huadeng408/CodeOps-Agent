from __future__ import annotations

import os
import json
import subprocess
import sys
import time
from pathlib import Path

from langgraph.graph.state import CompiledStateGraph

from orchestrator.graph.main_graph import build_graph
from orchestrator.graph.nodes import GraphState


def test_main_graph_is_a_langgraph_state_graph_with_conditional_route() -> None:
    graph = build_graph()

    assert isinstance(graph.compiled, CompiledStateGraph)
    assert graph.nodes == ("route", "execute", "verify", "respond")

    executed = graph.run(GraphState(tool_requests=[{"name": "Read"}]))
    budget_exceeded = graph.run(GraphState(budget_status="exceeded"))

    assert executed.tool_rounds == 1
    assert executed.metadata["verified"] is True
    assert executed.next_node == "done"
    assert budget_exceeded.tool_rounds == 0
    assert budget_exceeded.response == "Token budget exceeded."


def test_graph_checkpoint_resumes_after_a_paused_run(tmp_path: Path) -> None:
    checkpoint = tmp_path / "graph-checkpoint.sqlite"
    first = build_graph(checkpoint_path=checkpoint, interrupt_after=("execute",))

    paused = first.run(
        GraphState(tool_requests=[{"name": "Read"}]),
        thread_id="workflow-1",
    )
    first.close()

    assert paused.tool_rounds == 1
    assert paused.done is False
    assert checkpoint.exists()

    resumed_graph = build_graph(checkpoint_path=checkpoint)
    resumed = resumed_graph.run(
        GraphState(tool_requests=[{"name": "Read"}]),
        thread_id="workflow-1",
    )
    resumed_graph.close()

    assert resumed.tool_rounds == 1
    assert resumed.metadata["verified"] is True
    assert resumed.done is True
    assert resumed.next_node == "done"


def test_graph_checkpoint_survives_python_process_termination(tmp_path: Path) -> None:
    database = tmp_path / "process.sqlite"
    marker = tmp_path / "checkpoint.marker"
    script = Path(__file__).parent / "e2e" / "langgraph_process.py"
    environment = os.environ.copy()
    environment["PYTHONPATH"] = str(Path(__file__).resolve().parents[1])

    first = subprocess.Popen(
        [sys.executable, str(script), "--db", str(database), "--marker", str(marker)],
        cwd=environment["PYTHONPATH"],
        env=environment,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
    )
    try:
        deadline = time.monotonic() + 15
        while not marker.exists() and first.poll() is None and time.monotonic() < deadline:
            time.sleep(0.05)
        assert marker.exists(), first.stderr.read() if first.poll() is not None and first.stderr else ""
    finally:
        if first.poll() is None:
            first.kill()
        first.wait(timeout=10)

    resumed = subprocess.run(
        [sys.executable, str(script), "--db", str(database), "--marker", str(marker), "--resume"],
        cwd=environment["PYTHONPATH"],
        env=environment,
        capture_output=True,
        text=True,
        timeout=15,
        check=True,
    )

    assert json.loads(resumed.stdout) == {
        "done": True,
        "status": "completed",
        "tool_rounds": 1,
        "verified": True,
    }
