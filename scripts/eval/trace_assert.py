"""trace_assert — join eval artifacts to Phoenix traces (plan Task 6.2).

Each eval instance injects run_id/instance_id/traceparent; this CLI queries
Phoenix (via the same HTTP span API the e2e runner uses) and asserts the
trace contains the required span kinds:
  - Go root (invoke_agent)
  - Python LLM chat
  - retrieval (rag.retrieve)
  - tool execution
  - scorer (when scoring spans exist)

Exits non-zero when the join fails; never stops a running Phoenix and never
prints API keys.
"""

from __future__ import annotations

import argparse
import json
import sys
import time
from pathlib import Path
from urllib.parse import quote

import urllib.request


def _get_json(url: str) -> dict:
    with urllib.request.urlopen(url, timeout=15) as response:
        return json.loads(response.read().decode("utf-8"))


def _spans_url(phoenix_url: str, project: str, start_time: str) -> str:
    base = phoenix_url.rstrip("/")
    query = f"project_name={quote(project)}&start_time={quote(start_time)}&limit=500"
    return f"{base}/v1/spans?{query}"


def required_span_kinds() -> list[str]:
    """The span names that must be present in a joined run trace."""
    return ["invoke_agent", "chat", "rag.retrieve", "execute_tool", "scorer"]


def find_run_trace(
    phoenix_url: str,
    project: str,
    run_id: str,
    start_time: str,
) -> tuple[str, list[dict]]:
    """Find the trace carrying the run_id marker; returns (trace_id, spans)."""
    url = _spans_url(phoenix_url, project, start_time)
    payload = _get_json(url)
    spans = payload.get("data", []) if isinstance(payload, dict) else payload
    marker = f"TRACE_E2E_FIXTURE:{run_id}"
    matching = [
        item for item in spans
        if marker in str(_nested(item.get("attributes", {}), "gen_ai.tool.call.result") or "")
    ]
    if not matching:
        raise AssertionError(f"no span carries run marker {marker}")
    trace_ids = {item.get("trace_id") for item in matching}
    if len(trace_ids) != 1:
        raise AssertionError(f"run marker matched multiple traces: {sorted(trace_ids)}")
    trace_id = next(iter(trace_ids))
    return trace_id, [item for item in spans if item.get("trace_id") == trace_id]


def assert_span_kinds(trace_id: str, spans: list[dict], required: list[str]) -> list[str]:
    """Assert the trace contains each required span name; returns missing."""
    names = {str(item.get("name", "")) for item in spans}
    missing = [kind for kind in required if not any(kind in name for name in names)]
    if missing:
        raise AssertionError(
            f"trace {trace_id} missing required span kinds: {missing}; "
            f"present: {sorted(names)}"
        )
    return []


def assert_rag_schema(trace_id: str, spans: list[dict]) -> None:
    """Assert retrieval spans carry the standardized rag.* attributes."""
    rag_spans = [item for item in spans if "rag.retrieve" in str(item.get("name", ""))]
    if not rag_spans:
        return  # retrieval not instrumented in this run; not an error
    first = rag_spans[0]
    attrs = first.get("attributes", {})
    for key in ("rag.query_hash", "rag.retrieval_mode", "rag.top_n"):
        if key not in attrs and not _nested(attrs, key):
            raise AssertionError(f"trace {trace_id} retrieval span missing {key}")


def _nested(attributes: dict, dotted: str) -> object:
    parts = dotted.split(".")
    current: object = attributes
    for part in parts:
        if not isinstance(current, dict):
            return None
        current = current.get(part)
    return current


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Join eval artifacts to Phoenix traces")
    parser.add_argument("--phoenix-url", required=True, help="Phoenix base URL (e.g. http://127.0.0.1:6006)")
    parser.add_argument("--project", default="code-agent", help="Phoenix project name")
    parser.add_argument("--run-id", required=True, help="eval run id injected into every instance")
    parser.add_argument("--start-time", required=True, help="ISO start time for the span query")
    parser.add_argument("--out", default="", help="optional JSON report path")
    args = parser.parse_args(argv)

    try:
        trace_id, spans = find_run_trace(args.phoenix_url, args.project, args.run_id, args.start_time)
        missing = assert_span_kinds(trace_id, spans, required_span_kinds())
        assert_rag_schema(trace_id, spans)
    except AssertionError as exc:
        print(f"TRACE JOIN FAILED: {exc}", file=sys.stderr)
        return 1
    except Exception as exc:  # noqa: BLE001 - CLI surface
        print(f"TRACE JOIN ERROR: {exc}", file=sys.stderr)
        return 2

    report = {
        "run_id": args.run_id,
        "trace_id": trace_id,
        "span_count": len(spans),
        "required_kinds": required_span_kinds(),
        "missing": missing,
    }
    if args.out:
        Path(args.out).parent.mkdir(parents=True, exist_ok=True)
        Path(args.out).write_text(json.dumps(report, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(report, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
