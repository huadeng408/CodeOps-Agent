"""O2: in-process capture of real OTel spans (design map §20.6.4).

Why in-process rather than a Phoenix query
------------------------------------------
§20.6.4's artifact list requires ``traces/trace-summary.json`` and
``traces/span-assertion.json`` for every real run, but Phoenix ``:6006`` is not
running and O3 (the live Phoenix E2E) is a later task with its own gate.  A
trace artifact that depended on a live collector would simply not exist, and the
temptation would then be to hand-write one.

So this module observes spans where they are created: it registers an OTel
``SpanProcessor`` on the tracer provider and records each span as the SDK ends
it.  The recording is therefore a by-product of real instrumented execution —
the same spans an exporter would ship.  Nothing here can produce a span that the
program did not actually run, which is the property §20.2 demands ("manually
sending spans and claiming the production chain works" is disqualifying).

What this module deliberately does **not** claim
-----------------------------------------------
It does not prove spans reached Phoenix, and :meth:`TraceCapture.summary`
records that explicitly via ``collector: "none"`` and ``phoenix_verified:
false``.  Closing that gap is O3's job.  Stating it inside the artifact keeps a
later reader from mistaking an in-process capture for a verified live trace.

Degradation
-----------
Every entry point is defensive.  §9.3 requires the business path to degrade
normally when the exporter is off, and telemetry must never be the reason a
benchmark run fails: a missing SDK, a non-SDK provider, or an exception inside
``on_end`` all end in "zero spans captured", never a raised exception.
"""

from __future__ import annotations

import threading
from typing import Any

from eval.harness.trace_contract import CapturedSpan, redact_if_secret

#: Recorded into the summary so a reviewer can tell how the spans were obtained
#: without inferring it from the file's shape.
CAPTURE_MODE = "in-process-span-processor"


def _format_trace_id(value: int) -> str:
    return format(value, "032x")


def _format_span_id(value: int) -> str:
    return format(value, "016x")


class TraceCapture:
    """Collects finished spans in memory as an OTel ``SpanProcessor``.

    Implemented by duck-typing rather than subclassing ``SpanProcessor`` so the
    harness keeps working when ``opentelemetry-sdk`` is absent: the SDK only
    appends the processor to a list and calls ``on_start`` / ``on_end`` /
    ``shutdown`` / ``force_flush`` on it, with no isinstance check
    (verified against the installed SDK 1.39.1).
    """

    def __init__(self) -> None:
        self._spans: list[CapturedSpan] = []
        self._lock = threading.Lock()
        self._attached = False
        self._recording = True

    # -- SpanProcessor protocol ------------------------------------------

    def on_start(self, span: Any, parent_context: Any = None) -> None:  # noqa: D102
        return None

    def on_end(self, span: Any) -> None:
        """Record one finished span.

        Wrapped in a bare except: an exception raised inside a span processor
        propagates into whatever business code just closed the span, which would
        let telemetry break a benchmark run.
        """
        if not self._recording:
            return None
        try:
            converted = self._convert(span)
        except Exception:  # noqa: BLE001 - telemetry must never break the run
            return None
        with self._lock:
            self._spans.append(converted)

    def stop(self) -> None:
        """Stop recording, keeping whatever was already captured.

        The OTel SDK offers no way to unregister a span processor, so a capture
        attached to the global provider would otherwise keep recording every
        later run's spans (and grow without bound in a process that executes
        several runs).  Flipping this flag makes the processor inert while
        leaving the collected spans readable.
        """
        self._recording = False

    def shutdown(self) -> None:  # noqa: D102
        return None

    def force_flush(self, timeout_millis: int = 30_000) -> bool:  # noqa: D102
        return True

    # -- conversion --------------------------------------------------------

    def _convert(self, span: Any) -> CapturedSpan:
        context = span.get_span_context()
        parent = getattr(span, "parent", None)
        parent_span_id = ""
        if parent is not None and getattr(parent, "span_id", 0):
            parent_span_id = _format_span_id(parent.span_id)

        # ``status.status_code`` is an enum whose name is OK / ERROR / UNSET.
        status = "UNSET"
        status_obj = getattr(span, "status", None)
        code = getattr(status_obj, "status_code", None)
        if code is not None:
            status = getattr(code, "name", str(code))

        attributes: dict[str, Any] = {}
        for key, value in dict(getattr(span, "attributes", {}) or {}).items():
            attributes[str(key)] = redact_if_secret(value)

        links: list[str] = []
        for link in getattr(span, "links", ()) or ():
            link_context = getattr(link, "context", None)
            if link_context is not None and getattr(link_context, "trace_id", 0):
                links.append(_format_trace_id(link_context.trace_id))

        return CapturedSpan(
            name=str(getattr(span, "name", "")),
            trace_id=_format_trace_id(context.trace_id),
            span_id=_format_span_id(context.span_id),
            parent_span_id=parent_span_id,
            status=status,
            attributes=attributes,
            links=tuple(links),
        )

    # -- installation ------------------------------------------------------

    def attach_to_provider(self, provider: Any) -> bool:
        """Register on *provider*; return whether registration succeeded.

        Returns ``False`` for a provider without ``add_span_processor`` — most
        importantly the default ``ProxyTracerProvider`` returned by
        ``trace.get_tracer_provider()`` when nothing has been configured.  The
        caller treats that as "no capture available", not as an error.
        """
        add = getattr(provider, "add_span_processor", None)
        if not callable(add):
            return False
        try:
            add(self)
        except Exception:  # noqa: BLE001 - telemetry must never break the run
            return False
        self._attached = True

        # Stamp the eval join keys onto spans created by code that knows
        # nothing about the harness (orchestrator ``chat``, driver tool spans).
        # Registered as its own processor so the capture stays a pure observer:
        # the component that writes the artifact is not also the component that
        # makes the artifact pass.  Failure here is non-fatal — an unstamped
        # span is reported as a missing join attribute, which is the honest
        # outcome, rather than aborting the run.
        try:
            from eval.harness.trace_join import BaggageJoinSpanProcessor

            add(BaggageJoinSpanProcessor())
        except Exception:  # noqa: BLE001 - telemetry must never break the run
            pass
        return True

    def install(self) -> bool:
        """Attach to the global tracer provider, creating one if needed.

        Two paths, both required:

        * A provider is already registered (e.g. ``orchestrator/config/env.py``
          set one up with the Phoenix OTLP exporter).  The capture is added
          alongside it, so the same spans are both exported and recorded — this
          is what makes the artifact compatible with O3 instead of competing
          with it.
        * Nothing is registered.  An SDK ``TracerProvider`` is created and set,
          so harness spans are recorded even with no collector anywhere.
        """
        try:
            from opentelemetry import trace as trace_api
        except Exception:  # noqa: BLE001 - OTel not installed
            return False

        try:
            provider = trace_api.get_tracer_provider()
            if self.attach_to_provider(provider):
                return True

            from opentelemetry.sdk.trace import TracerProvider

            new_provider = TracerProvider()
            if not self.attach_to_provider(new_provider):
                return False
            trace_api.set_tracer_provider(new_provider)
            return True
        except Exception:  # noqa: BLE001 - telemetry must never break the run
            return False

    # -- readout -----------------------------------------------------------

    def spans(self) -> list[CapturedSpan]:
        with self._lock:
            return list(self._spans)

    def summary(self) -> dict[str, Any]:
        """The ``traces/trace-summary.json`` payload.

        ``collector``/``phoenix_verified`` are stated rather than implied: this
        artifact proves spans were produced, not that they were ingested by a
        collector.  Conflating the two is exactly the "五 span 合成链只能证明
        基础连通" error §20.3.1 records.
        """
        spans = self.spans()
        trace_ids = sorted({span.trace_id for span in spans})
        return {
            "capture_mode": CAPTURE_MODE,
            "collector": "none",
            "phoenix_verified": False,
            "attached": self._attached,
            "span_count": len(spans),
            "trace_ids": trace_ids,
            "spans": [span.to_dict() for span in spans],
        }
