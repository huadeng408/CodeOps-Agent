"""Human review artifacts must not turn unchecked candidates into gold."""

from __future__ import annotations

import base64
import hashlib
import json
from datetime import UTC, datetime
from pathlib import Path

import pytest
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from orchestrator.eval.multimodal_human_review import (
    HumanReviewContractError,
    export_review_worksheet,
    freeze_human_reviewed_evidence,
)


def _candidate(candidate_id: str = "candidate-001") -> dict:
    return {
        "schema_version": "multimodal-evidence-candidate/v1",
        "candidate_status": "AI_CANDIDATE",
        "candidate_id": candidate_id,
        "document_id": "doc-001",
        "page_id": "page-002",
        "element_id": "element-004",
        "bbox": [10.0, 20.0, 30.0, 40.0],
        "coordinate_system": "page_1000_xyxy",
        "page_image_sha256": "a" * 64,
        "source": {
            "source_id": "docvqa",
            "source_revision": "abc123",
            "license_spdx": "MIT",
        },
        "mineru": {
            "version": "3.4.4",
            "ocr_mode": "explicit",
            "content_sha256": "b" * 64,
        },
    }


def _write_jsonl(path: Path, rows: list[dict]) -> Path:
    path.write_text("".join(json.dumps(row) + "\n" for row in rows), encoding="utf-8")
    return path


def _sign_receipt(candidate_path: Path, decision_path: Path, private_key: Ed25519PrivateKey) -> tuple[dict, str]:
    candidates_sha256 = hashlib.sha256(candidate_path.read_bytes()).hexdigest()
    decisions_sha256 = hashlib.sha256(decision_path.read_bytes()).hexdigest()
    payload = {
        "candidates_sha256": candidates_sha256,
        "decisions_sha256": decisions_sha256,
        "reviewer_key_id": "controlled-human-reviewer-1",
    }
    message = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
    receipt = {
        **payload,
        "signature_b64": base64.b64encode(private_key.sign(message)).decode("ascii"),
    }
    public_key_b64 = base64.b64encode(
        private_key.public_key().public_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PublicFormat.Raw,
        )
    ).decode("ascii")
    return receipt, public_key_b64


def test_export_review_worksheet_preserves_candidate_and_uses_blank_decision_fields(tmp_path: Path) -> None:
    candidates = _write_jsonl(tmp_path / "candidates.jsonl", [_candidate()])

    worksheet = export_review_worksheet(candidates, tmp_path / "worksheet.jsonl")
    row = json.loads(worksheet.read_text(encoding="utf-8"))

    assert row["candidate_status"] == "AI_CANDIDATE"
    assert row["bbox"] == [10.0, 20.0, 30.0, 40.0]
    assert row["review_decision"] == ""
    assert row["corrected_bbox"] is None
    assert row["review_note"] == ""
    assert "HUMAN_REVIEWED" not in worksheet.read_text(encoding="utf-8")


def test_freeze_requires_trusted_signature_and_emits_only_accepted_evidence(tmp_path: Path) -> None:
    candidates = _write_jsonl(tmp_path / "candidates.jsonl", [_candidate(), _candidate("candidate-002")])
    decisions = _write_jsonl(
        tmp_path / "decisions.jsonl",
        [
            {
                "candidate_id": "candidate-001",
                "review_decision": "ACCEPT",
                "corrected_bbox": None,
                "review_note": "Verified against the page image.",
                "reviewed_at": datetime.now(UTC).isoformat(),
                "reviewer_id": "reviewer-1",
            },
            {
                "candidate_id": "candidate-002",
                "review_decision": "REJECT",
                "corrected_bbox": None,
                "review_note": "The element does not support the task.",
                "reviewed_at": datetime.now(UTC).isoformat(),
                "reviewer_id": "reviewer-1",
            },
        ],
    )
    private_key = Ed25519PrivateKey.generate()
    receipt, public_key_b64 = _sign_receipt(candidates, decisions, private_key)
    receipt_path = tmp_path / "receipt.json"
    receipt_path.write_text(json.dumps(receipt), encoding="utf-8")

    manifest = freeze_human_reviewed_evidence(
        candidates,
        decisions,
        receipt_path,
        tmp_path / "frozen.json",
        reviewer_public_key_b64=public_key_b64,
        expected_reviewer_key_id="controlled-human-reviewer-1",
    )

    result = json.loads(manifest.read_text(encoding="utf-8"))
    assert result["schema_version"] == "multimodal-human-reviewed-evidence/v1"
    assert result["evidence"][0]["review_status"] == "HUMAN_REVIEWED"
    assert result["evidence"][0]["candidate_id"] == "candidate-001"
    assert result["evidence"][0]["reviewer_hash"] == hashlib.sha256(
        "controlled-human-reviewer-1|reviewer-1".encode("utf-8")
    ).hexdigest()[:16]
    assert "reviewer_id" not in result["evidence"][0]
    assert len(result["evidence"]) == 1
    assert result["scoreable"] is False


def test_freeze_rejects_untrusted_or_unbound_review_receipt(tmp_path: Path) -> None:
    candidates = _write_jsonl(tmp_path / "candidates.jsonl", [_candidate()])
    decisions = _write_jsonl(
        tmp_path / "decisions.jsonl",
        [{
            "candidate_id": "candidate-001",
            "review_decision": "ACCEPT",
            "corrected_bbox": None,
            "review_note": "Checked.",
            "reviewed_at": "2026-08-14T00:00:00+00:00",
            "reviewer_id": "reviewer-1",
        }],
    )
    private_key = Ed25519PrivateKey.generate()
    receipt, public_key_b64 = _sign_receipt(candidates, decisions, private_key)
    receipt["decisions_sha256"] = "0" * 64
    receipt_path = tmp_path / "receipt.json"
    receipt_path.write_text(json.dumps(receipt), encoding="utf-8")

    with pytest.raises(HumanReviewContractError, match="RECEIPT_BINDING_MISMATCH"):
        freeze_human_reviewed_evidence(
            candidates,
            decisions,
            receipt_path,
            tmp_path / "frozen.json",
            reviewer_public_key_b64=public_key_b64,
            expected_reviewer_key_id="controlled-human-reviewer-1",
        )


def test_freeze_uses_human_corrected_bbox_not_ai_candidate_bbox(tmp_path: Path) -> None:
    candidates = _write_jsonl(tmp_path / "candidates.jsonl", [_candidate()])
    decisions = _write_jsonl(
        tmp_path / "decisions.jsonl",
        [{
            "candidate_id": "candidate-001",
            "review_decision": "CORRECT",
            "corrected_bbox": [11, 21, 31, 41],
            "review_note": "Adjusted to the visible element boundary.",
            "reviewed_at": "2026-08-14T00:00:00+00:00",
            "reviewer_id": "reviewer-1",
        }],
    )
    private_key = Ed25519PrivateKey.generate()
    receipt, public_key_b64 = _sign_receipt(candidates, decisions, private_key)
    receipt_path = tmp_path / "receipt.json"
    receipt_path.write_text(json.dumps(receipt), encoding="utf-8")

    manifest = freeze_human_reviewed_evidence(
        candidates,
        decisions,
        receipt_path,
        tmp_path / "frozen.json",
        reviewer_public_key_b64=public_key_b64,
        expected_reviewer_key_id="controlled-human-reviewer-1",
    )

    evidence = json.loads(manifest.read_text(encoding="utf-8"))["evidence"]
    assert evidence[0]["bbox"] == [11, 21, 31, 41]
    assert evidence[0]["review_decision"] == "CORRECT"


def test_freeze_rejects_missing_trust_root(tmp_path: Path) -> None:
    candidates = _write_jsonl(tmp_path / "candidates.jsonl", [_candidate()])
    decisions = _write_jsonl(
        tmp_path / "decisions.jsonl",
        [{
            "candidate_id": "candidate-001",
            "review_decision": "ACCEPT",
            "corrected_bbox": None,
            "review_note": "Checked.",
            "reviewed_at": "2026-08-14T00:00:00+00:00",
            "reviewer_id": "reviewer-1",
        }],
    )
    receipt_path = tmp_path / "receipt.json"
    receipt_path.write_text("{}", encoding="utf-8")

    with pytest.raises(HumanReviewContractError, match="RECEIPT_TRUST_ROOT_MISSING"):
        freeze_human_reviewed_evidence(
            candidates,
            decisions,
            receipt_path,
            tmp_path / "frozen.json",
            reviewer_public_key_b64="",
            expected_reviewer_key_id="",
        )
