"""trace_assert join tests (plan Task 6.2)."""

from __future__ import annotations

import json

import pytest

from scripts.eval.trace_assert import (
    assert_rag_schema,
    assert_span_kinds,
    required_span_kinds,
)


def _span(name: str, trace_id: str = "t1", attributes: dict | None = None) -> dict:
    return {"name": name, "trace_id": trace_id, "attributes": attributes or {}}


def test_required_span_kinds_frozen() -> None:
    assert required_span_kinds() == ["invoke_agent", "chat", "rag.retrieve", "execute_tool", "scorer"]


def test_assert_span_kinds_passes_with_all_kinds() -> None:
    spans = [
        _span("invoke_agent code-agent"),
        _span("chat"),
        _span("rag.retrieve"),
        _span("execute_tool Read"),
        _span("scorer evalplus"),
    ]
    assert assert_span_kinds("t1", spans, required_span_kinds()) == []


def test_assert_span_kinds_reports_missing() -> None:
    spans = [_span("invoke_agent code-agent"), _span("chat")]
    with pytest.raises(AssertionError) as exc:
        assert_span_kinds("t1", spans, required_span_kinds())
    assert "rag.retrieve" in str(exc.value)
    assert "scorer" in str(exc.value)


def test_assert_span_kinds_partial_name_match() -> None:
    # "chat" must match "chat" and "execute_tool" must match any execute_tool.
    spans = [
        _span("invoke_agent code-agent"),
        _span("chat openai-compatible"),
        _span("execute_tool Bash"),
        _span("scorer"),
        _span("rag.retrieve hybrid"),
    ]
    assert assert_span_kinds("t1", spans, required_span_kinds()) == []


def test_assert_rag_schema_requires_standard_attributes() -> None:
    # Flat attribute dict (Phoenix flattens nested keys).
    spans = [_span("rag.retrieve", attributes={"rag.query_hash": "abc", "rag.retrieval_mode": "hybrid", "rag.top_n": 10})]
    assert_rag_schema("t1", spans)  # must not raise


def test_assert_rag_schema_rejects_missing_attribute() -> None:
    spans = [_span("rag.retrieve", attributes={"rag.query_hash": "abc"})]
    with pytest.raises(AssertionError) as exc:
        assert_rag_schema("t1", spans)
    assert "rag.retrieval_mode" in str(exc.value)


def test_assert_rag_schema_skips_when_no_retrieval_spans() -> None:
    assert_rag_schema("t1", [_span("chat")])  # no error


def test_assert_rag_schema_accepts_nested_attributes() -> None:
    spans = [_span("rag.retrieve", attributes={"rag": {"query_hash": "a", "retrieval_mode": "hybrid", "top_n": 5}})]
    assert_rag_schema("t1", spans)
