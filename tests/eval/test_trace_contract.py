"""O1: the unified, versioned trace acceptance contract.

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

@pytest.fixture
def product_trace_capture():
    from eval.harness.trace_contract import evaluate_product_trace

    root = CapturedSpan("agent.main", "a" * 32, "1" * 16, attributes={"session.id": "session-1", "session.run_id": "run:1"})
    spans = [root, CapturedSpan("chat", root.trace_id, "2" * 16, root.span_id),
             CapturedSpan("execute_tool Read", root.trace_id, "3" * 16, root.span_id, attributes={"gen_ai.tool.call.id": "tool-1", "gen_ai.tool.name": "Read"}),
             CapturedSpan("provider.call", root.trace_id, "4" * 16, "2" * 16, attributes={"model.call_id": "model-1", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}),
             CapturedSpan("provider.call", root.trace_id, "5" * 16, root.span_id, attributes={"model.call_id": "model-2", "model.purpose": "memory_reflection", "gen_ai.request.model": "fixture"}),
             CapturedSpan("provider.attempt", root.trace_id, "6" * 16, "4" * 16, attributes={"model.call_id": "model-1", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}),
             CapturedSpan("provider.attempt", root.trace_id, "7" * 16, "5" * 16, attributes={"model.call_id": "model-2", "model.purpose": "memory_reflection", "gen_ai.request.model": "fixture"})]
    expected = {"session_id": "session-1", "run_id": "run:1", "completed": True,
                "tool_calls": {"tool-1": "Read"}, "model_calls": {"model-1": "foreground", "model-2": "memory_reflection"}, "model": "fixture"}
    return spans, expected


def test_product_trace_matches_the_durable_run_and_every_expected_call(product_trace_capture):
    from eval.harness.trace_contract import evaluate_product_trace

    spans, expected = product_trace_capture
    report = evaluate_product_trace(spans, expected)
    assert report["verdict"] == "PASS"
    assert report["primary_trace_id"] == "a" * 32
    assert report["span_count"] == 7


@pytest.fixture
def product_task_trace_capture(product_trace_capture):
    spans, expected = product_trace_capture
    expected["run_ids"] = ["run:1", "run:2"]
    expected["tool_calls_by_run"] = {"run:1": expected.pop("tool_calls"), "run:2": {}}
    expected["model_calls"]["model-3"] = "foreground"
    spans.extend([
        CapturedSpan("agent.main", "b" * 32, "8" * 16, attributes={"session.id": "session-1", "session.run_id": "run:2"}),
        CapturedSpan("chat", "b" * 32, "9" * 16, "8" * 16),
        CapturedSpan("provider.call", "b" * 32, "a" * 16, "9" * 16, attributes={"model.call_id": "model-3", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}),
        CapturedSpan("provider.attempt", "b" * 32, "b" * 16, "a" * 16, attributes={"model.call_id": "model-3", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}),
    ])
    return spans, expected


def test_product_task_trace_covers_a_readonly_round_and_a_coding_round(product_task_trace_capture):
    from eval.harness.trace_contract import evaluate_product_trace

    spans, expected = product_task_trace_capture
    report = evaluate_product_trace(spans, expected)
    assert report["verdict"] == "PASS"
    assert report["run_ids"] == ["run:1", "run:2"]
    assert report["trace_ids"] == ["a" * 32, "b" * 32]
    assert report["span_count"] == 11 and report["expected_model_calls"] == 3


def test_product_task_trace_rejects_a_tool_attached_to_another_round(product_task_trace_capture):
    from eval.harness.trace_contract import evaluate_product_trace

    spans, expected = product_task_trace_capture
    spans[2].trace_id = "b" * 32
    spans[2].parent_span_id = "8" * 16
    assert evaluate_product_trace(spans, expected)["verdict"] == "FAIL"


@pytest.mark.parametrize("parent", ["f" * 16, "1" * 16, "8" * 16])
def test_product_task_trace_rejects_a_go_root_with_a_parent(product_task_trace_capture, parent):
    from eval.harness.trace_contract import evaluate_product_trace

    spans, expected = product_task_trace_capture
    spans[7].parent_span_id = parent
    assert evaluate_product_trace(spans, expected)["verdict"] == "FAIL"


def test_product_task_trace_keeps_reused_tool_ids_in_different_runs(product_task_trace_capture):
    from eval.harness.trace_contract import evaluate_product_trace

    spans, expected = product_task_trace_capture
    expected["tool_calls_by_run"] = {"run:1": {"tool-1": "Read"}, "run:2": {"tool-1": "Read"}}
    spans.append(CapturedSpan("execute_tool Read", "b" * 32, "c" * 16, "8" * 16, attributes={"gen_ai.tool.call.id": "tool-1", "gen_ai.tool.name": "Read"}))
    report = evaluate_product_trace(spans, expected)
    assert report["verdict"] == "PASS" and report["expected_tools"] == 2


@pytest.mark.parametrize("case,verdict", [("missing-tool", "INCOMPLETE"), ("missing-reflection", "INCOMPLETE"),
    ("unfinished", "INCOMPLETE"), ("error", "FAIL"), ("canceled-run", "FAIL"), ("wrong-run", "FAIL"),
    ("detached", "FAIL"), ("cycle", "FAIL"), ("duplicate-call", "FAIL"), ("secret", "FAIL"),
    ("zero-trace", "FAIL"), ("zero-span", "FAIL"), ("wrong-model", "FAIL"), ("chat-sibling", "FAIL")])
def test_product_trace_never_promotes_partial_or_invalid_evidence(product_trace_capture, case, verdict):
    from eval.harness.trace_contract import evaluate_product_trace

    spans, expected = product_trace_capture
    if case == "missing-tool": spans.pop(2)
    elif case == "missing-reflection": spans.pop()
    elif case == "unfinished": spans[-1].ended = False
    elif case == "error": spans[-1].status = "ERROR"
    elif case == "canceled-run": expected["completed"] = False
    elif case == "wrong-run": spans[0].attributes["session.run_id"] = "run:other"
    elif case == "detached": spans[-1].parent_span_id = "f" * 16
    elif case == "cycle": spans[-1].parent_span_id = spans[-1].span_id
    elif case == "duplicate-call": spans.append(CapturedSpan("provider.call", "a" * 32, "6" * 16, "1" * 16, attributes=dict(spans[-1].attributes)))
    elif case == "secret": spans[-1].attributes["unsafe"] = "sk-" + "fixture" * 5
    elif case == "zero-trace":
        for span in spans: span.trace_id = "0" * 32
    elif case == "zero-span": spans[-1].span_id = "0" * 16
    elif case == "wrong-model": spans[-1].attributes["gen_ai.request.model"] = "another-model"
    elif case == "chat-sibling": spans[3].parent_span_id = spans[0].span_id
    assert evaluate_product_trace(spans, expected)["verdict"] == verdict


def test_product_trace_keeps_every_attempt_in_a_settled_retry(product_trace_capture):
    from eval.harness.trace_contract import evaluate_product_trace

    spans, expected = product_trace_capture
    expected["model_calls"]["model-retry"] = "foreground"
    expected["retry_of"] = {"model-retry": "model-1"}
    spans[5].status = "ERROR"
    spans.append(CapturedSpan("provider.attempt", "a" * 32, "8" * 16, "4" * 16, attributes={
        "model.call_id": "model-retry", "model.retry_of": "model-1", "model.purpose": "foreground", "gen_ai.request.model": "fixture",
    }))
    report = evaluate_product_trace(spans, expected)
    assert report["expected_logical_calls"] == 2 and report["expected_model_calls"] == 3
    assert report["missing"] == [] and report["verdict"] == "FAIL"
    assert report["problems"] == ["product tree includes an error or canceled span"]


@pytest.fixture
def product_integrity_capture(product_trace_capture):
    spans, expected = product_trace_capture
    expected.update(run_outcomes={"run:1": "completed"}, attempts_by_run={"run:1": 1},
                    tool_facts_by_run={"run:1": {"tool-1": [
                        {"type": "tool/call", "seq": 1, "tool_name": "Read", "arguments_sha256": "f" * 64},
                        {"type": "tool/dispatched", "seq": 2, "tool_name": "Read", "arguments_sha256": "f" * 64},
                        {"type": "tool/result", "seq": 3, "tool_name": "Read", "exit_code": 0},
                    ]}})
    return spans, expected


@pytest.mark.parametrize("case", ["known-error", "failed-run", "canceled-run", "recovered-run", "denied-tool", "settled-retry"])
def test_product_integrity_keeps_execution_outcomes_separate(product_integrity_capture, case):
    from eval.harness.trace_contract import evaluate_product_trace

    spans, expected = product_integrity_capture
    if case == "known-error":
        spans[2].status = "ERROR"
        expected["tool_facts_by_run"]["run:1"]["tool-1"][-1].update(exit_code=1, has_error=True)
    elif case in {"failed-run", "canceled-run"}:
        expected["completed"] = False
        expected["run_outcomes"]["run:1"] = "failed" if case == "failed-run" else "canceled"
        if case == "canceled-run":
            expected["cancellation_states"] = {"run:1": "stopped"}
        spans[0].status = "ERROR"
    elif case == "recovered-run":
        expected["attempts_by_run"]["run:1"] = 2
        spans.append(CapturedSpan("agent.main", "a" * 32, "8" * 16, "1" * 16, attributes=dict(spans[0].attributes)))
        spans[0].status = "ERROR"
    elif case == "denied-tool":
        expected["tool_calls"]["tool-denied"] = "WebFetch"
        expected["tool_facts_by_run"]["run:1"]["tool-denied"] = [
            {"type": "tool/call", "seq": 4, "tool_name": "WebFetch", "arguments_sha256": "f" * 64},
            {"type": "approval/denied", "seq": 5, "tool_name": "WebFetch", "decision": "denied", "arguments_sha256": "f" * 64},
            {"type": "tool/result", "seq": 6, "tool_name": "WebFetch", "exit_code": 1, "has_error": True},
        ]
    else:
        expected["model_calls"]["model-retry"] = "foreground"
        expected["retry_of"] = {"model-retry": "model-1"}
        spans[5].status = "ERROR"
        spans.append(CapturedSpan("provider.attempt", "a" * 32, "8" * 16, "4" * 16, attributes={
            "model.call_id": "model-retry", "model.retry_of": "model-1", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}))
    report = evaluate_product_trace(spans, expected, contract_version="product-integrity-v2")
    assert report["contract_version"] == "product-integrity-v2" and report["verdict"] == "PASS", report
    assert report["span_count"] == len(spans) and report["missing"] == []
    assert report["execution"]["runs"] == expected["run_outcomes"]
    assert report["error_spans"] == [
        {"trace_id": s.trace_id, "span_id": s.span_id, "name": s.name, "status": s.status}
        for s in spans if s.status not in {"OK", "UNSET"}]
    if case == "denied-tool":
        assert report["expected_tools"] == 2 and report["expected_dispatched_tools"] == 1
        assert report["execution"]["tools"][-1]["state"] == "denied"
    assert evaluate_product_trace(spans, expected)["verdict"] != "PASS"


@pytest.mark.parametrize("case", ["missing-tool", "missing-model", "unfinished", "wrong-run", "wrong-model", "detached", "cycle",
    "duplicate-span", "duplicate-call", "unknown-status", "missing-attempt", "extra-root", "unknown-result", "missing-result",
    "undispatched", "denied-dispatched", "denied-executed", "runtime-still-running", "wrong-arguments", "unknown-run", "wrong-tool-fact", "foreign-parent", "cross-run-root", "cancellation-unknown", "cancellation-missing", "provider-suffix-model", "provider-suffix-parent", "secret"])
def test_product_integrity_still_refuses_unproven_evidence(product_integrity_capture, case):
    from eval.harness.trace_contract import evaluate_product_trace

    spans, expected = product_integrity_capture
    facts = expected["tool_facts_by_run"]["run:1"]["tool-1"]
    if case == "missing-tool": spans.pop(2)
    elif case == "missing-model": spans.pop()
    elif case == "unfinished": spans[-1].ended = False
    elif case == "wrong-run": spans[0].attributes["session.run_id"] = "run:other"
    elif case == "wrong-model": spans[-1].attributes["gen_ai.request.model"] = "other"
    elif case == "detached": spans[-1].parent_span_id = "f" * 16
    elif case == "cycle": spans[1].parent_span_id = spans[3].span_id
    elif case == "duplicate-span": spans.append(spans[-1])
    elif case == "duplicate-call": spans.append(CapturedSpan("execute_tool Read", "a" * 32, "8" * 16, "1" * 16, attributes=dict(spans[2].attributes)))
    elif case == "unknown-status": spans[-1].status = "UNKNOWN"
    elif case == "missing-attempt": expected["attempts_by_run"]["run:1"] = 2
    elif case == "extra-root": spans.append(CapturedSpan("agent.main", "a" * 32, "8" * 16, attributes=dict(spans[0].attributes)))
    elif case == "unknown-result": facts[-1]["outcome_unknown"] = True
    elif case == "missing-result": facts.pop()
    elif case == "undispatched": facts.pop(1)
    elif case == "denied-dispatched": facts.insert(1, {"type": "approval/denied", "seq": 1, "tool_name": "Read", "decision": "denied"})
    elif case == "denied-executed":
        facts[1].update(type="approval/denied", decision="denied")
        facts[-1].update(exit_code=1, has_error=True)
    elif case == "runtime-still-running": facts[-1].update(runtime_started=True, stopped=False)
    elif case == "wrong-arguments": facts[1]["arguments_sha256"] = "a" * 64
    elif case == "unknown-run": expected["run_outcomes"]["run:1"] = "unknown"
    elif case == "wrong-tool-fact": facts[-1]["tool_name"] = "Write"
    elif case == "foreign-parent": spans[0].parent_span_id = "f" * 16
    elif case == "cross-run-root":
        expected["run_ids"] = ["run:1", "run:2"]
        expected["attempts_by_run"]["run:2"] = 1
        spans.append(CapturedSpan("agent.main", "a" * 32, "8" * 16, "1" * 16, attributes={"session.id": "session-1", "session.run_id": "run:2"}))
    elif case.startswith("cancellation-"):
        expected["completed"] = False
        expected["run_outcomes"]["run:1"] = "canceled"
        if case.endswith("unknown"):
            expected["cancellation_states"] = {"run:1": "unknown"}
    elif case == "provider-suffix-model":
        spans[-1].name = "provider.attempt suffix"
        spans[-1].attributes["gen_ai.request.model"] = "other"
    elif case == "provider-suffix-parent":
        spans[-1].name = "provider.attempt suffix"
        spans[-1].parent_span_id = spans[0].span_id
    elif case == "secret": spans[-1].attributes["unsafe"] = "sk-" + "fixture" * 5
    report = evaluate_product_trace(spans, expected, contract_version="product-integrity-v2")
    assert report["verdict"] != "PASS", report


def test_product_integrity_joins_separate_run_roots_in_the_same_actual_trace(product_task_trace_capture):
    from eval.harness.trace_contract import evaluate_product_trace

    spans, expected = product_task_trace_capture
    for span in spans:
        span.trace_id = "a" * 32
    expected.update(run_outcomes={"run:1": "completed", "run:2": "completed"}, attempts_by_run={"run:1": 1, "run:2": 1},
                    tool_facts_by_run={"run:1": {"tool-1": [
                        {"type": "tool/call", "seq": 1, "tool_name": "Read", "arguments_sha256": "f" * 64},
                        {"type": "tool/dispatched", "seq": 2, "tool_name": "Read", "arguments_sha256": "f" * 64},
                        {"type": "tool/result", "seq": 3, "tool_name": "Read", "exit_code": 0},
                    ]}, "run:2": {}})
    report = evaluate_product_trace(spans, expected, contract_version="product-integrity-v2")
    assert report["verdict"] == "PASS" and report["span_count"] == 11, report


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


def test_schema_covers_every_required_evaluation_span_kind() -> None:
    """The evaluation contract enumerates every span kind it must cover."""
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


def test_join_attributes_match_evaluation_contract() -> None:
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


def test_default_profile_rejects_error_spans() -> None:
    """A complete capture with an error is not successful trace evidence."""
    spans = _full_capture()
    spans[2] = CapturedSpan(
        name=spans[2].name,
        trace_id=spans[2].trace_id,
        span_id=spans[2].span_id,
        parent_span_id=spans[2].parent_span_id,
        status="ERROR",
        attributes=dict(spans[2].attributes),
        links=spans[2].links,
    )

    report = evaluate_trace_contract(spans, run_id="run-1")

    assert report["verdict"] == VERDICT_FAIL
    assert any("ERROR" in problem for problem in report["problems"])


def test_default_profile_rejects_unfinished_spans() -> None:
    """A capture is not complete while one of its recorded spans is open."""
    spans = _full_capture()
    spans[2] = CapturedSpan(
        name=spans[2].name,
        trace_id=spans[2].trace_id,
        span_id=spans[2].span_id,
        parent_span_id=spans[2].parent_span_id,
        status=spans[2].status,
        attributes=dict(spans[2].attributes),
        links=spans[2].links,
        ended=False,
    )

    report = evaluate_trace_contract(spans, run_id="run-1")

    assert report["verdict"] == VERDICT_FAIL
    assert any("not ended" in problem for problem in report["problems"])


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
