"""Minimal honest trace boundary for external official benchmark receipts.

Terminal-Bench and tau2 execute their own upstream runner, outside the project
HarnessRun lifecycle.  This module records the receipt orchestration that the
project actually performs and distinguishes it from an end-to-end agent trace.
It never creates agent or chat spans unless the caller executes those scopes.
"""

from __future__ import annotations

import contextlib
import hashlib
import json
import os
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterator

from eval.harness.trace_contract import (
    SPAN_EVAL_INSTANCE,
    SPAN_EVAL_RUN,
    SPAN_SCORER_OFFICIAL,
    CapturedSpan,
)


SPAN_INVOKE_AGENT = "invoke_agent"
SPAN_CHAT = "chat"
_REQUIRED_BASE = (SPAN_EVAL_RUN, SPAN_EVAL_INSTANCE, SPAN_SCORER_OFFICIAL)
_REQUIRED_AGENT = (SPAN_INVOKE_AGENT, SPAN_CHAT)


def _get_tracer() -> Any:
    try:
        from opentelemetry import trace

        return trace.get_tracer(__name__)
    except Exception:  # pragma: no cover - telemetry absence is an honest gap
        return None


def _has_required_parentage(spans: list[CapturedSpan], trace_id: str) -> bool:
    """Check the receipt topology inside one trace without inferring it by name."""
    scoped = [span for span in spans if span.trace_id == trace_id]
    roots = {span.span_id for span in scoped if span.name == SPAN_EVAL_RUN}
    instances = {
        span.span_id
        for span in scoped
        if span.name == SPAN_EVAL_INSTANCE and span.parent_span_id in roots
    }
    agents = {
        span.span_id
        for span in scoped
        if span.name == SPAN_INVOKE_AGENT and span.parent_span_id in instances
    }
    has_chat = any(
        span.name == SPAN_CHAT and span.parent_span_id in agents for span in scoped
    )
    has_scorer = any(
        span.name == SPAN_SCORER_OFFICIAL and span.parent_span_id in instances
        for span in scoped
    )
    return bool(roots and instances and agents and has_chat and has_scorer)


@contextlib.contextmanager
def _span(
    tracer: Any, name: str, attributes: dict[str, str], *, context: Any | None = None
) -> Iterator[None]:
    if tracer is None:
        yield
        return
    kwargs: dict[str, Any] = {"attributes": attributes}
    if context is not None:
        kwargs["context"] = context
    with tracer.start_as_current_span(name, **kwargs):
        yield


@dataclass
class OfficialReceiptTrace:
    """Context manager for an upstream benchmark receipt's real local steps."""

    run_id: str
    instance_id: str
    agent_parent_context: Any | None = None

    def __post_init__(self) -> None:
        self._tracer = _get_tracer()
        self._attributes = {
            "eval.run_id": self.run_id,
            "eval.instance_id": self.instance_id,
        }
        self._run_scope: contextlib.AbstractContextManager[None] | None = None
        self._instance_scope: contextlib.AbstractContextManager[None] | None = None

    def __enter__(self) -> "OfficialReceiptTrace":
        self._run_scope = _span(self._tracer, SPAN_EVAL_RUN, self._attributes)
        self._run_scope.__enter__()
        self._instance_scope = _span(self._tracer, SPAN_EVAL_INSTANCE, self._attributes)
        self._instance_scope.__enter__()
        return self

    def __exit__(self, exc_type: Any, exc: Any, traceback: Any) -> None:
        assert self._instance_scope is not None and self._run_scope is not None
        self._instance_scope.__exit__(exc_type, exc, traceback)
        self._run_scope.__exit__(exc_type, exc, traceback)

    def scorer(self) -> contextlib.AbstractContextManager[None]:
        """Scope the real upstream result collection / official scorer read."""
        return _span(self._tracer, SPAN_SCORER_OFFICIAL, self._attributes)

    def agent(self) -> contextlib.AbstractContextManager[None]:
        """Only callers executing a real agent loop may create this span."""
        return _span(
            self._tracer,
            SPAN_INVOKE_AGENT,
            self._attributes,
            context=self.agent_parent_context,
        )

    def chat(self) -> contextlib.AbstractContextManager[None]:
        """Only callers executing a real provider turn may create this span."""
        return _span(self._tracer, SPAN_CHAT, self._attributes)

    @contextlib.contextmanager
    def worker_environment(self) -> Iterator[None]:
        """Temporarily provide the active instance's W3C context to workers.

        Terminal-Bench dispatches each trial through a thread executor and then
        an asyncio executor.  Neither copies ``contextvars``.  The official
        receipt runs exactly one trial, so a scoped process environment carrier
        avoids altering upstream code while retaining a true parent-child link.
        """
        keys = ("TERMINALBENCH_PARENT_TRACEPARENT", "TERMINALBENCH_PARENT_TRACESTATE")
        previous = {key: os.environ.get(key) for key in keys}
        try:
            from opentelemetry import propagate

            carrier: dict[str, str] = {}
            propagate.inject(carrier)
            for source, target in (
                ("traceparent", "TERMINALBENCH_PARENT_TRACEPARENT"),
                ("tracestate", "TERMINALBENCH_PARENT_TRACESTATE"),
            ):
                value = carrier.get(source)
                if value:
                    os.environ[target] = value
                else:
                    os.environ.pop(target, None)
            yield
        finally:
            for key, value in previous.items():
                if value is None:
                    os.environ.pop(key, None)
                else:
                    os.environ[key] = value


def evaluate_official_receipt_trace(
    spans: list[CapturedSpan], run_id: str, instance_id: str
) -> dict[str, Any]:
    """Classify a receipt trace without treating an external runner as opaque success."""
    matching = [
        span
        for span in spans
        if span.attributes.get("eval.run_id") == run_id
        and span.attributes.get("eval.instance_id") == instance_id
    ]
    observed = {span.name for span in matching}
    # Keep the diagnostic in lifecycle order; alphabetic output puts the child
    # before its parent and makes parentage reviews needlessly error-prone.
    present = [
        name
        for name in (*_REQUIRED_BASE, *_REQUIRED_AGENT)
        if name in observed
    ] + sorted(observed - set(_REQUIRED_BASE) - set(_REQUIRED_AGENT))
    missing_base = [name for name in _REQUIRED_BASE if name not in present]
    missing_agent = [name for name in _REQUIRED_AGENT if name not in present]
    trace_ids = sorted({span.trace_id for span in matching})
    complete_trace_ids = sorted(
        trace_id
        for trace_id in trace_ids
        if set(_REQUIRED_BASE).union(_REQUIRED_AGENT)
        <= {span.name for span in matching if span.trace_id == trace_id}
    )
    valid_trace_ids = [
        trace_id
        for trace_id in complete_trace_ids
        if _has_required_parentage(matching, trace_id)
    ]
    if missing_base:
        verdict = "FAIL"
    elif missing_agent:
        verdict = "INCOMPLETE_AGENT_TRACE"
    elif not complete_trace_ids:
        verdict = "SPLIT_TRACE"
    elif not valid_trace_ids:
        verdict = "INVALID_TOPOLOGY"
    else:
        verdict = "PASS"
    return {
        "run_id": run_id,
        "instance_id": instance_id,
        "verdict": verdict,
        "present_kinds": present,
        "missing_base_kinds": missing_base,
        "missing_agent_kinds": missing_agent,
        "trace_ids": trace_ids,
        "complete_trace_ids": complete_trace_ids,
        "valid_trace_ids": valid_trace_ids,
        "span_count": len(matching),
        "scope": "official-receipt-boundary",
    }


def write_official_receipt_trace(
    artifact_root: Path | str,
    capture: Any,
    run_id: str,
    instance_id: str,
    *,
    phoenix_url: str = "",
    phoenix_project: str = "default",
    phoenix_start_time: str = "",
    phoenix_reader: Any | None = None,
) -> dict[str, Any]:
    """Persist observed receipt trace and its honest completeness classification.

    The caller owns checksum finalization.  That ordering makes trace evidence
    part of the same immutable receipt as the copied upstream scorer output.
    """
    root = Path(artifact_root)
    traces = root / "traces"
    traces.mkdir(parents=True, exist_ok=True)
    summary = capture.summary()
    source_spans = capture.spans()
    if phoenix_url and phoenix_start_time:
        try:
            if phoenix_reader is None:
                from eval.harness.phoenix import read_run_spans

                phoenix_reader = read_run_spans
            readback_spans = phoenix_reader(
                phoenix_url,
                phoenix_project,
                phoenix_start_time,
                run_id,
                (instance_id,),
            )
            if not readback_spans:
                raise RuntimeError("Phoenix returned no spans for receipt run")
            source_spans = readback_spans
            summary.update(
                {
                    "collector": "phoenix-readback",
                    "phoenix_verified": True,
                    "phoenix_url": phoenix_url.rstrip("/"),
                    "phoenix_project": phoenix_project,
                    "phoenix_span_count": len(readback_spans),
                    "spans": [span.to_dict() for span in readback_spans],
                    "trace_ids": sorted({span.trace_id for span in readback_spans}),
                }
            )
        except Exception as exc:  # noqa: BLE001 - readback must fail closed
            summary.update(
                {
                    "collector": "otlp-http-unverified",
                    "phoenix_verified": False,
                    "phoenix_url": phoenix_url.rstrip("/"),
                    "phoenix_project": phoenix_project,
                    "phoenix_readback_error": type(exc).__name__,
                }
            )
    report = evaluate_official_receipt_trace(source_spans, run_id, instance_id)
    (traces / "trace-summary.json").write_text(
        json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    (traces / "span-assertion.json").write_text(
        json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    return report


def refresh_receipt_checksums(artifact_root: Path | str) -> Path:
    """Recursively pin a receipt after its final trace artifact is written."""
    root = Path(artifact_root)
    entries = []
    for path in sorted(root.rglob("*"), key=lambda item: item.as_posix()):
        if not path.is_file() or path.name == "checksums.sha256":
            continue
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        entries.append(f"{digest}  {path.relative_to(root).as_posix()}")
    target = root / "checksums.sha256"
    target.write_text("\n".join(entries) + "\n", encoding="utf-8")
    return target
