"""Receipt-bound spans for project-owned Terminal-Bench agent execution."""

from __future__ import annotations

import os

from eval.harness.official_receipt_trace import OfficialReceiptTrace


def receipt_agent_trace() -> OfficialReceiptTrace | None:
    """Return receipt context only when the official launcher supplied both IDs."""
    run_id = os.environ.get("TERMINALBENCH_RUN_ID", "")
    instance_id = os.environ.get("TERMINALBENCH_INSTANCE_ID", "")
    if not run_id or not instance_id:
        return None
    parent_context = None
    traceparent = os.environ.get("TERMINALBENCH_PARENT_TRACEPARENT", "")
    if traceparent:
        try:
            from opentelemetry import propagate

            carrier = {"traceparent": traceparent}
            tracestate = os.environ.get("TERMINALBENCH_PARENT_TRACESTATE", "")
            if tracestate:
                carrier["tracestate"] = tracestate
            parent_context = propagate.extract(carrier)
        except Exception:  # noqa: BLE001 - tracing must not break the agent
            parent_context = None
    return OfficialReceiptTrace(run_id, instance_id, agent_parent_context=parent_context)
