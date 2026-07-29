from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any, Callable, Mapping, Sequence


@dataclass(frozen=True, slots=True)
class SpanRecord:
    trace_id: str
    span_id: str
    parent_id: str | None
    name: str
    status_code: str
    attributes: Mapping[str, Any]


@dataclass(frozen=True, slots=True)
class RequiredSpan:
    name: str
    runtime: str
    span_id: str
    parent_id: str | None


@dataclass(frozen=True, slots=True)
class TraceResult:
    trace_id: str
    required_spans: tuple[RequiredSpan, ...]


def nested_attribute(attributes: Mapping[str, Any], dotted_key: str) -> Any:
    if dotted_key in attributes:
        return attributes[dotted_key]

    value: Any = attributes
    for part in dotted_key.split("."):
        if not isinstance(value, Mapping) or part not in value:
            return None
        value = value[part]
    return value


def _reaches_root(
    span: SpanRecord,
    root_id: str,
    by_id: Mapping[str, SpanRecord],
) -> bool:
    seen: set[str] = set()
    current = span
    while current.parent_id:
        if current.parent_id == root_id:
            return True
        if current.parent_id in seen or current.parent_id not in by_id:
            return False
        seen.add(current.parent_id)
        current = by_id[current.parent_id]
    return False


def select_run_trace(spans: Sequence[SpanRecord], run_id: str) -> TraceResult:
    marker = f"TRACE_E2E_FIXTURE:{run_id}"
    matching_tools = [
        item
        for item in spans
        if item.name == "execute_tool Read"
        and marker
        in str(nested_attribute(item.attributes, "gen_ai.tool.call.result") or "")
    ]
    if not matching_tools:
        raise AssertionError(f"no execute_tool Read span contains {marker}")

    trace_ids = {item.trace_id for item in matching_tools}
    if len(trace_ids) != 1:
        raise AssertionError(f"run marker matched multiple traces: {sorted(trace_ids)}")

    trace_id = next(iter(trace_ids))
    trace_spans = [item for item in spans if item.trace_id == trace_id]
    roots = [item for item in trace_spans if item.name == "invoke_agent code-agent"]
    if len(roots) != 1:
        raise AssertionError(f"expected one invoke_agent root, found {len(roots)}")
    root = roots[0]

    tools = [item for item in matching_tools if item.trace_id == trace_id]
    chats = [item for item in trace_spans if item.name == "chat"]
    if len(chats) < 2:
        raise AssertionError(f"expected at least two chat spans, found {len(chats)}")
    if any(item.status_code.upper() == "ERROR" for item in tools):
        raise AssertionError("the required Read tool span has ERROR status")

    by_id = {item.span_id: item for item in trace_spans}
    detached = [
        item.span_id
        for item in [*tools, *chats]
        if not _reaches_root(item, root.span_id, by_id)
    ]
    if detached:
        raise AssertionError(f"required spans do not reach invoke_agent: {detached}")

    required = [RequiredSpan(root.name, "go", root.span_id, root.parent_id)]
    required.extend(
        RequiredSpan(item.name, "go", item.span_id, item.parent_id) for item in tools
    )
    required.extend(
        RequiredSpan(item.name, "python", item.span_id, item.parent_id)
        for item in chats
    )
    return TraceResult(trace_id=trace_id, required_spans=tuple(required))


def build_model_url(base_url: str) -> str:
    base = base_url.rstrip("/")
    return f"{base}/models" if base.endswith("/v1") else f"{base}/v1/models"


def build_spans_url(
    phoenix_url: str,
    project: str,
    start_time: str,
    cursor: str | None,
) -> str:
    query: dict[str, str | int] = {
        "start_time": start_time,
        "limit": 100,
    }
    if cursor:
        query["cursor"] = cursor
    encoded_project = urllib.parse.quote(project, safe="")
    return (
        f"{phoenix_url.rstrip('/')}/v1/projects/{encoded_project}/spans?"
        f"{urllib.parse.urlencode(query)}"
    )


def parse_model_ids(payload: Mapping[str, Any]) -> set[str]:
    data = payload.get("data")
    if not isinstance(data, list):
        raise ValueError("model response has no data list")
    return {
        str(item["id"])
        for item in data
        if isinstance(item, Mapping) and item.get("id")
    }


def parse_spans_page(
    payload: Mapping[str, Any],
) -> tuple[list[SpanRecord], str | None]:
    data = payload.get("data")
    if not isinstance(data, list):
        raise ValueError("Phoenix span response has no data list")

    spans: list[SpanRecord] = []
    for item in data:
        if not isinstance(item, Mapping):
            raise ValueError("Phoenix returned a malformed span")
        context = item.get("context")
        if (
            not isinstance(context, Mapping)
            or not context.get("trace_id")
            or not context.get("span_id")
            or not item.get("name")
        ):
            raise ValueError("Phoenix returned a malformed span")
        attributes = item.get("attributes")
        spans.append(
            SpanRecord(
                trace_id=str(context["trace_id"]),
                span_id=str(context["span_id"]),
                parent_id=str(item["parent_id"]) if item.get("parent_id") else None,
                name=str(item["name"]),
                status_code=str(item.get("status_code", "UNSET")),
                attributes=attributes if isinstance(attributes, Mapping) else {},
            )
        )
    cursor = payload.get("next_cursor")
    return spans, str(cursor) if cursor else None


def sanitize_text(text: str, secret: str | None) -> str:
    if secret:
        return text.replace(secret, "<redacted>")
    return text


def _request_json(
    url: str,
    *,
    api_key: str | None = None,
    timeout: float = 30.0,
) -> Mapping[str, Any]:
    headers = {"Accept": "application/json"}
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    request = urllib.request.Request(url, headers=headers, method="GET")
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            body = response.read().decode("utf-8")
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", errors="replace")
        detail = sanitize_text(body, api_key)[:500]
        raise RuntimeError(f"HTTP {exc.code} from {url}: {detail}") from None
    except urllib.error.URLError as exc:
        detail = sanitize_text(str(exc.reason), api_key)
        raise RuntimeError(f"request failed for {url}: {detail}") from None

    try:
        payload = json.loads(body)
    except json.JSONDecodeError as exc:
        raise RuntimeError(f"invalid JSON from {url}: {exc}") from None
    if not isinstance(payload, Mapping):
        raise RuntimeError(f"JSON response from {url} is not an object")
    return payload


def check_model(base_url: str, model: str, api_key: str) -> dict[str, Any]:
    models = parse_model_ids(
        _request_json(build_model_url(base_url), api_key=api_key, timeout=30)
    )
    if model not in models:
        available = ", ".join(sorted(models)) or "<none>"
        raise RuntimeError(f"model {model!r} is unavailable; available models: {available}")
    return {"model": model, "available": True}


def fetch_all_spans(
    phoenix_url: str,
    project: str,
    start_time: str,
) -> list[SpanRecord]:
    spans: list[SpanRecord] = []
    cursor: str | None = None
    seen_cursors: set[str] = set()
    while True:
        payload = _request_json(
            build_spans_url(phoenix_url, project, start_time, cursor), timeout=10
        )
        page, cursor = parse_spans_page(payload)
        spans.extend(page)
        if not cursor:
            return spans
        if cursor in seen_cursors:
            raise RuntimeError(f"Phoenix repeated pagination cursor {cursor!r}")
        seen_cursors.add(cursor)


def _candidate_summary(spans: Sequence[SpanRecord]) -> str:
    if not spans:
        return "no candidate spans"
    rows = [
        {
            "trace_id": item.trace_id,
            "name": item.name,
            "span_id": item.span_id,
            "parent_id": item.parent_id,
            "status_code": item.status_code,
        }
        for item in spans[-50:]
    ]
    return json.dumps(rows, ensure_ascii=True, separators=(",", ":"))


def poll_for_trace(
    fetch_pages: Callable[[], Sequence[SpanRecord]],
    run_id: str,
    timeout: float,
    *,
    monotonic: Callable[[], float] = time.monotonic,
    sleep: Callable[[float], None] = time.sleep,
) -> TraceResult:
    deadline = monotonic() + max(timeout, 0)
    last_spans: Sequence[SpanRecord] = []
    last_error = "trace not queried"
    while True:
        last_spans = fetch_pages()
        try:
            return select_run_trace(last_spans, run_id)
        except AssertionError as exc:
            last_error = str(exc)

        remaining = deadline - monotonic()
        if remaining <= 0:
            raise TimeoutError(
                f"Phoenix trace not ready: {last_error}; "
                f"candidates={_candidate_summary(last_spans)}"
            )
        sleep(min(1.0, remaining))


def verify_phoenix(
    phoenix_url: str,
    project: str,
    start_time: str,
    run_id: str,
    timeout: float,
) -> dict[str, Any]:
    result = poll_for_trace(
        lambda: fetch_all_spans(phoenix_url, project, start_time),
        run_id,
        timeout,
    )
    return {
        "run_id": run_id,
        "trace_id": result.trace_id,
        "spans": [
            {
                "name": item.name,
                "runtime": item.runtime,
                "span_id": item.span_id,
                "parent_id": item.parent_id,
            }
            for item in result.required_spans
        ],
    }


def _build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Explicit trace E2E helper")
    subparsers = parser.add_subparsers(dest="command", required=True)

    model_parser = subparsers.add_parser("check-model")
    model_parser.add_argument("--base-url", required=True)
    model_parser.add_argument("--model", required=True)

    phoenix_parser = subparsers.add_parser("verify-phoenix")
    phoenix_parser.add_argument("--phoenix-url", required=True)
    phoenix_parser.add_argument("--project", default="default")
    phoenix_parser.add_argument("--start-time", required=True)
    phoenix_parser.add_argument("--run-id", required=True)
    phoenix_parser.add_argument("--timeout", type=float, default=45)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = _build_parser().parse_args(argv)
    try:
        if args.command == "check-model":
            api_key = os.environ.get("OPENAI_API_KEY", "").strip()
            if not api_key:
                raise RuntimeError("OPENAI_API_KEY is required")
            result = check_model(args.base_url, args.model, api_key)
        else:
            result = verify_phoenix(
                args.phoenix_url,
                args.project,
                args.start_time,
                args.run_id,
                args.timeout,
            )
    except Exception as exc:
        print(sanitize_text(str(exc), os.environ.get("OPENAI_API_KEY")), file=sys.stderr)
        return 1

    print(json.dumps(result, ensure_ascii=True, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
