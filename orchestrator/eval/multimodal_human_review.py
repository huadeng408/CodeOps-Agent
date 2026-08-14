"""Fail-closed human review workflow for multimodal evidence candidates.

This module intentionally creates a non-scoreable evidence manifest.  A
separate dataset release process must still pin, split, contamination-scan, and
score a new qrels version before any evaluation or index gate can use it.
"""

from __future__ import annotations

import base64
import hashlib
import json
import math
import re
from pathlib import Path
from typing import Any

from orchestrator.eval.multimodal_candidate import CandidateContractError, validate_candidate

try:
    from cryptography.exceptions import InvalidSignature
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
except ImportError:  # pragma: no cover
    InvalidSignature = ValueError  # type: ignore[assignment,misc]
    Ed25519PublicKey = None  # type: ignore[assignment,misc]


SCHEMA_VERSION = "multimodal-human-reviewed-evidence/v1"
_DECISIONS = frozenset({"ACCEPT", "CORRECT", "REJECT"})
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")


class HumanReviewContractError(ValueError):
    """Raised when a human-review artifact cannot be trusted or bound."""


def export_review_worksheet(candidates_path: Path | str, out_path: Path | str) -> Path:
    """Export immutable candidates with blank fields for an external reviewer."""
    candidates = _load_candidates(candidates_path)
    worksheet = [
        {
            **candidate,
            "review_decision": "",
            "corrected_bbox": None,
            "review_note": "",
            "reviewed_at": "",
            "reviewer_id": "",
        }
        for candidate in candidates
    ]
    return _write_jsonl(out_path, worksheet)


def freeze_human_reviewed_evidence(
    candidates_path: Path | str,
    decisions_path: Path | str,
    receipt_path: Path | str,
    out_path: Path | str,
    *,
    reviewer_public_key_b64: str,
    expected_reviewer_key_id: str,
) -> Path:
    """Verify signed decisions and freeze only accepted/corrected candidates.

    A signature binds the exact candidate and decision bytes.  This protects
    against reviewing one record set then replaying that receipt over changed
    candidate geometry or decisions.  The output is not a qrels artifact and
    deliberately declares ``scoreable=false``.
    """
    candidate_file = Path(candidates_path)
    decision_file = Path(decisions_path)
    candidates = _load_candidates(candidate_file)
    decisions = _load_decisions(decision_file, candidates)
    _verify_receipt(
        receipt_path,
        candidates_sha256=_sha256_file(candidate_file),
        decisions_sha256=_sha256_file(decision_file),
        reviewer_public_key_b64=reviewer_public_key_b64,
        expected_reviewer_key_id=expected_reviewer_key_id,
    )

    candidate_by_id = {str(candidate["candidate_id"]): candidate for candidate in candidates}
    evidence: list[dict[str, Any]] = []
    for decision in decisions:
        if decision["review_decision"] not in {"ACCEPT", "CORRECT"}:
            continue
        candidate = candidate_by_id[decision["candidate_id"]]
        bbox = decision["corrected_bbox"] if decision["review_decision"] == "CORRECT" else candidate["bbox"]
        _validate_xyxy_bbox(bbox)
        evidence.append(
            {
                "candidate_id": candidate["candidate_id"],
                "document_id": candidate["document_id"],
                "page_id": candidate["page_id"],
                "element_id": candidate["element_id"],
                "bbox": bbox,
                "coordinate_system": candidate["coordinate_system"],
                "page_image_sha256": candidate["page_image_sha256"],
                "source": candidate["source"],
                "mineru": candidate["mineru"],
                "review_status": "HUMAN_REVIEWED",
                "review_decision": decision["review_decision"],
                "reviewed_at": decision["reviewed_at"],
                "reviewer_hash": _reviewer_hash(
                    expected_reviewer_key_id, decision["reviewer_id"]
                ),
                "review_note": decision["review_note"],
            }
        )

    manifest = {
        "schema_version": SCHEMA_VERSION,
        "scoreable": False,
        "qrels": False,
        "candidates_sha256": _sha256_file(candidate_file),
        "decisions_sha256": _sha256_file(decision_file),
        "receipt_sha256": _sha256_file(receipt_path),
        "reviewer_key_id": expected_reviewer_key_id,
        "evidence": evidence,
    }
    out = Path(out_path)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return out


def _load_candidates(path: Path | str) -> list[dict[str, Any]]:
    candidates = _load_jsonl(path)
    if not candidates:
        raise HumanReviewContractError("CANDIDATES_EMPTY")
    ids: set[str] = set()
    for candidate in candidates:
        try:
            validate_candidate(candidate)
        except CandidateContractError as exc:
            raise HumanReviewContractError(f"CANDIDATE_INVALID: {exc}") from exc
        candidate_id = str(candidate["candidate_id"])
        if candidate_id in ids:
            raise HumanReviewContractError(f"CANDIDATE_DUPLICATE: {candidate_id}")
        ids.add(candidate_id)
    return candidates


def _load_decisions(path: Path | str, candidates: list[dict[str, Any]]) -> list[dict[str, Any]]:
    rows = _load_jsonl(path)
    candidate_ids = {str(candidate["candidate_id"]) for candidate in candidates}
    if len(rows) != len(candidate_ids):
        raise HumanReviewContractError("DECISION_COVERAGE_MISMATCH")
    seen: set[str] = set()
    for row in rows:
        candidate_id = row.get("candidate_id")
        if not isinstance(candidate_id, str) or candidate_id not in candidate_ids or candidate_id in seen:
            raise HumanReviewContractError("DECISION_CANDIDATE_MISMATCH")
        seen.add(candidate_id)
        if row.get("review_decision") not in _DECISIONS:
            raise HumanReviewContractError("DECISION_INVALID")
        for field in ("review_note", "reviewed_at", "reviewer_id"):
            if not isinstance(row.get(field), str) or not row[field].strip():
                raise HumanReviewContractError(f"DECISION_MISSING_{field.upper()}")
        corrected = row.get("corrected_bbox")
        if row["review_decision"] == "CORRECT":
            _validate_xyxy_bbox(corrected)
        elif corrected is not None:
            raise HumanReviewContractError("DECISION_UNEXPECTED_CORRECTED_BBOX")
    return rows


def _verify_receipt(
    receipt_path: Path | str,
    *,
    candidates_sha256: str,
    decisions_sha256: str,
    reviewer_public_key_b64: str,
    expected_reviewer_key_id: str,
) -> None:
    if not reviewer_public_key_b64 or not expected_reviewer_key_id:
        raise HumanReviewContractError("RECEIPT_TRUST_ROOT_MISSING")
    try:
        receipt = json.loads(Path(receipt_path).read_text(encoding="utf-8"))
        if not isinstance(receipt, dict):
            raise ValueError("receipt is not an object")
        payload = {
            "candidates_sha256": receipt["candidates_sha256"],
            "decisions_sha256": receipt["decisions_sha256"],
            "reviewer_key_id": receipt["reviewer_key_id"],
        }
        if (
            payload["candidates_sha256"] != candidates_sha256
            or payload["decisions_sha256"] != decisions_sha256
            or payload["reviewer_key_id"] != expected_reviewer_key_id
        ):
            raise HumanReviewContractError("RECEIPT_BINDING_MISMATCH")
        signature = base64.b64decode(str(receipt["signature_b64"]), validate=True)
        public_key = base64.b64decode(reviewer_public_key_b64, validate=True)
        if Ed25519PublicKey is None:
            raise ValueError("cryptography is not installed")
        message = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
        Ed25519PublicKey.from_public_bytes(public_key).verify(signature, message)
    except HumanReviewContractError:
        raise
    except (KeyError, ValueError, TypeError, InvalidSignature, base64.binascii.Error) as exc:
        raise HumanReviewContractError(f"RECEIPT_INVALID: {exc}") from exc


def _validate_xyxy_bbox(value: Any) -> None:
    if not isinstance(value, list) or len(value) != 4 or not all(
        isinstance(item, (int, float)) and not isinstance(item, bool) for item in value
    ):
        raise HumanReviewContractError("CORRECTED_BBOX_INVALID")
    if not all(math.isfinite(item) for item in value):
        raise HumanReviewContractError("CORRECTED_BBOX_INVALID")
    if value[2] <= value[0] or value[3] <= value[1]:
        raise HumanReviewContractError("CORRECTED_BBOX_INVALID")


def _load_jsonl(path: Path | str) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for line_number, line in enumerate(Path(path).read_text(encoding="utf-8").splitlines(), 1):
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except json.JSONDecodeError as exc:
            raise HumanReviewContractError(f"JSONL_INVALID_LINE_{line_number}") from exc
        if not isinstance(row, dict):
            raise HumanReviewContractError(f"JSONL_NOT_OBJECT_LINE_{line_number}")
        rows.append(row)
    return rows


def _write_jsonl(path: Path | str, rows: list[dict[str, Any]]) -> Path:
    out = Path(path)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text("".join(json.dumps(row, ensure_ascii=False) + "\n" for row in rows), encoding="utf-8")
    return out


def _sha256_file(path: Path | str) -> str:
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def _reviewer_hash(reviewer_key_id: str, reviewer_id: str) -> str:
    """Keep the human identity outside the frozen shared evidence manifest."""
    return hashlib.sha256(f"{reviewer_key_id}|{reviewer_id}".encode("utf-8")).hexdigest()[:16]
