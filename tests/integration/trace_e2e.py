from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Mapping, Sequence


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
