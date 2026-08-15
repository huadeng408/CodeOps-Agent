"""Receipt-bound spans from project-owned external benchmark agents."""

from __future__ import annotations

from concurrent.futures import ThreadPoolExecutor

from eval.harness.trace_capture import TraceCapture


def test_agent_trace_is_disabled_without_a_complete_receipt_identity(monkeypatch) -> None:
    from eval.harness.official_agent_trace import receipt_agent_trace

    monkeypatch.delenv("TERMINALBENCH_RUN_ID", raising=False)
    monkeypatch.delenv("TERMINALBENCH_INSTANCE_ID", raising=False)
    assert receipt_agent_trace() is None

    monkeypatch.setenv("TERMINALBENCH_RUN_ID", "receipt-001")
    assert receipt_agent_trace() is None


def test_agent_trace_emits_nested_agent_and_chat_spans_for_a_receipt(monkeypatch) -> None:
    from eval.harness.official_agent_trace import receipt_agent_trace

    capture = TraceCapture()
    assert capture.install()
    monkeypatch.setenv("TERMINALBENCH_RUN_ID", "receipt-002")
    monkeypatch.setenv("TERMINALBENCH_INSTANCE_ID", "fixed-task")

    receipt_trace = receipt_agent_trace()
    assert receipt_trace is not None
    with receipt_trace.agent():
        with receipt_trace.chat():
            pass

    spans = [
        span
        for span in capture.spans()
        if span.attributes.get("eval.run_id") == "receipt-002"
    ]
    assert [span.name for span in spans] == ["chat", "invoke_agent"]
    assert all(span.attributes["eval.instance_id"] == "fixed-task" for span in spans)
    assert spans[0].parent_span_id == spans[1].span_id


def test_agent_trace_restores_w3c_parent_context_inside_terminalbench_worker(monkeypatch) -> None:
    """Terminal-Bench executes its agent in worker threads with fresh contextvars."""
    from eval.harness.official_agent_trace import receipt_agent_trace
    from eval.harness.official_receipt_trace import OfficialReceiptTrace

    capture = TraceCapture()
    assert capture.install()
    monkeypatch.setenv("TERMINALBENCH_RUN_ID", "receipt-worker-001")
    monkeypatch.setenv("TERMINALBENCH_INSTANCE_ID", "fixed-task")

    def execute_agent_turn() -> None:
        worker_trace = receipt_agent_trace()
        assert worker_trace is not None
        with worker_trace.agent():
            with worker_trace.chat():
                pass

    with OfficialReceiptTrace("receipt-worker-001", "fixed-task") as trace:
        with trace.worker_environment():
            with ThreadPoolExecutor(max_workers=1) as executor:
                executor.submit(execute_agent_turn).result()
        with trace.scorer():
            pass

    spans = {
        span.name: span
        for span in capture.spans()
        if span.attributes.get("eval.run_id") == "receipt-worker-001"
    }
    assert set(spans) == {"eval.run", "eval.instance", "invoke_agent", "chat", "scorer.official"}
    assert len({span.trace_id for span in spans.values()}) == 1
    assert spans["eval.instance"].parent_span_id == spans["eval.run"].span_id
    assert spans["invoke_agent"].parent_span_id == spans["eval.instance"].span_id
    assert spans["chat"].parent_span_id == spans["invoke_agent"].span_id
    assert spans["scorer.official"].parent_span_id == spans["eval.instance"].span_id
