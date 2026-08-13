"""O2: in-process span capture from *real* OTel execution (design map §20.6.4).

Phoenix `:6006` is not running, and §20.6.4's artifact list still requires
``traces/trace-summary.json`` + ``traces/span-assertion.json``.  The capture
here is an OTel ``SpanProcessor``: it observes spans the SDK actually started
and ended, so the artifact is a recording of real execution rather than a
hand-authored file.  §20.2 forbids "manually sending spans to Phoenix and
claiming the production chain works" — the same prohibition applies to writing
a span dict straight to disk, which is why every test below drives the real
``TracerProvider`` instead of constructing ``CapturedSpan`` by hand.

The capture must also degrade honestly: with no OTel installed, or with no
spans produced, it reports zero spans rather than inventing any.
"""

from __future__ import annotations

import pytest

from eval.harness.trace_capture import TraceCapture

trace_api = pytest.importorskip("opentelemetry.trace")
from opentelemetry.sdk.trace import TracerProvider  # noqa: E402


@pytest.fixture()
def provider() -> TracerProvider:
    """An isolated provider, never registered globally.

    Registering would collide with ``orchestrator/config/env.py``'s global
    provider and leak between tests.
    """
    return TracerProvider()


def test_capture_records_a_real_sdk_span(provider: TracerProvider) -> None:
    capture = TraceCapture()
    capture.attach_to_provider(provider)
    tracer = provider.get_tracer(__name__)

    with tracer.start_as_current_span("eval.run") as span:
        span.set_attribute("eval.run_id", "run-1")

    spans = capture.spans()
    assert len(spans) == 1
    assert spans[0].name == "eval.run"
    assert spans[0].attributes["eval.run_id"] == "run-1"
    # Real OTel ids are 32/16 hex chars; a fabricated span would not be.
    assert len(spans[0].trace_id) == 32
    assert len(spans[0].span_id) == 16


def test_capture_preserves_parent_child_relationship(provider: TracerProvider) -> None:
    """The parent chain is what §20.6.4 item 3 asserts on; if the capture
    flattened it, the artifact could not prove a父链 exists."""
    capture = TraceCapture()
    capture.attach_to_provider(provider)
    tracer = provider.get_tracer(__name__)

    with tracer.start_as_current_span("eval.run") as parent:
        parent_id = format(parent.get_span_context().span_id, "016x")
        with tracer.start_as_current_span("eval.instance"):
            pass

    by_name = {span.name: span for span in capture.spans()}
    assert by_name["eval.instance"].parent_span_id == parent_id
    assert by_name["eval.run"].parent_span_id == ""
    assert by_name["eval.instance"].trace_id == by_name["eval.run"].trace_id


def test_capture_records_error_status(provider: TracerProvider) -> None:
    """§20.6.4 item 2: error/timeout/cancel/skipped must still end the span
    and record status."""
    capture = TraceCapture()
    capture.attach_to_provider(provider)
    tracer = provider.get_tracer(__name__)

    with pytest.raises(RuntimeError):
        with tracer.start_as_current_span("scorer.official"):
            raise RuntimeError("scorer blew up")

    spans = capture.spans()
    assert spans[0].status == "ERROR"


def test_capture_is_empty_before_any_span(provider: TracerProvider) -> None:
    capture = TraceCapture()
    capture.attach_to_provider(provider)
    assert capture.spans() == []


def test_capture_redacts_secret_looking_attribute_values(provider: TracerProvider) -> None:
    """Defence in depth (§20.1 rule 4 / §9.2).  The contract validator also
    rejects secrets, but the capture must not hold one in memory long enough
    to reach an artifact."""
    capture = TraceCapture()
    capture.attach_to_provider(provider)
    tracer = provider.get_tracer(__name__)

    with tracer.start_as_current_span("chat") as span:
        span.set_attribute("http.authorization", "Bearer eyJhbGciOiJIUzI1NiJ9")

    value = capture.spans()[0].attributes["http.authorization"]
    assert "eyJhbGciOiJIUzI1NiJ9" not in str(value)
    assert value == "<redacted>"


def test_attaching_to_a_non_sdk_provider_is_a_noop() -> None:
    """``trace.get_tracer_provider()`` returns a ProxyTracerProvider when
    nothing was configured.  Telemetry must never crash a run (§9.3: the
    business path degrades normally when the exporter is off)."""
    capture = TraceCapture()

    class _Proxy:
        pass

    assert capture.attach_to_provider(_Proxy()) is False
    assert capture.spans() == []


def test_capture_summary_reports_real_counts(provider: TracerProvider) -> None:
    capture = TraceCapture()
    capture.attach_to_provider(provider)
    tracer = provider.get_tracer(__name__)

    with tracer.start_as_current_span("eval.run"):
        with tracer.start_as_current_span("eval.instance"):
            pass

    summary = capture.summary()
    assert summary["span_count"] == 2
    assert summary["capture_mode"] == "in-process-span-processor"
    assert len(summary["trace_ids"]) == 1
    assert {item["name"] for item in summary["spans"]} == {"eval.run", "eval.instance"}


def test_summary_declares_no_collector_dependency(provider: TracerProvider) -> None:
    """The artifact must state where the spans came from, so a reviewer can
    tell an in-process capture from a Phoenix query without guessing."""
    capture = TraceCapture()
    capture.attach_to_provider(provider)
    summary = capture.summary()
    assert summary["collector"] == "none"
    assert summary["phoenix_verified"] is False


def test_o3_exporter_can_attach_to_existing_sdk_provider(monkeypatch, provider: TracerProvider) -> None:
    capture = TraceCapture()
    recorded: list[str] = []
    monkeypatch.setattr(
        TraceCapture,
        "_attach_otlp_exporter",
        staticmethod(lambda _provider, endpoint: recorded.append(endpoint)),
    )
    # Bypass global registration and exercise the existing-provider branch.
    monkeypatch.setattr(trace_api, "get_tracer_provider", lambda: provider)

    assert capture.install("http://phoenix/v1/traces") is True
    assert recorded == ["http://phoenix/v1/traces"]
