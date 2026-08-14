"""Fail-closed materialization of human-reviewed document-native page Qrels.

The resulting artifact is intentionally not a release or scoring input.  It
only makes a human-signed query-to-reviewed-element decision auditable while
preserving the separate split, contamination, and evaluator gates.
"""

from __future__ import annotations

import base64
import json
import re
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from orchestrator.eval.multimodal_human_review import (
    HumanReviewContractError,
    _load_candidates,
    _load_decisions,
    _reviewer_hash,
    _sha256_file,
    _verify_receipt,
)

try:
    from cryptography.exceptions import InvalidSignature
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
except ImportError:  # pragma: no cover
    InvalidSignature = ValueError  # type: ignore[assignment,misc]
    Ed25519PublicKey = None  # type: ignore[assignment,misc]


SCHEMA_VERSION = "multimodal-page-qrels/v1"
STATUS = "HUMAN_REVIEWED_PAGE_QRELS_NOT_RELEASED"
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
_ANSWER_FIELDS = frozenset({"answer", "answers", "gold_answer", "expected_answer"})


class HumanPageQrelsError(ValueError):
    """Raised when a page-Qrels artifact lacks a complete human evidence chain."""


def materialize_human_reviewed_page_qrels(
    *,
    candidates_path: Path | str,
    decisions_path: Path | str,
    evidence_receipt_path: Path | str,
    evidence_reviewer_public_key_b64: str,
    expected_evidence_reviewer_key_id: str,
    query_links_path: Path | str,
    query_links_receipt_path: Path | str,
    link_reviewer_public_key_b64: str,
    expected_link_reviewer_key_id: str,
    license_allowlist_path: Path | str,
    ocr_receipts_by_document: Mapping[str, Path | str],
    out_dir: Path | str,
) -> Path:
    """Write a non-scoreable page-Qrels version from two signed review stages.

    The candidate-stage signature says that a page element and its geometry are
    human reviewed.  The link-stage signature separately says that a query is
    supported by that reviewed element and binds the exact PDF allowlist used
    for the release.  Neither a source bbox nor OCR text can substitute for
    the link-stage decision.
    """
    candidates_file = Path(candidates_path)
    decisions_file = Path(decisions_path)
    links_file = Path(query_links_path)
    allowlist_file = Path(license_allowlist_path)
    candidates_sha256 = _sha256_file(candidates_file)
    decisions_sha256 = _sha256_file(decisions_file)
    links_sha256 = _sha256_file(links_file)
    allowlist_sha256 = _sha256_file(allowlist_file)

    try:
        candidates = _load_candidates(candidates_file)
        decisions = _load_decisions(decisions_file, candidates)
        _verify_receipt(
            evidence_receipt_path,
            candidates_sha256=candidates_sha256,
            decisions_sha256=decisions_sha256,
            reviewer_public_key_b64=evidence_reviewer_public_key_b64,
            expected_reviewer_key_id=expected_evidence_reviewer_key_id,
        )
    except HumanReviewContractError as exc:
        raise HumanPageQrelsError(f"EVIDENCE_REVIEW_INVALID: {exc}") from exc

    _verify_link_receipt(
        query_links_receipt_path,
        candidates_sha256=candidates_sha256,
        decisions_sha256=decisions_sha256,
        links_sha256=links_sha256,
        allowlist_sha256=allowlist_sha256,
        reviewer_public_key_b64=link_reviewer_public_key_b64,
        expected_reviewer_key_id=expected_link_reviewer_key_id,
    )
    reviewed = _reviewed_candidates(candidates, decisions)
    links = _load_links(links_file)
    allowed_documents = _load_allowed_documents(allowlist_file)

    rows: list[dict[str, Any]] = []
    ocr_receipt_sha256_by_document: dict[str, str] = {}
    seen_links: set[tuple[str, str]] = set()
    query_text: dict[str, str] = {}
    for link in links:
        query_id = link["query_id"]
        candidate_id = link["candidate_id"]
        pair = (query_id, candidate_id)
        if pair in seen_links:
            raise HumanPageQrelsError("QUERY_LINK_DUPLICATE")
        seen_links.add(pair)
        if query_id in query_text and query_text[query_id] != link["query"]:
            raise HumanPageQrelsError("QUERY_TEXT_INCONSISTENT")
        query_text[query_id] = link["query"]
        candidate = reviewed.get(candidate_id)
        if candidate is None:
            raise HumanPageQrelsError("QUERY_LINK_EVIDENCE_NOT_HUMAN_REVIEWED")
        allowed_pdf_sha256 = allowed_documents.get(candidate["document_id"])
        if allowed_pdf_sha256 is None:
            raise HumanPageQrelsError("PDF_LICENSE_NOT_ALLOWLISTED")
        ocr_receipt_sha256_by_document[candidate["document_id"]] = _verify_candidate_ocr_receipt(
            candidate,
            allowed_pdf_sha256=allowed_pdf_sha256,
            receipt_path=ocr_receipts_by_document.get(candidate["document_id"]),
        )
        rows.append(
            {
                "query_id": query_id,
                "query": link["query"],
                "document_id": candidate["document_id"],
                "page_id": candidate["page_id"],
                "element_id": candidate["element_id"],
                "bbox": candidate["bbox"],
                "coordinate_system": candidate["coordinate_system"],
                "candidate_id": candidate_id,
                "relevance": 1,
                "review_status": "HUMAN_REVIEWED",
            }
        )
    if not rows:
        raise HumanPageQrelsError("QUERY_LINKS_EMPTY")

    destination = Path(out_dir)
    if destination.exists():
        raise HumanPageQrelsError("PAGE_QRELS_OUTPUT_EXISTS")
    destination.mkdir(parents=True)
    qrels_path = destination / "qrels.jsonl"
    qrels_path.write_text(
        "".join(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n" for row in rows),
        encoding="utf-8",
    )
    manifest = {
        "schema_version": SCHEMA_VERSION,
        "status": STATUS,
        "qrels": True,
        "scoreable": False,
        "qrels_count": len(rows),
        "qrels_sha256": _sha256_file(qrels_path),
        "candidates_sha256": candidates_sha256,
        "decisions_sha256": decisions_sha256,
        "query_links_sha256": links_sha256,
        "license_allowlist_sha256": allowlist_sha256,
        "evidence_receipt_sha256": _sha256_file(evidence_receipt_path),
        "query_links_receipt_sha256": _sha256_file(query_links_receipt_path),
        "mineru_ocr_receipt_sha256_by_document": ocr_receipt_sha256_by_document,
        "evidence_reviewer_key_id": expected_evidence_reviewer_key_id,
        "link_reviewer_key_id": expected_link_reviewer_key_id,
        "link_reviewer_hashes": sorted(
            {
                _reviewer_hash(expected_link_reviewer_key_id, link["reviewer_id"])
                for link in links
            }
        ),
        "limitations": {
            "release_gate_complete": False,
            "split_frozen": False,
            "contamination_scanned": False,
            "independent_scorer_receipt": False,
        },
    }
    (destination / "manifest.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    return destination


def _reviewed_candidates(candidates: list[dict[str, Any]], decisions: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    by_id = {str(candidate["candidate_id"]): candidate for candidate in candidates}
    result: dict[str, dict[str, Any]] = {}
    for decision in decisions:
        if decision["review_decision"] not in {"ACCEPT", "CORRECT"}:
            continue
        candidate = by_id[str(decision["candidate_id"])]
        result[str(candidate["candidate_id"])] = {
            **candidate,
            "bbox": decision["corrected_bbox"]
            if decision["review_decision"] == "CORRECT"
            else candidate["bbox"],
        }
    return result


def _load_links(path: Path) -> list[dict[str, str]]:
    try:
        raw_rows = [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    except (OSError, json.JSONDecodeError) as exc:
        raise HumanPageQrelsError("QUERY_LINKS_INVALID") from exc
    links: list[dict[str, str]] = []
    for row in raw_rows:
        if not isinstance(row, Mapping) or _ANSWER_FIELDS.intersection(row):
            raise HumanPageQrelsError("QUERY_LINKS_INVALID")
        required = ("query_id", "query", "candidate_id", "review_note", "reviewed_at", "reviewer_id")
        if any(not isinstance(row.get(field), str) or not row[field].strip() for field in required):
            raise HumanPageQrelsError("QUERY_LINKS_INVALID")
        links.append({field: row[field] for field in required})
    return links


def _load_allowed_documents(path: Path) -> dict[str, str]:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise HumanPageQrelsError("PDF_LICENSE_ALLOWLIST_INVALID") from exc
    if not isinstance(payload, Mapping) or payload.get("schema_version") != "multimodal-pdf-license-allowlist/v1":
        raise HumanPageQrelsError("PDF_LICENSE_ALLOWLIST_INVALID")
    documents = payload.get("documents")
    if not isinstance(documents, list):
        raise HumanPageQrelsError("PDF_LICENSE_ALLOWLIST_INVALID")
    allowed: dict[str, str] = {}
    for document in documents:
        if not isinstance(document, Mapping):
            raise HumanPageQrelsError("PDF_LICENSE_ALLOWLIST_INVALID")
        document_id = document.get("document_id")
        source_pdf_sha256 = document.get("source_pdf_sha256")
        if (
            not isinstance(document_id, str)
            or not document_id
            or not isinstance(source_pdf_sha256, str)
            or not _SHA256_RE.fullmatch(source_pdf_sha256)
            or document.get("redistribution_permitted") is not True
            or not isinstance(document.get("license_spdx"), str)
            or not document["license_spdx"].strip()
            or not isinstance(document.get("evidence_url"), str)
            or not document["evidence_url"].strip()
            or document_id in allowed
        ):
            raise HumanPageQrelsError("PDF_LICENSE_ALLOWLIST_INVALID")
        allowed[document_id] = source_pdf_sha256
    return allowed


def _verify_candidate_ocr_receipt(
    candidate: Mapping[str, Any], *, allowed_pdf_sha256: str, receipt_path: Path | str | None
) -> str:
    if receipt_path is None:
        raise HumanPageQrelsError("MINERU_OCR_RECEIPT_MISSING")
    try:
        path = Path(receipt_path)
        receipt = json.loads(path.read_text(encoding="utf-8"))
        mineru = candidate["mineru"]
        if (
            not isinstance(receipt, Mapping)
            or not isinstance(mineru, Mapping)
            or receipt.get("schema_version") != "mineru-explicit-ocr-receipt/v1"
            or receipt.get("ocr_mode") != "explicit"
            or receipt.get("exit_code") != 0
            or receipt.get("input_pdf_sha256") != allowed_pdf_sha256
            or receipt.get("content_sha256") != mineru.get("content_sha256")
            or receipt.get("middle_sha256") != mineru.get("middle_sha256")
            or _sha256_file(path) != mineru.get("receipt_sha256")
        ):
            raise ValueError("OCR receipt binding mismatch")
    except (OSError, TypeError, ValueError, json.JSONDecodeError) as exc:
        raise HumanPageQrelsError("MINERU_OCR_RECEIPT_INVALID") from exc
    return _sha256_file(path)


def _verify_link_receipt(
    receipt_path: Path | str,
    *,
    candidates_sha256: str,
    decisions_sha256: str,
    links_sha256: str,
    allowlist_sha256: str,
    reviewer_public_key_b64: str,
    expected_reviewer_key_id: str,
) -> None:
    expected = {
        "candidates_sha256": candidates_sha256,
        "decisions_sha256": decisions_sha256,
        "links_sha256": links_sha256,
        "license_allowlist_sha256": allowlist_sha256,
        "reviewer_key_id": expected_reviewer_key_id,
    }
    try:
        receipt = json.loads(Path(receipt_path).read_text(encoding="utf-8"))
        if not isinstance(receipt, Mapping) or any(receipt.get(key) != value for key, value in expected.items()):
            raise ValueError("receipt binding mismatch")
        signature = base64.b64decode(str(receipt["signature_b64"]), validate=True)
        public_key = base64.b64decode(reviewer_public_key_b64, validate=True)
        if Ed25519PublicKey is None:
            raise ValueError("cryptography is not installed")
        message = json.dumps(expected, sort_keys=True, separators=(",", ":")).encode("utf-8")
        Ed25519PublicKey.from_public_bytes(public_key).verify(signature, message)
    except (KeyError, ValueError, TypeError, InvalidSignature, base64.binascii.Error) as exc:
        raise HumanPageQrelsError("QUERY_LINK_RECEIPT_INVALID") from exc
