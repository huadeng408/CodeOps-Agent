"""E2 gate: the Sol review artifact must carry provider-reported identity.

Design map §20.6.3 task E2.  §20.4 recorded the concrete dishonesty this gate
exists to prevent: ``reviewer_revision`` was written from
``args.revision or read_env("OPENAI_MODEL_REVISION") or "unknown"`` — a value we
chose ourselves on the CLI.  An input echoed back into an artifact is not
evidence of which model served the review.

``tests/eval/test_model_identity_capture.py`` already pins the transport layer
(:class:`ChatResponse.model_identity` populated from the raw response body).
This file pins the *artifact* layer: the captured identity must reach the
sidecar rows, the arbitrated rows and the summary, and the status must be
``MODEL_IDENTITY_UNVERIFIED`` whenever the provider gave no immutable revision.

Offline only — a fake in-memory client stands in for the provider, so no key,
no network and no spend are involved.
"""

from __future__ import annotations

import json
import sys
from pathlib import Path
from typing import Any

import pytest

PROJECT_ROOT = Path(__file__).resolve().parents[2]
if str(PROJECT_ROOT) not in sys.path:
    sys.path.insert(0, str(PROJECT_ROOT))

from orchestrator.eval.sol_reviewer import (  # noqa: E402
    PassVerdict,
    _reconstruct_verdicts,
    _row_to_verdict,
    _verdict_to_row,
    arbitrate,
    load_sidecar,
    resolve_identity_status,
    summarize,
    write_sidecar_row,
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

_VERIFIED_IDENTITY = {
    "endpoint_host": "api.openai.com",
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


def _make_verdict(
    *,
    qid: str = "go-q001",
    pass_id: str = "A",
    failed: bool = False,
    confidence: float = 0.9,
    model_identity: dict[str, Any] | None = None,
) -> PassVerdict:
    return PassVerdict(
        query_id=qid,
        pass_id=pass_id,
        failed=failed,
        fail_reason=None,
        answerable=True,
        language_correct=True,
        query_type_correct=True,
        relevance_correct=True,
        section_correct=True,
        evidence_sufficient=True,
        contamination_risk="none",
        confidence=confidence,
        prompt_hash="abcd1234deadbeef",
        evidence_refs=("es:t:doc:x",),
        model_identity=dict(model_identity or {}),
    )


class TestPassVerdictCarriesIdentity:
    def test_model_identity_field_exists_and_defaults_empty(self):
        v = _make_verdict()
        assert v.model_identity == {}

    def test_model_identity_round_trips(self):
        v = _make_verdict(model_identity=_VERIFIED_IDENTITY)
        assert v.model_identity["reported_model"] == "gpt-5.6-sol-2026-07"
        assert v.model_identity["system_fingerprint"] == "fp_deadbeef01"


class TestResolveIdentityStatus:
    def test_verified_when_provider_names_model_and_fingerprint(self):
        assert resolve_identity_status([_VERIFIED_IDENTITY]) == "MODEL_IDENTITY_VERIFIED"

    def test_unverified_when_provider_is_silent(self):
        assert resolve_identity_status([_SILENT_IDENTITY]) == "MODEL_IDENTITY_UNVERIFIED"

    def test_unverified_with_no_identities_at_all(self):
        assert resolve_identity_status([]) == "MODEL_IDENTITY_UNVERIFIED"

    def test_unverified_when_any_call_is_unverified(self):
        status = resolve_identity_status([_VERIFIED_IDENTITY, _SILENT_IDENTITY])
        assert status == "MODEL_IDENTITY_UNVERIFIED"

    def test_requested_model_alone_is_never_verification(self):
        """The §20.4 failure mode, pinned: an echoed request name proves nothing."""
        echoed = {
            "requested_model": "gpt-5.6-sol",
            "reported_model": "",
            "response_id": "",
            "system_fingerprint": "",
            "created": 0,
            "identity_verified": False,
        }
        assert resolve_identity_status([echoed]) == "MODEL_IDENTITY_UNVERIFIED"

    def test_reported_model_without_fingerprint_is_unverified(self):
        partial = dict(_VERIFIED_IDENTITY, system_fingerprint="", identity_verified=False)
        assert resolve_identity_status([partial]) == "MODEL_IDENTITY_UNVERIFIED"


class TestSidecarRowCarriesIdentity:
    def test_row_records_provider_reported_model(self):
        v = _make_verdict(model_identity=_VERIFIED_IDENTITY)
        row = _verdict_to_row(v, _SAMPLE_QREL, "gpt-5.6-sol", "unknown")
        assert row["reviewer_reported_model"] == "gpt-5.6-sol-2026-07"
        assert row["reviewer_system_fingerprint"] == "fp_deadbeef01"
        assert row["reviewer_response_id"] == "chatcmpl-abc123"
        assert row["reviewer_endpoint_host"] == "api.openai.com"

    def test_row_identity_status_verified(self):
        v = _make_verdict(model_identity=_VERIFIED_IDENTITY)
        row = _verdict_to_row(v, _SAMPLE_QREL, "gpt-5.6-sol", "unknown")
        assert row["reviewer_identity_status"] == "MODEL_IDENTITY_VERIFIED"

    def test_row_identity_status_unverified_when_silent(self):
        v = _make_verdict(model_identity=_SILENT_IDENTITY)
        row = _verdict_to_row(v, _SAMPLE_QREL, "gpt-5.6-sol", "unknown")
        assert row["reviewer_identity_status"] == "MODEL_IDENTITY_UNVERIFIED"
        assert row["reviewer_reported_model"] == ""

    def test_requested_revision_is_not_promoted_to_reported(self):
        """`--revision` must never be laundered into the reported-identity field."""
        v = _make_verdict(model_identity=_SILENT_IDENTITY)
        row = _verdict_to_row(v, _SAMPLE_QREL, "gpt-5.6-sol", "2026-07-claimed")
        assert row["reviewer_revision"] == "2026-07-claimed"
        assert row["reviewer_reported_model"] != "2026-07-claimed"
        assert row["reviewer_system_fingerprint"] == ""
        assert row["reviewer_identity_status"] == "MODEL_IDENTITY_UNVERIFIED"

    def test_row_identity_never_contains_credentials(self):
        v = _make_verdict(
            model_identity=dict(_VERIFIED_IDENTITY, api_key="sk-should-never-appear")
        )
        row = _verdict_to_row(v, _SAMPLE_QREL, "gpt-5.6-sol", "unknown")
        text = json.dumps(row)
        assert "sk-should-never-appear" not in text
        assert "api_key" not in text
        assert "authorization" not in text.lower()


class TestArbitratedRowsCarryIdentity:
    def test_arbitrated_row_reports_identity_status(self):
        a = _make_verdict(model_identity=_VERIFIED_IDENTITY)
        b = _make_verdict(pass_id="B", confidence=0.85, model_identity=_VERIFIED_IDENTITY)
        rows = arbitrate({"go-q001": a}, {"go-q001": b}, [_SAMPLE_QREL])
        assert rows[0]["review_status"] == "AI_REVIEWED"
        assert rows[0]["reviewer_identity_status"] == "MODEL_IDENTITY_VERIFIED"

    def test_one_silent_pass_makes_arbitrated_identity_unverified(self):
        a = _make_verdict(model_identity=_VERIFIED_IDENTITY)
        b = _make_verdict(pass_id="B", confidence=0.85, model_identity=_SILENT_IDENTITY)
        rows = arbitrate({"go-q001": a}, {"go-q001": b}, [_SAMPLE_QREL])
        assert rows[0]["reviewer_identity_status"] == "MODEL_IDENTITY_UNVERIFIED"

    def test_ai_reviewed_verdict_is_independent_of_identity(self):
        """Identity is reported alongside, not folded into, the review verdict."""
        a = _make_verdict(model_identity=_SILENT_IDENTITY)
        b = _make_verdict(pass_id="B", confidence=0.85, model_identity=_SILENT_IDENTITY)
        rows = arbitrate({"go-q001": a}, {"go-q001": b}, [_SAMPLE_QREL])
        assert rows[0]["review_status"] == "AI_REVIEWED"
        assert rows[0]["reviewer_identity_status"] == "MODEL_IDENTITY_UNVERIFIED"


class TestSummaryCarriesIdentity:
    def test_summary_reports_unverified_by_default(self):
        a = _make_verdict(model_identity=_SILENT_IDENTITY)
        b = _make_verdict(pass_id="B", confidence=0.85, model_identity=_SILENT_IDENTITY)
        rows = arbitrate({"go-q001": a}, {"go-q001": b}, [_SAMPLE_QREL])
        summary = summarize(rows, {"go-q001": a}, {"go-q001": b}, {"model": "gpt-5.6-sol"})
        assert summary["model_identity_status"] == "MODEL_IDENTITY_UNVERIFIED"

    def test_summary_reports_verified_and_observed_models(self):
        a = _make_verdict(model_identity=_VERIFIED_IDENTITY)
        b = _make_verdict(pass_id="B", confidence=0.85, model_identity=_VERIFIED_IDENTITY)
        rows = arbitrate({"go-q001": a}, {"go-q001": b}, [_SAMPLE_QREL])
        summary = summarize(rows, {"go-q001": a}, {"go-q001": b}, {"model": "gpt-5.6-sol"})
        assert summary["model_identity_status"] == "MODEL_IDENTITY_VERIFIED"
        assert summary["observed_models"] == ["gpt-5.6-sol-2026-07"]
        assert summary["observed_system_fingerprints"] == ["fp_deadbeef01"]

    def test_summary_identity_has_no_credentials(self):
        a = _make_verdict(
            model_identity=dict(_VERIFIED_IDENTITY, api_key="sk-nope")
        )
        b = _make_verdict(pass_id="B", confidence=0.85, model_identity=_VERIFIED_IDENTITY)
        rows = arbitrate({"go-q001": a}, {"go-q001": b}, [_SAMPLE_QREL])
        summary = summarize(rows, {"go-q001": a}, {"go-q001": b}, {"model": "gpt-5.6-sol"})
        text = json.dumps(summary)
        assert "sk-nope" not in text
        assert "api_key" not in text


class TestNoSilentBackfillRegression:
    def test_module_never_backfills_reported_model_from_revision(self):
        """AST-level guard: `revision` must not feed a reported-identity field."""
        import ast

        source = (
            PROJECT_ROOT / "orchestrator" / "eval" / "sol_reviewer.py"
        ).read_text(encoding="utf-8")
        tree = ast.parse(source)
        for node in ast.walk(tree):
            if not isinstance(node, ast.Dict):
                continue
            for key, value in zip(node.keys, node.values):
                if not isinstance(key, ast.Constant) or not isinstance(key.value, str):
                    continue
                if key.value not in {
                    "reviewer_reported_model",
                    "reviewer_system_fingerprint",
                    "reviewer_response_id",
                }:
                    continue
                names = {
                    n.id for n in ast.walk(value) if isinstance(n, ast.Name)
                }
                assert "revision" not in names, (
                    f"{key.value} is derived from the CLI `revision` input — "
                    "that is the §20.4 false-identity failure"
                )
                assert "model" not in names, (
                    f"{key.value} is derived from the requested `model` input"
                )


class TestResumeRoundTripPreservesIdentity:
    """Identity written to a sidecar must survive being read back.

    Two separate code paths rebuild a ``PassVerdict`` from a sidecar row: the
    resume branch inside :func:`run_pass` and :func:`_reconstruct_verdicts`
    (used by ``--only arbitrate``).  If either drops the identity, a resumed or
    arbitrate-only run reports ``MODEL_IDENTITY_UNVERIFIED`` even though a real
    provider fingerprint is sitting on disk — the artifact would understate
    evidence it actually holds, which is the same class of dishonesty as
    overstating it.
    """

    def test_row_to_verdict_restores_identity(self):
        v = _make_verdict(model_identity=_VERIFIED_IDENTITY)
        row = _verdict_to_row(v, _SAMPLE_QREL, "gpt-5.6-sol", "unknown")
        restored = _row_to_verdict(row, "go-q001", "A")
        assert restored.model_identity["reported_model"] == "gpt-5.6-sol-2026-07"
        assert restored.model_identity["system_fingerprint"] == "fp_deadbeef01"
        assert resolve_identity_status([restored.model_identity]) == "MODEL_IDENTITY_VERIFIED"

    def test_row_to_verdict_keeps_silent_identity_silent(self):
        """A blank on-disk identity must not become verified on reload."""
        v = _make_verdict(model_identity=_SILENT_IDENTITY)
        row = _verdict_to_row(v, _SAMPLE_QREL, "gpt-5.6-sol", "2026-07-claimed")
        restored = _row_to_verdict(row, "go-q001", "A")
        assert restored.model_identity.get("reported_model", "") == ""
        assert restored.model_identity.get("system_fingerprint", "") == ""
        assert resolve_identity_status([restored.model_identity]) == "MODEL_IDENTITY_UNVERIFIED"

    def test_row_to_verdict_preserves_verdict_fields(self):
        v = _make_verdict(model_identity=_VERIFIED_IDENTITY, confidence=0.83)
        row = _verdict_to_row(v, _SAMPLE_QREL, "gpt-5.6-sol", "unknown")
        restored = _row_to_verdict(row, "go-q001", "A")
        assert restored.failed is False
        assert restored.confidence == 0.83
        assert restored.relevance_correct is True
        assert restored.prompt_hash == "abcd1234deadbeef"

    def test_row_to_verdict_preserves_failed_rows(self):
        v = PassVerdict(
            query_id="go-q001", pass_id="A", failed=True, fail_reason="llm_error",
            answerable=False, language_correct=False, query_type_correct=False,
            relevance_correct=False, section_correct=False, evidence_sufficient=False,
            contamination_risk="none", confidence=0.0,
            prompt_hash="abcd1234deadbeef", evidence_refs=(),
            model_identity={},
        )
        row = _verdict_to_row(v, _SAMPLE_QREL, "gpt-5.6-sol", "unknown")
        restored = _row_to_verdict(row, "go-q001", "A")
        assert restored.failed is True
        assert restored.fail_reason == "llm_error"
        assert restored.model_identity == {}

    def test_reconstruct_verdicts_restores_identity_from_disk(self, tmp_path):
        sidecar = tmp_path / "passA.jsonl"
        v = _make_verdict(model_identity=_VERIFIED_IDENTITY)
        write_sidecar_row(sidecar, _verdict_to_row(v, _SAMPLE_QREL, "gpt-5.6-sol", "unknown"))

        loaded = load_sidecar(sidecar)
        rebuilt = _reconstruct_verdicts(loaded, "A")
        assert set(rebuilt) == {"go-q001"}
        assert rebuilt["go-q001"].model_identity["system_fingerprint"] == "fp_deadbeef01"

    def test_arbitrate_after_reload_is_verified(self, tmp_path):
        """End-to-end: write both passes, reload, arbitrate, stay VERIFIED."""
        path_a = tmp_path / "passA.jsonl"
        path_b = tmp_path / "passB.jsonl"
        a = _make_verdict(model_identity=_VERIFIED_IDENTITY)
        b = _make_verdict(pass_id="B", confidence=0.85, model_identity=_VERIFIED_IDENTITY)
        write_sidecar_row(path_a, _verdict_to_row(a, _SAMPLE_QREL, "gpt-5.6-sol", "unknown"))
        write_sidecar_row(path_b, _verdict_to_row(b, _SAMPLE_QREL, "gpt-5.6-sol", "unknown"))

        rebuilt_a = _reconstruct_verdicts(load_sidecar(path_a), "A")
        rebuilt_b = _reconstruct_verdicts(load_sidecar(path_b), "B")
        rows = arbitrate(rebuilt_a, rebuilt_b, [_SAMPLE_QREL])
        assert rows[0]["reviewer_identity_status"] == "MODEL_IDENTITY_VERIFIED"

        summary = summarize(rows, rebuilt_a, rebuilt_b, {"model": "gpt-5.6-sol"})
        assert summary["model_identity_status"] == "MODEL_IDENTITY_VERIFIED"
        assert summary["observed_system_fingerprints"] == ["fp_deadbeef01"]

    def test_both_rebuild_paths_share_one_helper(self):
        """AST guard against the two rebuilds drifting apart again.

        The identity drop existed in *two* places because the row-to-verdict
        logic was duplicated.  Pin that both callers go through one helper so a
        future field cannot be wired into one path and forgotten in the other.
        """
        import ast

        source = (
            PROJECT_ROOT / "orchestrator" / "eval" / "sol_reviewer.py"
        ).read_text(encoding="utf-8")
        tree = ast.parse(source)

        def _calls_in(fn_name: str) -> set[str]:
            for node in ast.walk(tree):
                is_fn = isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef))
                if is_fn and node.name == fn_name:
                    return {
                        n.func.id
                        for n in ast.walk(node)
                        if isinstance(n, ast.Call) and isinstance(n.func, ast.Name)
                    }
            raise AssertionError(f"{fn_name} not found")

        assert "_row_to_verdict" in _calls_in("run_pass"), (
            "run_pass rebuilds a PassVerdict inline instead of using the shared "
            "_row_to_verdict helper — identity will drift out of the resume path"
        )
        assert "_row_to_verdict" in _calls_in("_reconstruct_verdicts"), (
            "_reconstruct_verdicts rebuilds a PassVerdict inline instead of "
            "using the shared _row_to_verdict helper"
        )


if __name__ == "__main__":  # pragma: no cover
    raise SystemExit(pytest.main([__file__, "-q"]))
