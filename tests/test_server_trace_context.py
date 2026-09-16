from __future__ import annotations

import pytest
from opentelemetry import baggage, context as otel_context, trace

from orchestrator.config.env import _EvalJoinSpanProcessor
from orchestrator.server import _admitted_grpc_trace_context


def test_grpc_trace_context_admits_only_safe_eval_baggage() -> None:
    ctx = _admitted_grpc_trace_context(
        (
            (
                "traceparent",
                "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
            ),
            ("tracestate", "vendor=value"),
            (
                "baggage",
                "eval.run_id=run-1,eval.instance_id=agent-1,untrusted=discard-me",
            ),
        )
    )
    assert ctx is not None
    span_context = trace.get_current_span(ctx).get_span_context()
    assert span_context.trace_id == int("0123456789abcdef0123456789abcdef", 16)
    assert span_context.span_id == int("0123456789abcdef", 16)
    assert span_context.trace_state.get("vendor") == "value"
    assert baggage.get_baggage("eval.run_id", context=ctx) == "run-1"
    assert baggage.get_baggage("eval.instance_id", context=ctx) == "agent-1"
    assert baggage.get_baggage("untrusted", context=ctx) is None


def test_grpc_trace_context_rejects_unsafe_eval_baggage() -> None:
    ctx = _admitted_grpc_trace_context(
        (
            (
                "traceparent",
                "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
            ),
            (
                "baggage",
                "eval.run_id=unsafe%20run,eval.instance_id=bad%2Cvalue",
            ),
        )
    )
    assert ctx is not None
    assert baggage.get_baggage("eval.run_id", context=ctx) is None
    assert baggage.get_baggage("eval.instance_id", context=ctx) is None


def test_grpc_trace_context_does_not_inherit_ambient_baggage() -> None:
    ambient = baggage.set_baggage("eval.run_id", "stale-run")
    ambient = baggage.set_baggage("untrusted", "stale-value", context=ambient)
    token = otel_context.attach(ambient)
    try:
        ctx = _admitted_grpc_trace_context(
            (
                (
                    "traceparent",
                    "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01",
                ),
            )
        )
    finally:
        otel_context.detach(token)

    assert ctx is not None
    assert baggage.get_baggage("eval.run_id", context=ctx) is None
    assert baggage.get_baggage("untrusted", context=ctx) is None


@pytest.mark.parametrize(
    "metadata",
    (
        (
            ("traceparent", "malformed"),
            ("baggage", "eval.run_id=run-1,eval.instance_id=agent-1"),
        ),
        (
            (
                "traceparent",
                "00-00000000000000000000000000000000-0000000000000000-01",
            ),
            ("baggage", "eval.run_id=run-1,eval.instance_id=agent-1"),
        ),
        (("baggage", "eval.run_id=run-1,eval.instance_id=agent-1"),),
    ),
    ids=("malformed", "all-zero", "missing-parent"),
)
def test_grpc_trace_context_rejects_baggage_without_valid_remote_parent(
    metadata: tuple[tuple[str, str], ...],
) -> None:
    assert _admitted_grpc_trace_context(metadata) is None


def test_production_span_processor_stamps_admitted_join_only() -> None:
    class Span:
        def __init__(self) -> None:
            self.attributes: dict[str, str] = {}

        def set_attribute(self, key: str, value: str) -> None:
            self.attributes[key] = value

    ctx = baggage.set_baggage("eval.run_id", "run-processor-1")
    ctx = baggage.set_baggage("eval.instance_id", "agent-processor-1", context=ctx)
    ctx = baggage.set_baggage("untrusted", "SECRET_SENTINEL", context=ctx)
    token = otel_context.attach(ctx)
    try:
        span = Span()
        _EvalJoinSpanProcessor().on_start(span, parent_context=ctx)
    finally:
        otel_context.detach(token)
    assert span.attributes == {
        "eval.run_id": "run-processor-1",
        "eval.instance_id": "agent-processor-1",
    }
