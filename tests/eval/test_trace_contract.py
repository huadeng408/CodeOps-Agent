"""O1: the unified, versioned trace acceptance contract (design map §20.6.4).

Before this module existed the repository carried *three* disagreeing trace
contracts:

* ``scripts/eval/trace_assert.py`` required 5 span kinds,
* ``tests/integration/trace_e2e.py`` required root + one Read tool + 2 chats,
* the Phoenix trace on disk was a 5-span synthetic chain with no RAG spans.

§20.6.4 item 1 asks for one versioned schema covering root, instance,
agent/chat, tool, retrieve, embedding, rerank (when enabled) and official
scorer, with every span in the same trace (or joined by a legal span link) and
carrying ``eval.run_id`` / ``eval.instance_id`` plus non-sensitive pins.

The verdict semantics are the anti-cheating core of this module and are tested
hardest: a capture that is missing required span kinds must report
``INCOMPLETE`` and name them, and an *empty* capture must never report
``PASS``.  §20.2 forbids "only ran mock/contract tests but claimed the
production chain works", so the contract has to be able to say "these kinds
were never produced" out loud.
"""

from __future__ import annotations

import pytest

from eval.harness.trace_contract import (
    CONTRACT_VERSION,
    JOIN_ATTRIBUTES,
    REQUIRED_SPAN_KINDS,
    SPAN_EVAL_INSTANCE,
    SPAN_EVAL_RUN,
    SPAN_SCORER_OFFICIAL,
    VERDICT_FAIL,
    VERDICT_INCOMPLETE,
    VERDICT_PASS,
    CapturedSpan,
    SpanKind,
    evaluate_trace_contract,
    span_kinds,
)


def _span(
    name: str,
    *,
    trace_id: str = "t1",
    span_id: str = "s1",
    parent_span_id: str = "",
    status: str = "OK",
    attributes: dict | None = None,
    links: tuple[str, ...] = (),
) -> CapturedSpan:
    attrs = {"eval.run_id": "run-1", "eval.instance_id": "inst-1"}
    if attributes is not None:
        attrs.update(attributes)
    return CapturedSpan(
        name=name,
        trace_id=trace_id,
        span_id=span_id,
        parent_span_id=parent_span_id,
        status=status,
        attributes=attrs,
        links=links,
    )


def _full_capture() -> list[CapturedSpan]:
    """One span per required kind, all in the same trace, properly parented."""
    spans: list[CapturedSpan] = []
    parent = ""
    for index, kind in enumerate(REQUIRED_SPAN_KINDS):
        attrs: dict[str, object] = {}
        if kind == SPAN_EVAL_RUN:
            attrs["git.commit"] = "abc1234"
            attrs["eval.benchmark"] = "swebench"
        if kind.startswith("rag."):
            attrs.update(
                {
                    "rag.query_hash": "qh",
                    "rag.corpus_generation": "techdocs-2026-07-30-v1",
                    "rag.index_name": "knowledge_base_v2_bge_m3",
                    "rag.retrieval_mode": "hybrid",
                }
            )
        spans.append(
            _span(
                kind,
                span_id=f"s{index}",
                parent_span_id=parent,
                attributes=attrs,
            )
        )
        parent = f"s{index}"
    return spans


# ---------------------------------------------------------------------------
# Versioned schema shape
# ---------------------------------------------------------------------------


def test_contract_version_is_explicit_and_versioned() -> None:
    """§20.6.4 says 'versioned schema'; an unversioned contract cannot be
    referenced by an artifact whose verdict must be reproducible later."""
    assert isinstance(CONTRACT_VERSION, str)
    assert CONTRACT_VERSION.startswith("v")


def test_schema_covers_every_span_kind_the_design_map_names() -> None:
    """§20.6.4 item 1 enumerates the kinds the schema must cover."""
    names = {kind.name for kind in span_kinds()}
    # root, instance, agent/chat, tool, retrieve, embedding, rerank, scorer
    assert SPAN_EVAL_RUN in names
    assert SPAN_EVAL_INSTANCE in names
    assert "invoke_agent" in names
    assert "chat" in names
    assert "execute_tool" in names
    assert "rag.retrieve" in names
    assert "embedding" in names
    assert "rerank" in names
    assert SPAN_SCORER_OFFICIAL in names


def test_rerank_is_conditional_not_required() -> None:
    """§9.2 / §20.6.4 both qualify rerank with '启用时' (when enabled), so a run
    with the reranker off must not be penalised for its absence."""
    by_name = {kind.name: kind for kind in span_kinds()}
    assert by_name["rerank"].required is False
    assert "rerank" not in REQUIRED_SPAN_KINDS


def test_every_span_kind_declares_a_producer() -> None:
    """Diagnostics: when a kind is missing we must be able to say *which*
    process failed to emit it, otherwise 'INCOMPLETE' gives the next window
    nothing to act on.

    ``invoke_agent``/``execute_tool`` are attributed to ``agent-runtime``, not
    ``go-agent``: the headless eval driver runs those tools in-process, so
    naming the Go agent sent the reader to start a server that path never
    contacts — an unactionable verdict of exactly the kind this field prevents.
    """
    for kind in span_kinds():
        assert kind.producer in {
            "harness",
            "agent-runtime",
            "orchestrator",
        }
        assert isinstance(kind, SpanKind)


def test_join_attributes_match_design_map_section_9_2() -> None:
    assert JOIN_ATTRIBUTES == (
        "eval.run_id",
        "eval.instance_id",
        "git.commit",
        "rag.query_hash",
        "rag.corpus_generation",
        "rag.index_name",
        "rag.retrieval_mode",
    )


# ---------------------------------------------------------------------------
# Verdict semantics
# ---------------------------------------------------------------------------


def test_full_capture_passes() -> None:
    report = evaluate_trace_contract(_full_capture(), run_id="run-1")
    assert report["verdict"] == VERDICT_PASS, report["problems"]
    assert report["problems"] == []
    assert report["contract_version"] == CONTRACT_VERSION


def test_empty_capture_never_passes() -> None:
    """The single most important assertion in this file.

    A run that produced no spans at all must not yield a green trace
    artifact.  If it did, the H5 gate's 'trace' item could be satisfied by
    doing nothing.
    """
    report = evaluate_trace_contract([], run_id="run-1")
    assert report["verdict"] != VERDICT_PASS
    assert report["verdict"] == VERDICT_INCOMPLETE
    assert report["span_count"] == 0
    assert any("no spans" in problem for problem in report["problems"])


def test_missing_required_kinds_are_named_and_incomplete() -> None:
    """Harness-only capture: this is the *real* current state (Phoenix down,
    Go agent not running).  It must be reported honestly, not as PASS."""
    spans = [
        _span(SPAN_EVAL_RUN, span_id="s0", attributes={"git.commit": "abc1234"}),
        _span(SPAN_EVAL_INSTANCE, span_id="s1", parent_span_id="s0"),
        _span(SPAN_SCORER_OFFICIAL, span_id="s2", parent_span_id="s1"),
    ]
    report = evaluate_trace_contract(spans, run_id="run-1")
    assert report["verdict"] == VERDICT_INCOMPLETE
    assert set(report["missing_required_kinds"]) == {
        "invoke_agent",
        "chat",
        "execute_tool",
        "rag.retrieve",
        "embedding",
    }
    # The producer of each missing kind must be attributed.  ``invoke_agent``
    # and ``execute_tool`` belong to whichever runtime ran the agent loop, which
    # on the headless path is this Python process rather than the Go agent.
    assert report["missing_by_producer"]["agent-runtime"] == [
        "invoke_agent",
        "execute_tool",
    ]
    assert report["missing_by_producer"]["orchestrator"]


def test_present_kinds_are_reported() -> None:
    spans = [_span(SPAN_EVAL_RUN, attributes={"git.commit": "abc"})]
    report = evaluate_trace_contract(spans, run_id="run-1")
    assert SPAN_EVAL_RUN in report["present_kinds"]


# ---------------------------------------------------------------------------
# Same-trace / span-link rule
# ---------------------------------------------------------------------------


def test_split_trace_without_link_fails() -> None:
    """§20.6.4: 'all spans in the same trace or using a legal span link'."""
    spans = _full_capture()
    spans[-1] = CapturedSpan(
        name=spans[-1].name,
        trace_id="OTHER-TRACE",
        span_id="sX",
        parent_span_id="",
        status="OK",
        attributes=dict(spans[-1].attributes),
        links=(),
    )
    report = evaluate_trace_contract(spans, run_id="run-1")
    assert report["verdict"] == VERDICT_FAIL
    assert any("trace" in problem.lower() for problem in report["problems"])


def test_split_trace_with_legal_span_link_is_accepted() -> None:
    """A linked span is explicitly permitted, so it must not be a FAIL."""
    spans = _full_capture()
    spans[-1] = CapturedSpan(
        name=spans[-1].name,
        trace_id="OTHER-TRACE",
        span_id="sX",
        parent_span_id="",
        status="OK",
        attributes=dict(spans[-1].attributes),
        links=("t1",),  # links back to the primary trace
    )
    report = evaluate_trace_contract(spans, run_id="run-1")
    assert report["verdict"] == VERDICT_PASS, report["problems"]


def test_primary_trace_id_is_reported() -> None:
    report = evaluate_trace_contract(_full_capture(), run_id="run-1")
    assert report["primary_trace_id"] == "t1"


# ---------------------------------------------------------------------------
# Join attributes
# ---------------------------------------------------------------------------


def test_missing_run_id_fails() -> None:
    spans = _full_capture()
    spans[1] = CapturedSpan(
        name=SPAN_EVAL_INSTANCE,
        trace_id="t1",
        span_id="s1",
        parent_span_id="s0",
        status="OK",
        attributes={"eval.instance_id": "inst-1"},  # no eval.run_id
        links=(),
    )
    report = evaluate_trace_contract(spans, run_id="run-1")
    assert report["verdict"] == VERDICT_FAIL
    assert any("eval.run_id" in problem for problem in report["problems"])


def test_run_id_mismatch_fails() -> None:
    """A trace joined to the *wrong* run is worse than no trace: it would
    attribute one run's evidence to another."""
    report = evaluate_trace_contract(_full_capture(), run_id="a-different-run")
    assert report["verdict"] == VERDICT_FAIL
    assert any("run_id" in problem for problem in report["problems"])


def test_eval_run_span_requires_git_commit() -> None:
    spans = _full_capture()
    spans[0] = CapturedSpan(
        name=SPAN_EVAL_RUN,
        trace_id="t1",
        span_id="s0",
        parent_span_id="",
        status="OK",
        attributes={"eval.run_id": "run-1", "eval.instance_id": "inst-1"},
        links=(),
    )
    report = evaluate_trace_contract(spans, run_id="run-1")
    assert report["verdict"] == VERDICT_FAIL
    assert any("git.commit" in problem for problem in report["problems"])


def test_retrieve_span_requires_rag_pins() -> None:
    spans = _full_capture()
    for index, span in enumerate(spans):
        if span.name == "rag.retrieve":
            spans[index] = CapturedSpan(
                name="rag.retrieve",
                trace_id="t1",
                span_id=span.span_id,
                parent_span_id=span.parent_span_id,
                status="OK",
                attributes={"eval.run_id": "run-1", "eval.instance_id": "inst-1"},
                links=(),
            )
    report = evaluate_trace_contract(spans, run_id="run-1")
    assert report["verdict"] == VERDICT_FAIL
    assert any("rag.query_hash" in problem for problem in report["problems"])


# ---------------------------------------------------------------------------
# Secret leakage (§9.2: never record raw keys/tokens/DSN/full prompts)
# ---------------------------------------------------------------------------


# Assembled at runtime rather than written as a literal.  A key-shaped literal
# here — even a synthetic one — trips the repository's own pre-commit key gate,
# and a gate that cries wolf at its own test fixtures trains people to bypass
# it.  The prefix deliberately shares nothing with any real key.
_FAKE_OPENAI_KEY = "sk-" + ("dead" * 8)


@pytest.mark.parametrize(
    "value",
    [
        _FAKE_OPENAI_KEY,
        "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
        "mysql://root:hunter2@127.0.0.1:3306/app",
        "AKIAIOSFODNN7EXAMPLE",
    ],
)
def test_secret_looking_attribute_values_fail(value: str) -> None:
    spans = _full_capture()
    spans[0].attributes["some.attribute"] = value
    report = evaluate_trace_contract(spans, run_id="run-1")
    assert report["verdict"] == VERDICT_FAIL
    assert any("secret" in problem.lower() for problem in report["problems"])


def test_clean_attributes_do_not_trip_the_secret_detector() -> None:
    """False positives would push honest runs to FAIL and create pressure to
    disable the check, so the detector must tolerate normal pins."""
    spans = _full_capture()
    spans[0].attributes.update(
        {
            "git.commit": "3b0569f2aa11bc4d5e6f7a8b9c0d1e2f3a4b5c6d",
            "eval.model": "deepseek-v4-pro",
            "rag.index_name": "knowledge_base_v2_bge_m3",
        }
    )
    report = evaluate_trace_contract(spans, run_id="run-1")
    assert report["verdict"] == VERDICT_PASS, report["problems"]


# ---------------------------------------------------------------------------
# Error / timeout / cancel spans must still be reported (§20.6.4 item 2)
# ---------------------------------------------------------------------------


def test_error_spans_are_counted_not_dropped() -> None:
    """§20.1 rule 7: failures stay in the denominator.  An ERROR span is
    evidence and must appear in the report, not be filtered out."""
    spans = _full_capture()
    spans[1] = CapturedSpan(
        name=SPAN_EVAL_INSTANCE,
        trace_id="t1",
        span_id="s1",
        parent_span_id="s0",
        status="ERROR",
        attributes={"eval.run_id": "run-1", "eval.instance_id": "inst-1"},
        links=(),
    )
    report = evaluate_trace_contract(spans, run_id="run-1")
    assert report["error_span_count"] == 1
    assert report["span_count"] == len(spans)


# ---------------------------------------------------------------------------
# O3 strict parent-chain profile
# ---------------------------------------------------------------------------


def _o3_capture() -> list[CapturedSpan]:
    """The one permitted O3 topology, using real remote-parent identity."""
    shared = {"git.commit": "abc1234"}
    rag = {
        "rag.query_hash": "qh",
        "rag.corpus_generation": "techdocs-2026-07-30-v1",
        "rag.index_name": "knowledge_base_v2_bge_m3",
        "rag.retrieval_mode": "hybrid",
        "rag.reranker_applied": True,
        "rag.degraded": False,
        "rag.reranker_timeout": False,
    }
    return [
        _span(SPAN_EVAL_RUN, span_id="run", attributes=shared),
        _span(SPAN_EVAL_INSTANCE, span_id="instance", parent_span_id="run"),
        _span("invoke_agent", span_id="agent", parent_span_id="instance"),
        _span("chat", span_id="chat", parent_span_id="agent"),
        _span("execute_tool SearchKnowledge", span_id="tool", parent_span_id="agent"),
        _span("rag.retrieve", span_id="retrieve", parent_span_id="tool", attributes=rag),
        _span("embedding", span_id="embedding", parent_span_id="retrieve"),
        _span("rerank", span_id="rerank", parent_span_id="retrieve"),
        _span(SPAN_SCORER_OFFICIAL, span_id="scorer", parent_span_id="instance"),
    ]


def test_o3_profile_requires_full_real_parent_chain() -> None:
    report = evaluate_trace_contract(
        _o3_capture(), run_id="run-1", capabilities=("rag", "rerank"), profile="o3"
    )
    assert report["verdict"] == VERDICT_PASS, report["problems"]


def test_o3_profile_rejects_retrieve_not_parented_by_search_knowledge_tool() -> None:
    spans = _o3_capture()
    spans[5] = _span(
        "rag.retrieve",
        span_id="retrieve",
        parent_span_id="agent",
        attributes=dict(spans[5].attributes),
    )
    report = evaluate_trace_contract(
        spans, run_id="run-1", capabilities=("rag", "rerank"), profile="o3"
    )
    assert report["verdict"] == VERDICT_FAIL
    assert any("SearchKnowledge" in problem for problem in report["problems"])


def test_o3_profile_rejects_cycle_or_detached_subtree() -> None:
    spans = _o3_capture()
    spans[2] = _span("invoke_agent", span_id="agent", parent_span_id="chat")
    spans[3] = _span("chat", span_id="chat", parent_span_id="agent")
    report = evaluate_trace_contract(
        spans, run_id="run-1", capabilities=("rag", "rerank"), profile="o3"
    )
    assert report["verdict"] == VERDICT_FAIL
    assert any("cycle" in problem or "detached" in problem for problem in report["problems"])


def test_o3_profile_rejects_raw_tool_or_document_attributes() -> None:
    spans = _o3_capture()
    spans[4].attributes["gen_ai.tool.call.arguments"] = "harmless but prohibited"
    report = evaluate_trace_contract(
        spans, run_id="run-1", capabilities=("rag", "rerank"), profile="o3"
    )
    assert report["verdict"] == VERDICT_FAIL
    assert any("prohibited sensitive attribute" in problem for problem in report["problems"])
