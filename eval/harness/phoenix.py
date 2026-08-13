"""Read-only Phoenix span source adapter for the O3 trace contract.

This module deliberately has no verdict logic. It paginates Phoenix's project
span API, normalizes finished spans, and returns only the attributes allowed in
evaluation trace artifacts. The shared contract remains the only evaluator.
"""

from __future__ import annotations

import json
from typing import Any, Iterable, Mapping
from urllib.parse import quote, urlencode
from urllib.request import Request, urlopen

from eval.harness.trace_contract import CapturedSpan


class PhoenixReadbackError(RuntimeError):
    """Phoenix readback could not produce a trustworthy normalized source."""


_ATTRIBUTE_ALLOWLIST = frozenset(
    {
        "eval.run_id",
        "eval.instance_id",
        "git.commit",
        "rag.query_hash",
        "rag.corpus_generation",
        "rag.index_name",
        "rag.retrieval_mode",
        "rag.top_n",
        "rag.reranker_applied",
        "rag.degraded",
        "rag.reranker_timeout",
        "gen_ai.operation.name",
    }
)


def _request_json(url: str) -> Mapping[str, Any]:
    try:
        with urlopen(Request(url, headers={"Accept": "application/json"}), timeout=15) as response:
            payload = json.loads(response.read().decode("utf-8"))
    except Exception as exc:  # noqa: BLE001 - source adapter boundary
        raise PhoenixReadbackError(f"Phoenix request failed: {type(exc).__name__}") from exc
    if not isinstance(payload, Mapping):
        raise PhoenixReadbackError("Phoenix response is not an object")
    return payload


def _spans_url(phoenix_url: str, project: str, start_time: str, cursor: str = "") -> str:
    query: dict[str, str | int] = {"start_time": start_time, "limit": 100}
    if cursor:
        query["cursor"] = cursor
    return f"{phoenix_url.rstrip('/')}/v1/projects/{quote(project, safe='')}/spans?{urlencode(query)}"


def _allowed_attributes(attributes: object) -> dict[str, Any]:
    if not isinstance(attributes, Mapping):
        return {}
    return {str(key): value for key, value in attributes.items() if str(key) in _ATTRIBUTE_ALLOWLIST}


def _parse_page(payload: Mapping[str, Any]) -> tuple[list[CapturedSpan], str]:
    data = payload.get("data")
    if not isinstance(data, list):
        raise PhoenixReadbackError("Phoenix span response has no data list")
    spans: list[CapturedSpan] = []
    for item in data:
        if not isinstance(item, Mapping):
            raise PhoenixReadbackError("Phoenix returned a malformed span")
        context = item.get("context")
        if not isinstance(context, Mapping):
            raise PhoenixReadbackError("Phoenix span has no context")
        trace_id = str(context.get("trace_id", "")).strip()
        span_id = str(context.get("span_id", "")).strip()
        name = str(item.get("name", "")).strip()
        if not trace_id or not span_id or not name:
            raise PhoenixReadbackError("Phoenix span lacks trace ID, span ID, or name")
        spans.append(
            CapturedSpan(
                name=name,
                trace_id=trace_id,
                span_id=span_id,
                parent_span_id=str(item.get("parent_id") or ""),
                status=str(item.get("status_code") or "UNSET"),
                attributes=_allowed_attributes(item.get("attributes")),
                ended=bool(item.get("end_time")),
            )
        )
    cursor = payload.get("next_cursor")
    return spans, str(cursor) if cursor else ""


def read_run_spans(
    phoenix_url: str,
    project: str,
    start_time: str,
    run_id: str,
    expected_instance_ids: Iterable[str] = (),
) -> list[CapturedSpan]:
    """Return only normalized Phoenix spans belonging to one O3 run.

    All pages from the project/start-time window are read before filtering so a
    run split across pages cannot be mistaken for incomplete. Repeated cursors
    fail closed rather than looping indefinitely or silently truncating data.
    """
    expected = set(expected_instance_ids)
    cursor = ""
    seen_cursors: set[str] = set()
    matched: list[CapturedSpan] = []
    while True:
        page, next_cursor = _parse_page(_request_json(_spans_url(phoenix_url, project, start_time, cursor)))
        for span in page:
            if str(span.attributes.get("eval.run_id", "")) != run_id:
                continue
            if expected and str(span.attributes.get("eval.instance_id", "")) not in expected:
                continue
            matched.append(span)
        if not next_cursor:
            return matched
        if next_cursor in seen_cursors:
            raise PhoenixReadbackError(f"Phoenix repeated pagination cursor {next_cursor!r}")
        seen_cursors.add(next_cursor)
        cursor = next_cursor
