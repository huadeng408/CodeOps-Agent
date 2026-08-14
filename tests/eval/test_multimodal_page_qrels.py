"""Page-level Qrels must be chained to signed human evidence."""

from __future__ import annotations

import base64
import hashlib
import json
from pathlib import Path

from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
import pytest


def _write_json(path: Path, value: object) -> Path:
    path.write_text(json.dumps(value, sort_keys=True) + "\n", encoding="utf-8")
    return path


def _write_jsonl(path: Path, rows: list[dict]) -> Path:
    path.write_text("".join(json.dumps(row, sort_keys=True) + "\n" for row in rows), encoding="utf-8")
    return path


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _public_key_b64(private_key: Ed25519PrivateKey) -> str:
    return base64.b64encode(
        private_key.public_key().public_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PublicFormat.Raw,
        )
    ).decode("ascii")


def _signed_receipt(payload: dict[str, str], private_key: Ed25519PrivateKey) -> dict[str, str]:
    message = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return {
        **payload,
        "signature_b64": base64.b64encode(private_key.sign(message)).decode("ascii"),
    }


def _candidate() -> dict:
    return {
        "schema_version": "multimodal-evidence-candidate/v1",
        "candidate_status": "AI_CANDIDATE",
        "candidate_id": "candidate-001",
        "document_id": "dude@pin:sample.pdf",
        "page_id": "dude@pin:sample.pdf:p0",
        "element_id": "dude@pin:sample.pdf:p0:e1",
        "bbox": [100.0, 200.0, 300.0, 400.0],
        "coordinate_system": "page_1000_xyxy",
        "page_image_sha256": "a" * 64,
        "source": {
            "source_id": "dude",
            "source_revision": "pin",
            "license_spdx": "CC-BY-4.0",
        },
        "mineru": {
            "version": "3.4.4",
            "ocr_mode": "explicit",
            "content_sha256": "b" * 64,
            "middle_sha256": "c" * 64,
            "receipt_sha256": "d" * 64,
        },
    }


def _inputs(tmp_path: Path) -> dict[str, object]:
    candidates = _write_jsonl(tmp_path / "candidates.jsonl", [_candidate()])
    decisions = _write_jsonl(
        tmp_path / "decisions.jsonl",
        [{
            "candidate_id": "candidate-001",
            "review_decision": "ACCEPT",
            "corrected_bbox": None,
            "review_note": "The page element is readable and accurately bounded.",
            "reviewed_at": "2026-08-14T00:00:00Z",
            "reviewer_id": "evidence-reviewer",
        }],
    )
    evidence_key = Ed25519PrivateKey.generate()
    evidence_key_id = "evidence-reviewer-key-1"
    evidence_receipt = _write_json(
        tmp_path / "evidence-receipt.json",
        _signed_receipt(
            {
                "candidates_sha256": _sha256(candidates),
                "decisions_sha256": _sha256(decisions),
                "reviewer_key_id": evidence_key_id,
            },
            evidence_key,
        ),
    )
    links = _write_jsonl(
        tmp_path / "query-links.jsonl",
        [{
            "query_id": "dude-q001",
            "query": "What grade did the person receive out of 100?",
            "candidate_id": "candidate-001",
            "review_note": "The selected element contains the requested grade.",
            "reviewed_at": "2026-08-14T00:01:00Z",
            "reviewer_id": "qrel-reviewer",
        }],
    )
    allowlist = _write_json(
        tmp_path / "license-allowlist.json",
        {
            "schema_version": "multimodal-pdf-license-allowlist/v1",
            "documents": [{
                "document_id": "dude@pin:sample.pdf",
                "source_pdf_sha256": "e" * 64,
                "license_spdx": "CC-BY-4.0",
                "redistribution_permitted": True,
                "evidence_url": "https://example.test/license",
            }],
        },
    )
    ocr_receipt = _write_json(
        tmp_path / "ocr-receipt.json",
        {
            "schema_version": "mineru-explicit-ocr-receipt/v1",
            "ocr_mode": "explicit",
            "exit_code": 0,
            "input_pdf_sha256": "e" * 64,
            "content_sha256": "b" * 64,
            "middle_sha256": "c" * 64,
        },
    )
    candidate = json.loads(candidates.read_text(encoding="utf-8"))
    candidate["mineru"]["receipt_sha256"] = _sha256(ocr_receipt)
    _write_jsonl(candidates, [candidate])
    evidence_receipt = _write_json(
        tmp_path / "evidence-receipt.json",
        _signed_receipt(
            {
                "candidates_sha256": _sha256(candidates),
                "decisions_sha256": _sha256(decisions),
                "reviewer_key_id": evidence_key_id,
            },
            evidence_key,
        ),
    )
    link_key = Ed25519PrivateKey.generate()
    link_key_id = "qrel-reviewer-key-1"
    link_receipt = _write_json(
        tmp_path / "query-links-receipt.json",
        _signed_receipt(
            {
                "candidates_sha256": _sha256(candidates),
                "decisions_sha256": _sha256(decisions),
                "links_sha256": _sha256(links),
                "license_allowlist_sha256": _sha256(allowlist),
                "reviewer_key_id": link_key_id,
            },
            link_key,
        ),
    )
    return {
        "candidates": candidates,
        "decisions": decisions,
        "evidence_receipt": evidence_receipt,
        "evidence_key": _public_key_b64(evidence_key),
        "evidence_key_id": evidence_key_id,
        "links": links,
        "link_receipt": link_receipt,
        "link_key": _public_key_b64(link_key),
        "link_private_key": link_key,
        "link_key_id": link_key_id,
        "allowlist": allowlist,
        "ocr_receipt": ocr_receipt,
    }


def test_materialize_page_qrels_requires_signed_human_evidence_link_and_pdf_allowlist(tmp_path: Path) -> None:
    from orchestrator.eval.multimodal_page_qrels import materialize_human_reviewed_page_qrels

    inputs = _inputs(tmp_path)
    out_dir = materialize_human_reviewed_page_qrels(
        candidates_path=inputs["candidates"],
        decisions_path=inputs["decisions"],
        evidence_receipt_path=inputs["evidence_receipt"],
        evidence_reviewer_public_key_b64=inputs["evidence_key"],
        expected_evidence_reviewer_key_id=inputs["evidence_key_id"],
        query_links_path=inputs["links"],
        query_links_receipt_path=inputs["link_receipt"],
        link_reviewer_public_key_b64=inputs["link_key"],
        expected_link_reviewer_key_id=inputs["link_key_id"],
        license_allowlist_path=inputs["allowlist"],
        ocr_receipts_by_document={"dude@pin:sample.pdf": inputs["ocr_receipt"]},
        out_dir=tmp_path / "page-qrels",
    )

    row = json.loads((out_dir / "qrels.jsonl").read_text(encoding="utf-8"))
    manifest = json.loads((out_dir / "manifest.json").read_text(encoding="utf-8"))
    assert row == {
        "bbox": [100.0, 200.0, 300.0, 400.0],
        "candidate_id": "candidate-001",
        "coordinate_system": "page_1000_xyxy",
        "document_id": "dude@pin:sample.pdf",
        "element_id": "dude@pin:sample.pdf:p0:e1",
        "page_id": "dude@pin:sample.pdf:p0",
        "query": "What grade did the person receive out of 100?",
        "query_id": "dude-q001",
        "relevance": 1,
        "review_status": "HUMAN_REVIEWED",
    }
    assert manifest["status"] == "HUMAN_REVIEWED_PAGE_QRELS_NOT_RELEASED"
    assert manifest["scoreable"] is False
    assert manifest["qrels"] is True
    assert manifest["qrels_count"] == 1
    assert manifest["license_allowlist_sha256"] == _sha256(inputs["allowlist"])
    assert manifest["evidence_receipt_sha256"] == _sha256(inputs["evidence_receipt"])
    assert manifest["query_links_receipt_sha256"] == _sha256(inputs["link_receipt"])
    assert manifest["mineru_ocr_receipt_sha256_by_document"] == {
        "dude@pin:sample.pdf": _sha256(inputs["ocr_receipt"])
    }


def test_materialize_page_qrels_rejects_tampered_ocr_receipt(tmp_path: Path) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        HumanPageQrelsError,
        materialize_human_reviewed_page_qrels,
    )

    inputs = _inputs(tmp_path)
    receipt = json.loads(inputs["ocr_receipt"].read_text(encoding="utf-8"))
    receipt["input_pdf_sha256"] = "f" * 64
    _write_json(inputs["ocr_receipt"], receipt)

    with pytest.raises(HumanPageQrelsError, match="MINERU_OCR_RECEIPT_INVALID"):
        materialize_human_reviewed_page_qrels(
            candidates_path=inputs["candidates"],
            decisions_path=inputs["decisions"],
            evidence_receipt_path=inputs["evidence_receipt"],
            evidence_reviewer_public_key_b64=inputs["evidence_key"],
            expected_evidence_reviewer_key_id=inputs["evidence_key_id"],
            query_links_path=inputs["links"],
            query_links_receipt_path=inputs["link_receipt"],
            link_reviewer_public_key_b64=inputs["link_key"],
            expected_link_reviewer_key_id=inputs["link_key_id"],
            license_allowlist_path=inputs["allowlist"],
            ocr_receipts_by_document={"dude@pin:sample.pdf": inputs["ocr_receipt"]},
            out_dir=tmp_path / "page-qrels",
        )


def test_materialize_page_qrels_rejects_document_missing_from_signed_allowlist(tmp_path: Path) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        HumanPageQrelsError,
        materialize_human_reviewed_page_qrels,
    )

    inputs = _inputs(tmp_path)
    _write_json(
        inputs["allowlist"],
        {"schema_version": "multimodal-pdf-license-allowlist/v1", "documents": []},
    )
    _write_json(
        inputs["link_receipt"],
        _signed_receipt(
            {
                "candidates_sha256": _sha256(inputs["candidates"]),
                "decisions_sha256": _sha256(inputs["decisions"]),
                "links_sha256": _sha256(inputs["links"]),
                "license_allowlist_sha256": _sha256(inputs["allowlist"]),
                "reviewer_key_id": inputs["link_key_id"],
            },
            inputs["link_private_key"],
        ),
    )

    with pytest.raises(HumanPageQrelsError, match="PDF_LICENSE_NOT_ALLOWLISTED"):
        materialize_human_reviewed_page_qrels(
            candidates_path=inputs["candidates"],
            decisions_path=inputs["decisions"],
            evidence_receipt_path=inputs["evidence_receipt"],
            evidence_reviewer_public_key_b64=inputs["evidence_key"],
            expected_evidence_reviewer_key_id=inputs["evidence_key_id"],
            query_links_path=inputs["links"],
            query_links_receipt_path=inputs["link_receipt"],
            link_reviewer_public_key_b64=inputs["link_key"],
            expected_link_reviewer_key_id=inputs["link_key_id"],
            license_allowlist_path=inputs["allowlist"],
            ocr_receipts_by_document={"dude@pin:sample.pdf": inputs["ocr_receipt"]},
            out_dir=tmp_path / "page-qrels",
        )
