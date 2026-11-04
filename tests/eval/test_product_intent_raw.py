"""Raw public Phoenix pages, real normalizer, public v3 contract and CLI."""
from dataclasses import replace
import hashlib
import json
from urllib.parse import parse_qs, urlsplit

import pytest
from eval.harness import trace_contract as C
from eval.harness import phoenix as P
from test_product_intents import capture
from test_product_intent_cli import cli_case


ACCOUNTING_ATTRIBUTES = {
    "llm-token-zero": {"llm": {"token_count": {"total": 0}}},
    "llm-token-false": {"llm": {"token_count": {"known": False}}},
    "llm-token-empty": {"llm": {"token_count": {}}},
    "flat-token-zero": {"llm.token_count.total": 0},
    "generic-usage-empty": {"other": {"usage": {}}},
    "generic-cost-false": {"cost": {"charged": False}},
    "generic-cost-empty": {"cost": {}},
    "generic-retry-empty": {"other": {"retry": ""}},
    "settlement-zero": {"settlement": {"total": 0}},
    "settlement-empty": {"settlement": {}},
    "billing-false": {"billing": {"confirmed": False}},
    "nested-array-usage": {"other": [[{"usage": {"total": 0}}]]},
    "nested-array-response": {"other": [{"gen_ai": {"response": {}}}]},
    "nested-array-outcome": {"other": [{"model.outcome": False}]},
}


def raw_page(spans, case):
    rows = [{"name": span.name, "context": {"trace_id": span.trace_id, "span_id": span.span_id},
        "parent_id": span.parent_span_id or None, "status_code": span.status,
        "end_time": "2026-10-15T00:00:01Z" if span.ended else None,
        "attributes": dict(span.attributes), "links": []} for span in spans]
    extra = rows[-1]
    if case == "usage-zero": extra["attributes"]["gen_ai.usage.total_tokens"] = 0
    elif case == "usage-false": extra["attributes"]["gen_ai.usage.provider_unknown"] = False
    elif case == "http-zero": extra["attributes"]["http.status_code"] = 0
    elif case == "http-false": extra["attributes"]["http.was_sent"] = False
    elif case == "response-zero": extra["attributes"]["gen_ai.response.any"] = 0
    elif case == "nested-http": extra["attributes"]["http"] = {"status_code": 0}
    elif case == "nested-usage": extra["attributes"]["gen_ai"] = {"usage": {"any": False}}
    elif case == "raw-links": extra["links"] = [{"context": {"trace_id": "b" * 32, "span_id": "7" * 16}}]
    elif case == "missing-links": extra.pop("links")
    elif case == "invalid-links": extra["links"] = None
    elif case in ACCOUNTING_ATTRIBUTES: extra["attributes"].update(ACCOUNTING_ATTRIBUTES[case])
    return rows


def public_read(monkeypatch, rows, session, run, *, retain=True):
    def request(url):
        query = parse_qs(urlsplit(url).query)
        return {"data": [row for row in rows if row["name"] == "agent.main"] if "name" in query else rows}
    monkeypatch.setattr(P, "_request_json", request)
    options = {"retain_intent_evidence": True} if retain else {}
    return P.read_product_spans("http://fixture-only", "fixture", "2026-10-15T00:00:00Z", session, run, **options)


def fingerprints(spans):
    return frozenset((span.trace_id, span.span_id,
        hashlib.sha256(json.dumps(span.to_dict(), sort_keys=True, separators=(",", ":")).encode()).hexdigest()) for span in spans)


@pytest.mark.parametrize("case", ["clean", "usage-zero", "usage-false", "http-zero", "http-false", "response-zero",
    "nested-http", "nested-usage", "raw-links", "missing-links", "invalid-links", *ACCOUNTING_ATTRIBUTES])
def test_raw_page_counterfacts_survive_real_normalizer_before_public_contract(capture, monkeypatch, case):
    spans, expected = capture
    normalized = public_read(monkeypatch, raw_page(spans, case), expected["session_id"], expected["run_id"])
    expected["intent_authority"] = replace(expected["intent_authority"], captured_spans=fingerprints(normalized))
    report = C.evaluate_product_trace(normalized, expected, contract_version="product-integrity-v3")
    assert report["verdict"] == ("PASS" if case == "clean" else "FAIL"), report
    assert report["model_intents"]["unadmitted_failed"] == (1 if case == "clean" else 0)
    # Only fixed presence/completeness flags may be added; raw links/unknown values are not artifacts.
    if case != "clean":
        assert normalized[-1].links == ()
    if case in ACCOUNTING_ATTRIBUTES:
        assert normalized[-1].intent_observation["transport_attribute_present"] is True


def test_legacy_normalized_capture_cannot_gain_missing_raw_observation(capture):
    spans, expected = capture
    spans[-1].intent_observation = None
    expected["intent_authority"] = replace(expected["intent_authority"], captured_spans=fingerprints(spans))
    report = C.evaluate_product_trace(spans, expected, contract_version="product-integrity-v3")
    assert report["verdict"] == "FAIL", report
    assert report["model_intents"]["unadmitted_failed"] == 0


@pytest.mark.parametrize("observation", [None, {}, {"schema_version": True, "complete": True, "transport_attribute_present": False, "raw_links_present": False},
    {"schema_version": 1, "complete": False, "transport_attribute_present": False, "raw_links_present": False},
    {"schema_version": 1, "complete": True, "transport_attribute_present": 0, "raw_links_present": False},
    {"schema_version": 1, "complete": True, "transport_attribute_present": False, "raw_links_present": "false"}])
def test_public_contract_refuses_unknown_or_malformed_observation(capture, observation):
    spans, expected = capture
    spans[-1].intent_observation = observation
    expected["intent_authority"] = replace(expected["intent_authority"], captured_spans=fingerprints(spans))
    report = C.evaluate_product_trace(spans, expected, contract_version="product-integrity-v3")
    assert report["verdict"] == "FAIL" and report["model_intents"]["unadmitted_failed"] == 0, report


@pytest.mark.parametrize("case", ["clean", "usage-zero", "http-zero", "raw-links", "missing-links", *ACCOUNTING_ATTRIBUTES])
def test_public_cli_reads_raw_pages_through_actual_normalizer(cli_case, monkeypatch, case):
    module, argv, root, pins, joined, save, state = cli_case
    rows = raw_page(state["spans"], case)
    run = state["capture"]["runs"][0]["run_id"]
    normalized = public_read(monkeypatch, rows, state["session"]["events"][0]["session_id"], run)
    artifact = state["capture_path"].parent / state["capture"]["runs"][0]["artifact"]
    document = json.loads(artifact.read_bytes())
    document["spans"] = [span.to_dict() for span in normalized]
    state["capture"]["runs"][0]["sha256"] = save(artifact, document)
    checksum = save(state["capture_path"], state["capture"])
    joined["input_sha256"][str(state["capture_path"])] = checksum
    pins["product_integrity_v3"]["capture_index"]["sha256"] = checksum
    pins["product_integrity_v3"]["native_join"]["sha256"] = save(state["join_path"], joined)
    save(state["analysis_path"], pins)
    monkeypatch.setattr(module, "read_product_spans", P.read_product_spans)
    assert module.main(argv) == (0 if case == "clean" else 3)
    receipt = json.loads((root / "output/receipt.json").read_bytes())
    assert receipt["report"]["verdict"] == ("PASS" if case == "clean" else "FAIL"), receipt
    if case in ACCOUNTING_ATTRIBUTES:
        assert receipt["status"] == "BLOCKED" and receipt["report"]["model_intents"]["unclassified"] == 1


@pytest.mark.parametrize("case", ["raw-links", *ACCOUNTING_ATTRIBUTES])
def test_default_reader_retains_legacy_projection_shape(capture, monkeypatch, case):
    spans, expected = capture
    normalized = public_read(monkeypatch, raw_page(spans, case), expected["session_id"], expected["run_id"], retain=False)
    assert normalized[-1].links == ()
    assert "intent_observation" not in normalized[-1].to_dict()
