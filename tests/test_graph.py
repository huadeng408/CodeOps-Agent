from orchestrator.graph.main_graph import build_graph
from orchestrator.graph.nodes import GraphState


def test_graph_executes_and_verifies_tool_requests() -> None:
    graph = build_graph()
    state = graph.run(GraphState(tool_requests=[{"name": "Read"}], error_count=1))

    assert state.tool_rounds == 1
    assert state.metadata["executed"] is True
    assert state.metadata["verified"] is True
    assert state.metadata["tool_request_count"] == 1
    assert state.recovery_hint == "retry_with_context"
    assert state.done is True


def test_graph_routes_budget_exceeded_to_response() -> None:
    graph = build_graph()
    state = graph.run(GraphState(budget_status="exceeded"))

    assert state.tool_rounds == 0
    assert state.response == "Token budget exceeded."
    assert state.next_node == "done"
    assert state.done is True
