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


def test_read_run_spans_keeps_the_run_root_with_empty_instance_id(monkeypatch) -> None:
    """The root spans the whole run, so it legitimately has no instance ID."""
    monkeypatch.setattr(
        "eval.harness.phoenix._request_json",
        lambda _url: {
            "data": [
                _span(run_id="run-1", instance_id="", name="eval.run"),
                _span(run_id="run-1", instance_id="inst-1", name="eval.instance"),
                _span(run_id="run-1", instance_id="foreign", name="eval.instance"),
            ]
        },
    )

    spans = read_run_spans(
        "http://phoenix", "code-agent", "2026-08-13T00:00:00Z", "run-1", ("inst-1",)
    )

    assert [span.name for span in spans] == ["eval.run", "eval.instance"]


def test_read_run_spans_rejects_repeated_pagination_cursor(monkeypatch) -> None:
    monkeypatch.setattr(
        "eval.harness.phoenix._request_json",
        lambda _url: {"data": [], "next_cursor": "stuck"},
    )

    with pytest.raises(PhoenixReadbackError, match="repeated pagination cursor"):
        read_run_spans("http://phoenix", "code-agent", "2026-08-13T00:00:00Z", "run-1")


def test_product_readback_locates_go_run_then_reads_all_its_spans(monkeypatch) -> None:
    from eval.harness import phoenix

    root = _span(run_id="", instance_id="session-1", name="agent.main")
    root["parent_id"] = None
    root["attributes"].update({"session.id": "session-1", "session.run_id": "run:actual"})
    child = _span(run_id="", instance_id="session-1", name="provider.call")
    child["context"]["span_id"] = "c" * 16
    child["parent_id"] = "b" * 16
    child["attributes"].update({"model.call_id": "call-1", "model.purpose": "memory_reflection"})
    responses = iter([{"data": [root]}, {"data": [root], "next_cursor": "child"}, {"data": [child]}])
    urls = []

    def request(url):
        urls.append(url)
        return next(responses)

    monkeypatch.setattr(phoenix, "_request_json", request)
    spans = phoenix.read_product_spans("http://phoenix", "default", "2026-10-15T00:00:00Z", "session-1", "run:actual")
    assert [span.name for span in spans] == ["agent.main", "provider.call"]
    assert spans[1].attributes["model.call_id"] == "call-1"
    assert spans[1].attributes["model.purpose"] == "memory_reflection"
    assert "untrusted.raw.prompt" not in spans[1].attributes
    assert "session.run_id" in urls[0] and "name=agent.main" in urls[0]
    assert "trace_id=" + "a" * 32 in urls[1]
    assert "cursor=child" in urls[2]


@pytest.mark.parametrize("same_trace", [True, False])
def test_product_readback_preserves_all_recovered_execution_attempts(monkeypatch, same_trace) -> None:
    from urllib.parse import parse_qs, urlsplit

    from eval.harness import phoenix

    roots, children = [], []
    for trace_id, root_id, child_id in [("a" * 32, "b" * 16, "d" * 16),
                                       ("a" * 32 if same_trace else "f" * 32, "c" * 16, "e" * 16)]:
        root = _span(run_id="", instance_id="session-1", name="agent.main")
        root["context"] = {"trace_id": trace_id, "span_id": root_id}
        root["parent_id"] = None
        root["attributes"].update({"session.id": "session-1", "session.run_id": "run:recovered"})
        child = _span(run_id="", instance_id="session-1", name="execute_tool Read")
        child["context"] = {"trace_id": trace_id, "span_id": child_id}
        child["parent_id"] = root_id
        roots.append(root)
        children.append(child)
    children[0]["status_code"] = "ERROR"

    def request(url):
        query = parse_qs(urlsplit(url).query)
        if "name" in query:
            return {"data": [roots[1]]} if "cursor" in query else {"data": [roots[0]], "next_cursor": "second-root"}
        trace_id = query["trace_id"][0]
        items = [span for pair in zip(roots, children) for span in pair if span["context"]["trace_id"] == trace_id]
        return {"data": items[1:]} if "cursor" in query else {"data": items[:1], "next_cursor": "remaining-spans"}

    monkeypatch.setattr(phoenix, "_request_json", request)
    spans = phoenix.read_product_spans("http://phoenix", "default", "2026-10-16T00:00:00Z", "session-1", "run:recovered")
    assert {span.span_id for span in spans} == {"b" * 16, "c" * 16, "d" * 16, "e" * 16}
    assert len(spans) == 4
    assert sum(span.name == "agent.main" for span in spans) == 2
    assert sum(span.status == "ERROR" for span in spans) == 1


@pytest.mark.parametrize("failure,reason", [("duplicate-root", "duplicate roots"),
                                         ("foreign-trace", "unexpected trace"),
                                         ("private-content", "sensitive fields")])
def test_product_readback_keeps_source_refusals(monkeypatch, failure, reason) -> None:
    from eval.harness import phoenix

    root = _span(run_id="", instance_id="session-1", name="agent.main")
    root["attributes"].update({"session.id": "session-1", "session.run_id": "run:actual"})

    def request(url):
        if "name=agent.main" in url:
            return {"data": [root, root] if failure == "duplicate-root" else [root]}
        child = _span(run_id="", instance_id="session-1")
        if failure == "foreign-trace":
            child["context"]["trace_id"] = "f" * 32
        else:
            child["attributes"]["gen_ai.input.messages"] = "private content"
        return {"data": [root, child]}

    monkeypatch.setattr(phoenix, "_request_json", request)
    with pytest.raises(PhoenixReadbackError, match=reason):
        phoenix.read_product_spans("http://phoenix", "default", "2026-10-16T00:00:00Z", "session-1", "run:actual")
