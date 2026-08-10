"""Propagate the eval join keys (``eval.run_id`` / ``eval.instance_id``) into
spans created by code that knows nothing about the harness.

Why this module exists
----------------------
The trace contract requires every span in a run to carry ``eval.run_id`` and
``eval.instance_id`` so a trace can be joined back to the run artifact.  The
harness sets those on the spans it creates itself, but the spans that matter
most are created elsewhere: ``chat`` inside
``orchestrator/runtime/conversation.py``, and the tool spans inside the eval
driver.  Threading a run id through every one of those call sites would couple
the orchestrator to the eval harness, and every new span site would silently
re-open the hole.

What this proves, precisely
---------------------------
Stamping is driven by OTel **baggage**, which is *context*-scoped, not
time-scoped.  A span is stamped only when it is created inside the context
attached by :func:`eval_join_context` — i.e. when it is a context-descendant of
the instance scope, either on the same thread or on a thread that inherited the
context.  So a stamped span proves *context propagation reached this span for
this logical run*.

What it does **not** prove: that the producing module was written to be
eval-aware, or that the work was semantically part of the instance.  A
deliberately time-correlated alternative — "stamp everything created while the
capture is open" — would prove strictly less, because an unrelated background
thread's span would be stamped with a run id it has nothing to do with, and the
contract's join check would then accept manufactured evidence.  That is the
distinction this module is built around, and
``tests/eval/test_trace_join.py`` pins it with an out-of-context control.
"""

from __future__ import annotations

import contextlib
from typing import Any, Iterator

#: Baggage keys.  These are the wire names; they are deliberately identical to
#: the span attribute names so an operator reading a trace sees one vocabulary.
BAGGAGE_RUN_ID = "eval.run_id"
BAGGAGE_INSTANCE_ID = "eval.instance_id"

#: The attributes a stamped span receives, in a fixed order.
JOIN_ATTRIBUTE_KEYS: tuple[str, ...] = (BAGGAGE_RUN_ID, BAGGAGE_INSTANCE_ID)


@contextlib.contextmanager
def eval_join_context(run_id: str, instance_id: str) -> Iterator[bool]:
    """Attach the join keys to the current OTel context for the duration.

    Yields ``True`` when the context was attached and ``False`` when it could
    not be (no OTel package installed).  Yielding instead of raising keeps
    telemetry non-load-bearing: a benchmark run must not fail because baggage
    is unavailable.

    ``instance_id`` may be empty for run-scoped work; the empty value is still
    attached, because the contract distinguishes "present but empty" (run
    scope) from "absent" (nobody propagated anything).
    """
    try:
        from opentelemetry import baggage, context as context_api
    except Exception:  # noqa: BLE001 - telemetry is never load-bearing
        yield False
        return

    try:
        ctx = baggage.set_baggage(BAGGAGE_RUN_ID, str(run_id))
        ctx = baggage.set_baggage(BAGGAGE_INSTANCE_ID, str(instance_id), context=ctx)
        token = context_api.attach(ctx)
    except Exception:  # noqa: BLE001 - telemetry is never load-bearing
        yield False
        return

    try:
        yield True
    finally:
        try:
            context_api.detach(token)
        except Exception:  # noqa: BLE001 - detach failure must not mask the body
            pass


def current_join_attributes() -> dict[str, str]:
    """Return the join attributes carried by the current context.

    Returns an empty dict when nothing was propagated — the caller must not
    invent values, because a fabricated ``eval.run_id`` is exactly the
    manufactured evidence the contract's join check exists to catch.
    """
    try:
        from opentelemetry import baggage
    except Exception:  # noqa: BLE001 - telemetry is never load-bearing
        return {}

    found: dict[str, str] = {}
    for key in JOIN_ATTRIBUTE_KEYS:
        try:
            value = baggage.get_baggage(key)
        except Exception:  # noqa: BLE001 - telemetry is never load-bearing
            continue
        if value is not None:
            found[key] = str(value)
    return found


class BaggageJoinSpanProcessor:
    """Span processor that stamps the join attributes at span start.

    Registered as a processor rather than folded into ``TraceCapture`` for two
    reasons.  First, production runs export through OTLP/Phoenix without any
    capture attached, and those spans need the join keys just as much.  Second,
    keeping the capture a pure observer means the artifact it writes is not
    also the thing that made the artifact pass — the stamping is auditable as
    its own registered component.

    Duck-typed against ``SpanProcessor`` exactly as ``TraceCapture`` is: the
    SDK performs no ``isinstance`` check (verified against SDK 1.39.1).
    """

    def on_start(self, span: Any, parent_context: Any = None) -> None:
        """Stamp join attributes onto *span* if the context carries them.

        Never overwrites a value the span already set for itself: an explicit
        attribute from the producing code is better evidence than an inherited
        one, and silently replacing it would hide a genuine disagreement.
        """
        attributes = current_join_attributes()
        if not attributes:
            return None
        existing = dict(getattr(span, "attributes", {}) or {})
        for key, value in attributes.items():
            if key in existing:
                continue
            try:
                span.set_attribute(key, value)
            except Exception:  # noqa: BLE001 - telemetry must never break the run
                return None
        return None

    def on_end(self, span: Any) -> None:  # noqa: D102
        return None

    def shutdown(self) -> None:  # noqa: D102
        return None

    def force_flush(self, timeout_millis: int = 30_000) -> bool:  # noqa: D102
        return True
