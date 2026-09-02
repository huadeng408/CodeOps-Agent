"""O2: a real ``rerank`` span at the real reranker call site (§20.6.4 item 2).

The trace contract requires the rerank span at the actual
call site, not in a test.  The call site is ``rerank_context`` inside
``orchestrator/rag/graph.py``'s ``build_graph`` closure, which calls
``backend.rerank_context``.

Two things are tested separately, because passing one while failing the other is
exactly how an instrumentation gate gets faked:

1. the span helper genuinely produces an OTel span with the contract's attribute
   names (tested by driving a real ``TracerProvider``);
2. the production call site actually calls that helper (tested by AST, so an
   indirect ``getattr`` lookup cannot satisfy the assertion).

Point 2 follows the precedent set in §24.2: an AST assertion was adopted there
after an indirect call let a "gate exists" claim pass while static audit could
not confirm it.  §20.2 lists "手工向 Phoenix 发送 span 却宣称生产链路已贯通" as
disqualifying, and a helper nobody calls is the same failure in a different
shape.
"""

from __future__ import annotations

import ast
from pathlib import Path

import pytest

from orchestrator.rag.trace import otel_span, query_hash

GRAPH_PATH = Path(__file__).resolve().parents[2] / "orchestrator" / "rag" / "graph.py"


# ---------------------------------------------------------------------------
# query_hash: the query must never reach a span in raw form (§9.2)
# ---------------------------------------------------------------------------


def test_query_hash_is_deterministic() -> None:
    assert query_hash("how do I configure postgres") == query_hash(
        "how do I configure postgres"
    )


def test_query_hash_does_not_leak_the_query() -> None:
    query = "my secret internal question about acme corp"
    digest = query_hash(query)
    assert query not in digest
    for word in query.split():
        assert word not in digest


def test_query_hash_distinguishes_queries() -> None:
    assert query_hash("alpha") != query_hash("beta")


def test_query_hash_of_empty_query_is_empty() -> None:
    """An absent query must be visibly absent rather than hashed into a
    plausible-looking constant."""
    assert query_hash("") == ""


# ---------------------------------------------------------------------------
# otel_span: real spans, and safe degradation
# ---------------------------------------------------------------------------


def test_otel_span_creates_a_real_span() -> None:
    pytest.importorskip("opentelemetry.sdk.trace")
    from opentelemetry.sdk.trace import TracerProvider

    from eval.harness.trace_capture import TraceCapture

    provider = TracerProvider()
    capture = TraceCapture()
    capture.attach_to_provider(provider)

    with otel_span("rerank", {"rag.reranker_applied": True}, tracer=provider.get_tracer(__name__)):
        pass

    spans = capture.spans()
    assert len(spans) == 1
    assert spans[0].name == "rerank"
    assert spans[0].attributes["rag.reranker_applied"] is True


def test_otel_span_records_error_status() -> None:
    """§20.6.4 item 2: error/timeout/cancel must still end the span."""
    pytest.importorskip("opentelemetry.sdk.trace")
    from opentelemetry.sdk.trace import TracerProvider

    from eval.harness.trace_capture import TraceCapture

    provider = TracerProvider()
    capture = TraceCapture()
    capture.attach_to_provider(provider)

    with pytest.raises(RuntimeError):
        with otel_span("rerank", {}, tracer=provider.get_tracer(__name__)):
            raise RuntimeError("reranker unreachable")

    assert capture.spans()[0].status == "ERROR"


@pytest.fixture()
def no_global_tracer_provider():
    """Force "nothing is configured" for the duration of one test.

    ``trace_api.set_tracer_provider`` is one-shot and cannot be undone, so any
    earlier test that installed an SDK provider — ``TraceCapture.install()``
    does exactly that when it finds none — leaves one registered for the whole
    session.  A test asserting on the *unconfigured* behaviour therefore passes
    alone and fails in suite order, which makes the verdict a function of test
    ordering rather than of the code.

    Reaching into the private global is deliberate and contained: it is the only
    way to restore the pre-configuration state, and the original value is put
    back afterwards.
    """
    from opentelemetry import trace as trace_api

    saved_provider = trace_api._TRACER_PROVIDER
    saved_once = trace_api._TRACER_PROVIDER_SET_ONCE._done
    trace_api._TRACER_PROVIDER = None
    trace_api._TRACER_PROVIDER_SET_ONCE._done = False
    try:
        yield
    finally:
        trace_api._TRACER_PROVIDER = saved_provider
        trace_api._TRACER_PROVIDER_SET_ONCE._done = saved_once


def test_otel_span_degrades_when_no_provider_is_configured(
    no_global_tracer_provider,
) -> None:
    """§9.3: the business path must work when telemetry is unavailable.

    ``tracer=None`` means "resolve the global tracer" — that is how the
    production call site in ``graph.py`` invokes this, so it must NOT mean "skip
    the span".  With no SDK provider registered, OTel hands back a
    non-recording span: the body still runs, nothing raises, and nothing is
    exported.  That is the degradation §9.3 asks for.

    (An earlier version of this test asserted ``span is None`` here.  That was
    the test encoding a false spec: had the implementation been changed to
    satisfy it, the real call site would have stopped producing spans whenever
    it passed no explicit tracer — i.e. always.)
    """
    executed = False
    with otel_span("rerank", {"a": 1}) as span:
        executed = True
        assert span is not None
        assert span.is_recording() is False
    assert executed


def test_otel_span_survives_a_broken_tracer() -> None:
    class _Broken:
        def start_as_current_span(self, *args, **kwargs):
            raise RuntimeError("tracer exploded")

    with otel_span("rerank", {}, tracer=_Broken()) as span:
        assert span is None


# ---------------------------------------------------------------------------
# The production call site really calls it
# ---------------------------------------------------------------------------


def _rerank_context_node() -> ast.AsyncFunctionDef:
    tree = ast.parse(GRAPH_PATH.read_text(encoding="utf-8"))
    for node in ast.walk(tree):
        if isinstance(node, ast.AsyncFunctionDef) and node.name == "rerank_context":
            return node
    raise AssertionError("rerank_context node not found in orchestrator/rag/graph.py")


def test_rerank_context_calls_otel_span_directly() -> None:
    """Direct call, so static audit can prove the instrumentation exists."""
    node = _rerank_context_node()
    called = {
        child.func.id
        for child in ast.walk(node)
        if isinstance(child, ast.Call) and isinstance(child.func, ast.Name)
    }
    assert "otel_span" in called, (
        "rerank_context must call otel_span() directly; an indirect lookup "
        "leaves the rerank span unprovable by static audit (§24.2 precedent)"
    )


def test_rerank_context_hashes_the_query() -> None:
    node = _rerank_context_node()
    called = {
        child.func.id
        for child in ast.walk(node)
        if isinstance(child, ast.Call) and isinstance(child.func, ast.Name)
    }
    assert "query_hash" in called, (
        "the rerank span must carry rag.query_hash, not the raw query (§9.2)"
    )


def test_rerank_span_uses_the_contract_span_name() -> None:
    """The span name must match the trace contract's ``rerank`` kind, or the
    contract validator reports the kind as missing even though it ran.

    Asserted over the AST rather than the raw source: a substring check on
    ``otel_span("rerank"`` breaks the moment the call is reformatted across
    lines, which would make this gate fail for a cosmetic reason and invite
    deleting it.
    """
    from eval.harness.trace_contract import SPAN_RERANK

    node = _rerank_context_node()
    names = [
        child.args[0].value
        for child in ast.walk(node)
        if isinstance(child, ast.Call)
        and isinstance(child.func, ast.Name)
        and child.func.id == "otel_span"
        and child.args
        and isinstance(child.args[0], ast.Constant)
    ]
    assert SPAN_RERANK in names
