"""Official benchmark receipt traces must expose, never conceal, gaps."""

from __future__ import annotations

from eval.harness.official_receipt_trace import (
    OfficialReceiptTrace,
    evaluate_official_receipt_trace,
)
from eval.harness.trace_capture import TraceCapture


RUN_ID = "terminalbench-receipt-001"
INSTANCE_ID = "break-filter-js-from-html"


def test_official_receipt_trace_captures_real_run_instance_and_scorer_spans() -> None:
    capture = TraceCapture()
    assert capture.install()

    with OfficialReceiptTrace(RUN_ID, INSTANCE_ID) as trace:
        with trace.scorer():
            pass

    report = evaluate_official_receipt_trace(capture.spans(), RUN_ID, INSTANCE_ID)

    assert report["verdict"] == "INCOMPLETE_AGENT_TRACE"
    assert report["present_kinds"] == ["eval.run", "eval.instance", "scorer.official"]
    assert report["missing_agent_kinds"] == ["invoke_agent", "chat"]


def test_official_receipt_trace_passes_only_when_actual_agent_and_chat_spans_exist() -> None:
    capture = TraceCapture()
    assert capture.install()

    with OfficialReceiptTrace(RUN_ID, INSTANCE_ID) as trace:
        with trace.agent():
            with trace.chat():
                pass
        with trace.scorer():
            pass

    report = evaluate_official_receipt_trace(capture.spans(), RUN_ID, INSTANCE_ID)

    assert report["verdict"] == "PASS"
    assert report["missing_agent_kinds"] == []


def test_receipt_trace_writer_persists_incomplete_assertion_before_checksum(tmp_path) -> None:
    from eval.harness.official_receipt_trace import (
        refresh_receipt_checksums,
        write_official_receipt_trace,
    )

    capture = TraceCapture()
    assert capture.install()
    with OfficialReceiptTrace(RUN_ID, INSTANCE_ID) as trace:
        with trace.scorer():
            pass

    report = write_official_receipt_trace(tmp_path, capture, RUN_ID, INSTANCE_ID)

    assert report["verdict"] == "INCOMPLETE_AGENT_TRACE"
    assert (tmp_path / "traces" / "trace-summary.json").is_file()
    assertion = (tmp_path / "traces" / "span-assertion.json").read_text(encoding="utf-8")
    assert "INCOMPLETE_AGENT_TRACE" in assertion
    refresh_receipt_checksums(tmp_path)
    checksums = (tmp_path / "checksums.sha256").read_text(encoding="utf-8")
    assert "traces/trace-summary.json" in checksums
    assert "traces/span-assertion.json" in checksums


def test_receipt_trace_writer_marks_phoenix_only_after_readback(tmp_path) -> None:
    from eval.harness.official_receipt_trace import write_official_receipt_trace

    capture = TraceCapture()
    assert capture.install()
    with OfficialReceiptTrace(RUN_ID, INSTANCE_ID) as trace:
        with trace.scorer():
            pass

    report = write_official_receipt_trace(
        tmp_path,
        capture,
        RUN_ID,
        INSTANCE_ID,
        phoenix_url="http://phoenix.test",
        phoenix_start_time="2026-08-14T00:00:00Z",
        phoenix_reader=lambda *_: capture.spans(),
    )

    summary = __import__("json").loads(
        (tmp_path / "traces" / "trace-summary.json").read_text(encoding="utf-8")
    )
    assert report["verdict"] == "INCOMPLETE_AGENT_TRACE"
    assert summary["collector"] == "phoenix-readback"
    assert summary["phoenix_verified"] is True


def test_receipt_trace_writer_never_claims_phoenix_when_readback_fails(tmp_path) -> None:
    from eval.harness.official_receipt_trace import write_official_receipt_trace
    from eval.harness.phoenix import PhoenixReadbackError

    capture = TraceCapture()
    assert capture.install()
    with OfficialReceiptTrace(RUN_ID, INSTANCE_ID) as trace:
        with trace.scorer():
            pass

    write_official_receipt_trace(
        tmp_path,
        capture,
        RUN_ID,
        INSTANCE_ID,
        phoenix_url="http://phoenix.test",
        phoenix_start_time="2026-08-14T00:00:00Z",
        phoenix_reader=lambda *_: (_ for _ in ()).throw(PhoenixReadbackError("unavailable")),
    )

    summary = __import__("json").loads(
        (tmp_path / "traces" / "trace-summary.json").read_text(encoding="utf-8")
    )
    assert summary["phoenix_verified"] is False
    assert summary["collector"] == "otlp-http-unverified"
    assert summary["phoenix_readback_error"] == "PhoenixReadbackError"
