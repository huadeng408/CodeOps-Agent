from __future__ import annotations

import pytest

from tests.integration.trace_e2e import SpanRecord, select_run_trace


RUN_ID = "run-123"
TRACE_ID = "a" * 32
ROOT_ID = "1" * 16


def span(
    name: str,
    span_id: str,
    parent_id: str | None,
    *,
    attributes: dict | None = None,
    status_code: str = "OK",
) -> SpanRecord:
    return SpanRecord(
        trace_id=TRACE_ID,
        span_id=span_id,
        parent_id=parent_id,
        name=name,
        status_code=status_code,
        attributes=attributes or {},
    )


def valid_spans(*, flattened_result: bool = False) -> list[SpanRecord]:
    if flattened_result:
        attributes = {
            "gen_ai.tool.call.result": f"TRACE_E2E_FIXTURE:{RUN_ID}",
        }
    else:
        attributes = {
            "gen_ai": {
                "tool": {
                    "call": {
                        "result": f"TRACE_E2E_FIXTURE:{RUN_ID}",
                    }
                }
            }
        }
    return [
        span("invoke_agent code-agent", ROOT_ID, None),
        span("chat", "2" * 16, ROOT_ID),
        span(
            "execute_tool Read",
            "3" * 16,
            ROOT_ID,
            attributes=attributes,
        ),
        span("chat", "4" * 16, ROOT_ID),
    ]


def test_select_run_trace_requires_fixture_result_and_cross_runtime_tree():
    result = select_run_trace(valid_spans(), RUN_ID)

    assert result.trace_id == TRACE_ID
    assert [item.runtime for item in result.required_spans] == [
        "go",
        "go",
        "python",
        "python",
    ]


def test_select_run_trace_accepts_flattened_tool_result_attribute():
    result = select_run_trace(valid_spans(flattened_result=True), RUN_ID)

    assert result.trace_id == TRACE_ID


def test_select_run_trace_rejects_historical_trace_without_run_marker():
    spans = valid_spans()
    spans[2] = span("execute_tool Read", "3" * 16, ROOT_ID)

    with pytest.raises(AssertionError, match=f"TRACE_E2E_FIXTURE:{RUN_ID}"):
        select_run_trace(spans, RUN_ID)


def test_select_run_trace_requires_two_chat_spans():
    spans = valid_spans()
    spans.pop()

    with pytest.raises(AssertionError, match="at least two chat spans"):
        select_run_trace(spans, RUN_ID)


def test_select_run_trace_rejects_error_tool_span():
    spans = valid_spans()
    spans[2] = span(
        "execute_tool Read",
        "3" * 16,
        ROOT_ID,
        attributes={
            "gen_ai.tool.call.result": f"TRACE_E2E_FIXTURE:{RUN_ID}",
        },
        status_code="ERROR",
    )

    with pytest.raises(AssertionError, match="ERROR status"):
        select_run_trace(spans, RUN_ID)


def test_select_run_trace_rejects_detached_chat_parent():
    spans = valid_spans()
    spans[-1] = span("chat", "4" * 16, "f" * 16)

    with pytest.raises(AssertionError, match="do not reach invoke_agent"):
        select_run_trace(spans, RUN_ID)
