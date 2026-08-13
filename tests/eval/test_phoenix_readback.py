from __future__ import annotations

import pytest

from eval.harness.phoenix import PhoenixReadbackError, read_run_spans


def _span(*, run_id: str, instance_id: str, name: str = "rag.retrieve") -> dict:
    return {
        "name": name,
        "context": {"trace_id": "a" * 32, "span_id": "b" * 16},
        "parent_id": "c" * 16,
        "status_code": "UNSET",
        "start_time": "2026-08-13T00:00:00Z",
        "end_time": "2026-08-13T00:00:01Z",
        "attributes": {
            "eval.run_id": run_id,
            "eval.instance_id": instance_id,
            "rag.query_hash": "0123456789abcdef",
            "rag.retrieval_mode": "hybrid",
            "untrusted.raw.prompt": "must not persist",
        },
    }


def test_read_run_spans_paginates_filters_and_allowlists(monkeypatch) -> None:
    calls: list[str] = []
    pages = iter(
        [
            {"data": [_span(run_id="run-1", instance_id="inst-1")], "next_cursor": "next"},
            {
                "data": [
                    _span(run_id="other-run", instance_id="inst-1"),
                    _span(run_id="run-1", instance_id="foreign"),
                ]
            },
        ]
    )

    def fake_request(url: str) -> dict:
        calls.append(url)
        return next(pages)

    monkeypatch.setattr("eval.harness.phoenix._request_json", fake_request)
    spans = read_run_spans(
        "http://phoenix", "code-agent", "2026-08-13T00:00:00Z", "run-1", ("inst-1",)
    )

    assert len(calls) == 2
    assert "cursor=next" in calls[1]
    assert len(spans) == 1
    assert spans[0].trace_id == "a" * 32
    assert spans[0].parent_span_id == "c" * 16
    assert spans[0].ended is True
    assert spans[0].attributes == {
        "eval.run_id": "run-1",
        "eval.instance_id": "inst-1",
        "rag.query_hash": "0123456789abcdef",
        "rag.retrieval_mode": "hybrid",
    }


def test_read_run_spans_rejects_repeated_pagination_cursor(monkeypatch) -> None:
    monkeypatch.setattr(
        "eval.harness.phoenix._request_json",
        lambda _url: {"data": [], "next_cursor": "stuck"},
    )

    with pytest.raises(PhoenixReadbackError, match="repeated pagination cursor"):
        read_run_spans("http://phoenix", "code-agent", "2026-08-13T00:00:00Z", "run-1")
