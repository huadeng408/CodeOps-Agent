"""Pin what baggage-driven join stamping proves — and what it must not.

The risk this file guards against: if the stamping were time-scoped ("stamp
everything created while the capture is open"), then an unrelated span would be
stamped with a run id it has nothing to do with, and the trace contract's join
check would accept manufactured evidence.  The control test here is
``test_span_created_outside_the_join_context_is_not_stamped`` — without it, the
other assertions cannot distinguish real propagation from blanket stamping.
"""

from __future__ import annotations

import pytest

from eval.harness.trace_join import (
    BAGGAGE_INSTANCE_ID,
    BAGGAGE_RUN_ID,
    BaggageJoinSpanProcessor,
    current_join_attributes,
    eval_join_context,
)


@pytest.fixture()
def isolated_provider():
    """A fresh SDK provider with the join processor, never touching the global.

    ``set_tracer_provider`` is one-shot and cannot be undone, so tests that
    register globally poison every later test in the process.
    """
    from opentelemetry.sdk.trace import TracerProvider
    from opentelemetry.sdk.trace.export import SimpleSpanProcessor
    from opentelemetry.sdk.trace.export.in_memory_span_exporter import (
        InMemorySpanExporter,
    )

    provider = TracerProvider()
    exporter = InMemorySpanExporter()
    provider.add_span_processor(BaggageJoinSpanProcessor())
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    yield provider, exporter
    provider.shutdown()


def _emit(provider, name: str) -> None:
    tracer = provider.get_tracer(__name__)
    with tracer.start_as_current_span(name):
        pass


def _only(exporter) -> dict:
    spans = exporter.get_finished_spans()
    assert len(spans) == 1, f"expected exactly one span, got {len(spans)}"
    return dict(spans[0].attributes or {})


# ---------------------------------------------------------------------------
# baggage plumbing
# ---------------------------------------------------------------------------


def test_join_context_exposes_both_keys() -> None:
    with eval_join_context("run-1", "inst-1") as attached:
        assert attached is True
        assert current_join_attributes() == {
            BAGGAGE_RUN_ID: "run-1",
            BAGGAGE_INSTANCE_ID: "inst-1",
        }


def test_join_context_is_removed_on_exit() -> None:
    with eval_join_context("run-1", "inst-1"):
        pass
    assert current_join_attributes() == {}


def test_empty_instance_id_is_still_propagated() -> None:
    """Run-scoped work: present-but-empty differs from absent in the contract."""
    with eval_join_context("run-1", ""):
        assert current_join_attributes() == {
            BAGGAGE_RUN_ID: "run-1",
            BAGGAGE_INSTANCE_ID: "",
        }


def test_current_join_attributes_invents_nothing_without_context() -> None:
    assert current_join_attributes() == {}


# ---------------------------------------------------------------------------
# stamping — and the control that gives it meaning
# ---------------------------------------------------------------------------


def test_span_created_inside_the_join_context_is_stamped(isolated_provider) -> None:
    provider, exporter = isolated_provider
    with eval_join_context("run-42", "inst-7"):
        _emit(provider, "chat")
    attributes = _only(exporter)
    assert attributes["eval.run_id"] == "run-42"
    assert attributes["eval.instance_id"] == "inst-7"


def test_span_created_outside_the_join_context_is_not_stamped(
    isolated_provider,
) -> None:
    """The control group.  Stamping is context-scoped, not time-scoped.

    If this fails, the processor is stamping blindly and the contract's join
    check no longer proves the span belongs to the run being reported.
    """
    provider, exporter = isolated_provider
    _emit(provider, "unrelated-background-work")
    attributes = _only(exporter)
    assert "eval.run_id" not in attributes
    assert "eval.instance_id" not in attributes


def test_stamping_stops_after_the_context_exits(isolated_provider) -> None:
    provider, exporter = isolated_provider
    with eval_join_context("run-42", "inst-7"):
        pass
    _emit(provider, "after")
    assert "eval.run_id" not in _only(exporter)


def test_nested_context_wins_for_spans_inside_it(isolated_provider) -> None:
    provider, exporter = isolated_provider
    with eval_join_context("run-outer", "inst-outer"):
        with eval_join_context("run-inner", "inst-inner"):
            _emit(provider, "inner")
    assert _only(exporter)["eval.run_id"] == "run-inner"


def test_explicit_attribute_is_not_overwritten(isolated_provider) -> None:
    """An attribute the producing code set itself is better evidence.

    Silently replacing it would hide a real disagreement between what the
    producer believes and what the context carries.
    """
    provider, exporter = isolated_provider
    tracer = provider.get_tracer(__name__)
    with eval_join_context("run-from-baggage", "inst-1"):
        with tracer.start_as_current_span(
            "explicit", attributes={"eval.run_id": "run-from-producer"}
        ):
            pass
    attributes = _only(exporter)
    assert attributes["eval.run_id"] == "run-from-producer"
    assert attributes["eval.instance_id"] == "inst-1"


def test_child_thread_inherits_when_context_is_carried(isolated_provider) -> None:
    """Context does not cross threads by itself; carrying it explicitly works.

    Documents the real boundary so nobody reads an unstamped span on a worker
    thread as a propagation bug.
    """
    import threading

    from opentelemetry import context as context_api

    provider, exporter = isolated_provider
    with eval_join_context("run-t", "inst-t"):
        carried = context_api.get_current()

    def worker() -> None:
        token = context_api.attach(carried)
        try:
            _emit(provider, "on-worker-thread")
        finally:
            context_api.detach(token)

    thread = threading.Thread(target=worker)
    thread.start()
    thread.join()
    assert _only(exporter)["eval.run_id"] == "run-t"


def test_processor_never_raises_into_business_code() -> None:
    """A span whose ``set_attribute`` explodes must not break the caller."""

    class _Exploding:
        attributes: dict = {}

        def set_attribute(self, key, value):  # noqa: ANN001, ANN202
            raise RuntimeError("boom")

    processor = BaggageJoinSpanProcessor()
    with eval_join_context("run-1", "inst-1"):
        processor.on_start(_Exploding())  # must not raise


def test_processor_is_inert_without_context() -> None:
    """No baggage → no ``set_attribute`` call at all, not an empty-string stamp."""
    calls: list[tuple] = []

    class _Recording:
        attributes: dict = {}

        def set_attribute(self, key, value):  # noqa: ANN001, ANN202
            calls.append((key, value))

    BaggageJoinSpanProcessor().on_start(_Recording())
    assert calls == []
