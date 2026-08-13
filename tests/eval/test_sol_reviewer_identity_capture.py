"""E2 gate: ``review_one`` must actually READ the provider identity.

``tests/eval/test_model_identity_capture.py`` pins the transport layer
(:class:`ChatResponse.model_identity` populated from the raw response body) and
``tests/eval/test_sol_reviewer_identity.py`` pins the artifact layer
(``reviewer_reported_model`` etc. reaching sidecar/arbitrated/summary rows).

Between the two sits the wire that makes them one system: :func:`review_one`
must copy ``response.model_identity`` onto the :class:`PassVerdict` it returns.
Without this file both halves can pass while the captured identity is silently
dropped on the floor and every artifact reports a blank, permanently
``MODEL_IDENTITY_UNVERIFIED`` identity regardless of what the provider said —
an end-to-end gap that looks exactly like success at each individual layer.

Offline only: an in-memory fake client stands in for the provider, so no key,
no network and no spend are involved.
"""

from __future__ import annotations

import asyncio
import ast
import json
import sys
from pathlib import Path
from typing import Any

import pytest

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from orchestrator.eval.sol_reviewer import (  # noqa: E402
    IDENTITY_UNVERIFIED,
    IDENTITY_VERIFIED,
    EvidenceChunk,
    resolve_identity_status,
    review_one,
)

_SAMPLE_QREL = {
    "query_id": "go-q001",
    "document_id": "go@abc:doc/go_spec.html",
    "section_path": ["Types"],
    "relevance": 1.0,
    "source_id": "go",
    "language": "en",
    "query_type": "concept",
}

_VALID_VERDICT = {
    "answerable": True,
    "language_correct": True,
    "query_type_correct": True,
    "relevance_correct": True,
    "section_correct": True,
    "evidence_sufficient": True,
    "contamination_risk": "none",
    "confidence": 0.85,
}

_VERIFIED_IDENTITY = {
    "requested_model": "gpt-5.6-sol",
    "reported_model": "gpt-5.6-sol-2026-07",
    "response_id": "chatcmpl-abc123",
    "system_fingerprint": "fp_deadbeef01",
    "created": 1786000000,
    "identity_verified": True,
}

_SILENT_IDENTITY = {
    "requested_model": "gpt-5.6-sol",
    "reported_model": "",
    "response_id": "",
    "system_fingerprint": "",
    "created": 0,
    "identity_verified": False,
}


class _FakeResponse:
    """Minimal ChatResponse stand-in carrying a provider identity."""

    def __init__(self, text: str, model_identity: dict[str, Any] | None = None):
        self.text = text
        self.tool_calls: list[Any] = []
        self.thinking_blocks: list[Any] = []
        self.usage = None
        self.model_identity = dict(model_identity or {})


class _FakeClient:
    def __init__(self, response: Any):
        self._response = response
        self.model = "gpt-5.6-sol"
        self.base_url = "https://api.openai.com/v1"

    async def chat(self, request):  # noqa: ANN001
        if isinstance(self._response, Exception):
            raise self._response
        return self._response


def _evidence() -> list[EvidenceChunk]:
    return [
        EvidenceChunk(
            document_id="go@abc:doc/go_spec.html",
            section_path=["Types"],
            source_path="doc/go_spec.html",
            text="Interface types describe method sets.",
        )
    ]


def _review(response: Any) -> Any:
    return asyncio.run(
        review_one(
            _FakeClient(response),
            "A",
            "What is an interface type in Go?",
            _SAMPLE_QREL,
            _evidence(),
            "gpt-5.6-sol",
            "unknown",
        )
    )


class TestReviewOneCapturesIdentity:
    def test_successful_review_carries_provider_identity(self):
        verdict = _review(
            _FakeResponse(json.dumps(_VALID_VERDICT), _VERIFIED_IDENTITY)
        )
        assert verdict.failed is False
        assert verdict.model_identity.get("reported_model") == "gpt-5.6-sol-2026-07"
        assert verdict.model_identity.get("system_fingerprint") == "fp_deadbeef01"
        assert verdict.model_identity.get("endpoint_host") == "api.openai.com"
        assert resolve_identity_status([verdict.model_identity]) == IDENTITY_VERIFIED

    def test_silent_provider_yields_unverified_not_backfilled(self):
        verdict = _review(
            _FakeResponse(json.dumps(_VALID_VERDICT), _SILENT_IDENTITY)
        )
        assert verdict.failed is False
        # The requested name must NOT be laundered into the reported field.
        assert verdict.model_identity.get("reported_model") == ""
        assert resolve_identity_status([verdict.model_identity]) == IDENTITY_UNVERIFIED

    def test_parse_failure_still_carries_identity(self):
        """A malformed body still proves which model produced it."""
        verdict = _review(_FakeResponse("not json at all", _VERIFIED_IDENTITY))
        assert verdict.failed is True
        assert verdict.fail_reason == "parse_failed"
        assert verdict.model_identity.get("reported_model") == "gpt-5.6-sol-2026-07"

    def test_validate_failure_still_carries_identity(self):
        # A non-bool boolean field is what `validate_verdict_fields` actually
        # fails closed on (bad confidence is clamped, bad contamination_risk
        # falls back to "none" — neither marks the pass failed).
        bad = dict(_VALID_VERDICT, answerable="yes")
        verdict = _review(_FakeResponse(json.dumps(bad), _VERIFIED_IDENTITY))
        assert verdict.failed is True
        assert verdict.fail_reason == "bad_field:answerable"
        assert verdict.model_identity.get("reported_model") == "gpt-5.6-sol-2026-07"

    def test_transport_error_has_empty_identity(self):
        """No response body means no identity evidence — blank, never invented."""
        verdict = _review(RuntimeError("connection reset"))
        assert verdict.failed is True
        assert verdict.fail_reason is not None
        assert verdict.fail_reason.startswith("llm_error")
        assert verdict.model_identity == {}
        assert resolve_identity_status([verdict.model_identity]) == IDENTITY_UNVERIFIED

    def test_response_without_identity_attribute_is_tolerated(self):
        """Older/foreign client objects must not crash the review."""

        class _Legacy:
            text = json.dumps(_VALID_VERDICT)
            tool_calls: list[Any] = []
            thinking_blocks: list[Any] = []
            usage = None

        verdict = _review(_Legacy())
        assert verdict.failed is False
        assert verdict.model_identity == {"endpoint_host": "api.openai.com"}
        assert resolve_identity_status([verdict.model_identity]) == IDENTITY_UNVERIFIED

    def test_captured_identity_never_carries_credentials(self):
        verdict = _review(
            _FakeResponse(
                json.dumps(_VALID_VERDICT),
                dict(_VERIFIED_IDENTITY, api_key="sk-should-never-appear"),
            )
        )
        text = json.dumps(verdict.model_identity)
        assert "sk-should-never-appear" not in text
        assert "api_key" not in text


class TestReviewOneReadsResponseIdentity:
    def test_review_one_reads_model_identity_attribute(self):
        """AST guard: the capture must be a direct read of the response body.

        An indirect ``getattr`` chain would let the wire be quietly removed
        while every other test in this file still passed via a fixture.
        """
        source = (
            PROJECT_ROOT / "orchestrator" / "eval" / "sol_reviewer.py"
        ).read_text(encoding="utf-8")
        tree = ast.parse(source)
        target = None
        for node in ast.walk(tree):
            if isinstance(node, ast.AsyncFunctionDef) and node.name == "review_one":
                target = node
                break
        assert target is not None, "review_one not found"
        attrs = {
            n.attr
            for n in ast.walk(target)
            if isinstance(n, ast.Attribute)
        }
        assert "model_identity" in attrs, (
            "review_one never reads response.model_identity — the provider "
            "identity is captured at the transport layer and then dropped"
        )


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(pytest.main([__file__, "-q"]))
