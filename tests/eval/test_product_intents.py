"""Pure public-contract fixtures; no real native, provider, network or193 claim."""
import hashlib
import json
from dataclasses import replace

import pytest

from eval.harness import trace_contract as C


@pytest.fixture
def capture():
    span = C.CapturedSpan
    spans = [
        span("agent.main", "a" * 32, "1" * 16, attributes={"session.id": "session-1", "session.run_id": "run:1"}),
        span("chat", "a" * 32, "2" * 16, "1" * 16),
        span("execute_tool Read", "a" * 32, "3" * 16, "1" * 16, attributes={"gen_ai.tool.call.id": "tool-1", "gen_ai.tool.name": "Read"}),
        span("provider.call", "a" * 32, "4" * 16, "2" * 16, attributes={"model.call_id": "paid", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}),
        span("provider.attempt", "a" * 32, "5" * 16, "4" * 16, attributes={"model.call_id": "paid", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}),
        span("provider.call", "a" * 32, "6" * 16, "2" * 16, status="ERROR", attributes={"model.call_id": "unadmitted-intent", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}),
    ]
    expected = {"session_id": "session-1", "run_id": "run:1", "run_ids": ["run:1"], "completed": True,
        "model": "fixture", "model_calls": {"paid": "foreground"}, "retry_of": {},
        "tool_calls_by_run": {"run:1": {"tool-1": "Read"}}, "run_outcomes": {"run:1": "completed"},
        "attempts_by_run": {"run:1": 1}, "run_attempts_valid": True,
        "tool_facts_by_run": {"run:1": {"tool-1": [
            {"type": "tool/call", "seq": 1, "tool_name": "Read", "arguments_sha256": "f" * 64},
            {"type": "tool/dispatched", "seq": 2, "tool_name": "Read", "arguments_sha256": "f" * 64},
            {"type": "tool/result", "seq": 3, "tool_name": "Read", "exit_code": 0}]}}}
    # Direct contract fixture only; raw-page observation is exercised separately.
    spans[-1].intent_observation = {"schema_version": 1, "complete": True,
        "transport_attribute_present": False, "raw_links_present": False}
    authority = getattr(C, "ProductIntentAuthority", None)
    expected["intent_authority"] = authority(
        session_id="session-1", run_ids=("run:1",), model="fixture",
        admitted_calls=(("paid", "foreground", ""),),
        global_reserved_call_ids=frozenset({"paid", "old-other-owner"}),
        contained_runs=frozenset({"run:1"}),
        terminal_roots=(("run:1", "a" * 32, "1" * 16),),
        source_bindings=(("run:1", "segment-new", "e" * 40),),
        evidence_sha256=(("join", "a" * 64), ("inputs", "b" * 64), ("manifest", "c" * 64)),
        captured_spans=frozenset((span.trace_id, span.span_id,
            hashlib.sha256(json.dumps(span.to_dict(), sort_keys=True, separators=(",", ":")).encode()).hexdigest()) for span in spans),
    ) if authority else None
    return spans, expected


def test_v3_keeps_source_qualified_unadmitted_intent_in_complete_denominator(capture):
    spans, expected = capture
    report = C.evaluate_product_trace(spans, expected, contract_version="product-integrity-v3")
    assert report["verdict"] == "PASS", report
    assert report["span_count"] == 6 and len(report["error_spans"]) == 1
    counts = report["model_intents"]
    assert (counts["observed"], counts["admitted_logical"], counts["physical_attempts"], counts["unadmitted_failed"]) == (2, 1, 1, 1)
    intent = counts["unadmitted"][0]
    assert intent["call_id"] == "unadmitted-intent" and intent["run_id"] == "run:1"
    assert intent["exact_failure_stage"] == intent["billing"] == "unknown"


def test_old_versions_keep_the_same_extra_intent_failure(capture):
    spans, expected = capture
    assert C.evaluate_product_trace(spans, expected)["verdict"] == "FAIL"
    assert C.evaluate_product_trace(spans, expected, contract_version="product-integrity-v2")["verdict"] == "FAIL"


@pytest.mark.parametrize("case", ["missing-authority", "bare-true", "wrong-native-calls", "global-old-other-owner",
    "old-run-source-unknown", "wrong-source-binding", "missing-root-proof", "usage-zero", "http-zero",
    "outcome-empty", "retry-empty", "unfinished", "ok", "canceled", "unknown-status", "wrong-model",
    "wrong-purpose", "wrong-explicit-run", "wrong-parent", "cycle", "duplicate-span", "duplicate-call",
    "descendant", "extra-attempt", "native-reserved-missing-attempt", "empty-id", "suffix", "linked", "secret"])
def test_v3_refuses_each_unproven_intent(capture, case):
    spans, expected = capture
    extra = spans[-1]
    proof = expected["intent_authority"]
    if case == "missing-authority": expected.pop("intent_authority")
    elif case == "bare-true": expected["intent_authority"] = True
    elif case == "wrong-native-calls": expected["intent_authority"] = replace(proof, admitted_calls=())
    elif case == "global-old-other-owner": extra.attributes["model.call_id"] = "old-other-owner"
    elif case == "old-run-source-unknown": expected["intent_authority"] = replace(proof, contained_runs=frozenset(), source_bindings=())
    elif case == "wrong-source-binding": expected["intent_authority"] = replace(proof, source_bindings=(("other", "segment", "e" * 40),))
    elif case == "missing-root-proof": expected["intent_authority"] = replace(proof, terminal_roots=())
    elif case == "usage-zero": extra.attributes["gen_ai.usage.input_tokens"] = 0
    elif case == "http-zero": extra.attributes["http.response.status_code"] = 0
    elif case == "outcome-empty": extra.attributes["model.outcome"] = ""
    elif case == "retry-empty": extra.attributes["model.retry_of"] = ""
    elif case == "unfinished": extra.ended = False
    elif case == "ok": extra.status = "OK"
    elif case == "canceled": extra.status = "CANCELED"
    elif case == "unknown-status": extra.status = "UNKNOWN"
    elif case == "wrong-model": extra.attributes["gen_ai.request.model"] = "other"
    elif case == "wrong-purpose": extra.attributes["model.purpose"] = "other"
    elif case == "wrong-explicit-run": extra.attributes["session.run_id"] = "run:other"
    elif case == "wrong-parent": extra.parent_span_id = "f" * 16
    elif case == "cycle": extra.parent_span_id = extra.span_id
    elif case == "duplicate-span": spans.append(extra)
    elif case == "duplicate-call": spans.append(C.CapturedSpan(**(extra.to_dict() | {"span_id": "7" * 16, "links": ()})))
    elif case == "descendant": spans.append(C.CapturedSpan("transport child", "a" * 32, "7" * 16, extra.span_id))
    elif case == "extra-attempt": spans.append(C.CapturedSpan("provider.attempt", "a" * 32, "7" * 16, extra.span_id, attributes=dict(extra.attributes)))
    elif case == "native-reserved-missing-attempt": extra.attributes["model.call_id"] = "paid"
    elif case == "empty-id": extra.attributes["model.call_id"] = ""
    elif case == "suffix": extra.name = "provider.call unsupported-suffix"
    elif case == "linked": extra.links = ("b" * 32,)
    elif case == "secret": extra.attributes["unsafe"] = "sk-" + "fixture" * 5
    report = C.evaluate_product_trace(spans, expected, contract_version="product-integrity-v3")
    assert report["verdict"] != "PASS", report
    assert report["span_count"] == len(spans)


@pytest.mark.parametrize("case", ["admitted-failure", "retry", "canceled-stopped", "canceled-unknown"])
def test_v3_retains_existing_admitted_and_execution_outcomes(capture, case):
    spans, expected = capture
    spans.pop()
    spans[-1].status = "ERROR"
    if case == "retry":
        expected["model_calls"]["retry"] = "foreground"
        expected["retry_of"]["retry"] = "paid"
        spans.append(C.CapturedSpan("provider.attempt", "a" * 32, "7" * 16, "4" * 16,
                     attributes={"model.call_id": "retry", "model.retry_of": "paid", "model.purpose": "foreground", "gen_ai.request.model": "fixture"}))
    if case.startswith("canceled"):
        expected["run_outcomes"]["run:1"] = "canceled"
        expected["cancellation_states"] = {"run:1": "stopped" if case.endswith("stopped") else "unknown"}
    report = C.evaluate_product_trace(spans, expected, contract_version="product-integrity-v3")
    assert report["verdict"] == ("FAIL" if case == "canceled-unknown" else "PASS"), report
    assert report["model_intents"]["unadmitted_failed"] == 0
    assert report["model_intents"]["physical_attempts"] == len(expected["model_calls"])
    assert report["error_spans"] and report["span_count"] == len(spans)
