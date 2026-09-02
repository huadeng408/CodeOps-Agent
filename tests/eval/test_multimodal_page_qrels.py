"""Page-level Qrels must be chained to signed human evidence."""

from __future__ import annotations

import base64
import hashlib
import json
from pathlib import Path

import pytest
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey


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


def _signed_receipt(payload: dict[str, object], private_key: Ed25519PrivateKey) -> dict[str, object]:
    message = json.dumps(payload, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return {
        **payload,
        "signature_b64": base64.b64encode(private_key.sign(message)).decode("ascii"),
    }


def _candidate(index: int = 1) -> dict:
    return {
        "schema_version": "multimodal-evidence-candidate/v1",
        "candidate_status": "AI_CANDIDATE",
        "candidate_id": f"candidate-{index:03d}",
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


def _inputs(tmp_path: Path, count: int = 1) -> dict[str, object]:
    candidates = _write_jsonl(tmp_path / "candidates.jsonl", [_candidate(index) for index in range(1, count + 1)])
    decisions = _write_jsonl(
        tmp_path / "decisions.jsonl",
        [{
            "candidate_id": f"candidate-{index:03d}",
            "review_decision": "ACCEPT",
            "corrected_bbox": None,
            "review_note": "The page element is readable and accurately bounded.",
            "reviewed_at": "2026-08-14T00:00:00Z",
            "reviewer_id": "evidence-reviewer",
        } for index in range(1, count + 1)],
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
            "query_id": f"dude-q{index:03d}",
            "query": "What grade did the person receive out of 100?" if count == 1 else f"What grade did the person receive out of 100? ({index})",
            "candidate_id": f"candidate-{index:03d}",
            "review_note": "The selected element contains the requested grade.",
            "reviewed_at": "2026-08-14T00:01:00Z",
            "reviewer_id": "qrel-reviewer",
        } for index in range(1, count + 1)],
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
    candidate_rows = [json.loads(line) for line in candidates.read_text(encoding="utf-8").splitlines()]
    for candidate in candidate_rows:
        candidate["mineru"]["receipt_sha256"] = _sha256(ocr_receipt)
    _write_jsonl(candidates, candidate_rows)
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
    from orchestrator.eval.multimodal_page_qrels import (
        materialize_human_reviewed_page_qrels,
    )

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


def test_materialize_page_qrels_uses_one_snapshot_for_signed_candidates(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        materialize_human_reviewed_page_qrels,
    )

    inputs = _inputs(tmp_path)
    candidates = inputs["candidates"]
    original_bytes = candidates.read_bytes()
    tampered_rows = [
        json.loads(line) for line in original_bytes.decode("utf-8").splitlines()
    ]
    tampered_rows[0]["bbox"] = [1.0, 2.0, 3.0, 4.0]
    tampered_bytes = "".join(
        json.dumps(row, sort_keys=True) + "\n" for row in tampered_rows
    ).encode("utf-8")
    original_read_bytes = Path.read_bytes
    replaced = False

    def replace_after_first_read(self: Path, *args: object, **kwargs: object) -> bytes:
        nonlocal replaced
        value = original_read_bytes(self, *args, **kwargs)
        if self == candidates and not replaced:
            replaced = True
            self.write_bytes(tampered_bytes)
        return value

    monkeypatch.setattr(Path, "read_bytes", replace_after_first_read)

    out_dir = materialize_human_reviewed_page_qrels(
        **_materialization_kwargs(inputs), out_dir=tmp_path / "page-qrels"
    )

    row = json.loads((out_dir / "qrels.jsonl").read_text(encoding="utf-8"))
    manifest = json.loads((out_dir / "manifest.json").read_text(encoding="utf-8"))
    assert replaced is True
    assert row["bbox"] == [100.0, 200.0, 300.0, 400.0]
    assert manifest["candidates_sha256"] == hashlib.sha256(original_bytes).hexdigest()


def test_materialize_page_qrels_binds_verified_receipt_snapshot(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        materialize_human_reviewed_page_qrels,
    )

    inputs = _inputs(tmp_path)
    evidence_receipt = inputs["evidence_receipt"]
    original_bytes = evidence_receipt.read_bytes()
    replacement_bytes = b'{"tampered":true}\n'
    original_read_text = Path.read_text
    replaced = False

    def replace_after_first_read(self: Path, *args: object, **kwargs: object) -> str:
        nonlocal replaced
        value = original_read_text(self, *args, **kwargs)
        if self == evidence_receipt and not replaced:
            replaced = True
            self.write_bytes(replacement_bytes)
        return value

    monkeypatch.setattr(Path, "read_text", replace_after_first_read)

    out_dir = materialize_human_reviewed_page_qrels(
        **_materialization_kwargs(inputs), out_dir=tmp_path / "page-qrels"
    )

    manifest = json.loads((out_dir / "manifest.json").read_text(encoding="utf-8"))
    assert replaced is False
    assert manifest["evidence_receipt_sha256"] == hashlib.sha256(
        original_bytes
    ).hexdigest()


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


def _materialization_kwargs(inputs: dict[str, object]) -> dict[str, object]:
    return {
        "candidates_path": inputs["candidates"],
        "decisions_path": inputs["decisions"],
        "evidence_receipt_path": inputs["evidence_receipt"],
        "evidence_reviewer_public_key_b64": inputs["evidence_key"],
        "expected_evidence_reviewer_key_id": inputs["evidence_key_id"],
        "query_links_path": inputs["links"],
        "query_links_receipt_path": inputs["link_receipt"],
        "link_reviewer_public_key_b64": inputs["link_key"],
        "expected_link_reviewer_key_id": inputs["link_key_id"],
        "license_allowlist_path": inputs["allowlist"],
        "ocr_receipts_by_document": {"dude@pin:sample.pdf": inputs["ocr_receipt"]},
    }


def _write_release_inputs(page_qrels_dir: Path, count: int) -> tuple[dict[str, object], Path, Path, Path, Path, Path, str, str]:
    from orchestrator.eval.multimodal_page_qrels import (
        materialize_human_reviewed_page_qrels,
    )

    inputs = _inputs(page_qrels_dir.parent, count=count)
    materialize_human_reviewed_page_qrels(**_materialization_kwargs(inputs), out_dir=page_qrels_dir)
    qrels = page_qrels_dir / "qrels.jsonl"
    release_key = Ed25519PrivateKey.generate()
    release_key_id = "release-workflow-key-1"
    contamination_report = (page_qrels_dir / "contamination-report.jsonl")
    contamination_report.write_text('{"layer":"exact","verdict":"clean"}\n', encoding="utf-8")
    scorer_predictions = page_qrels_dir / "scorer-predictions.jsonl"
    scorer_predictions.write_text('{"query_id":"dude-q001","page_id":"dude@pin:sample.pdf:p0"}\n', encoding="utf-8")
    split = _write_json(
        page_qrels_dir / "split-freeze.json",
        _signed_receipt({
            "schema_version": "multimodal-qrels-split-freeze/v1",
            "qrels_sha256": _sha256(qrels),
            "split": "test",
            "split_frozen": True,
            "reviewer_key_id": release_key_id,
        }, release_key),
    )
    contamination = _write_json(
        page_qrels_dir / "contamination.json",
        _signed_receipt({
            "schema_version": "multimodal-qrels-contamination/v1",
            "qrels_sha256": _sha256(qrels),
            "report_sha256": _sha256(contamination_report),
            "verdict": "CLEAN",
            "layers_completed": ["exact", "containment", "minhash", "embedding"],
            "reviewer_key_id": release_key_id,
        }, release_key),
    )
    scorer = _write_json(
        page_qrels_dir / "scorer.json",
        _signed_receipt({
            "schema_version": "multimodal-qrels-independent-scorer/v1",
            "qrels_sha256": _sha256(qrels),
            "scorer_id": "official-vidore",
            "predictions_sha256": _sha256(scorer_predictions),
            "independent": True,
            "reviewer_key_id": release_key_id,
        }, release_key),
    )
    return inputs, split, contamination, contamination_report, scorer, scorer_predictions, _public_key_b64(release_key), release_key_id


def _install_test_trust_root(
    monkeypatch: pytest.MonkeyPatch,
    inputs: dict[str, object],
    release_key: str,
    release_key_id: str,
) -> None:
    import orchestrator.eval.multimodal_page_qrels as page_qrels

    monkeypatch.delenv(page_qrels.TRUST_ROOT_ENV, raising=False)
    monkeypatch.setattr(
        page_qrels,
        "TRUSTED_PAGE_QRELS_SIGNERS",
        {
            "evidence": {str(inputs["evidence_key_id"]): str(inputs["evidence_key"])},
            "query_link": {str(inputs["link_key_id"]): str(inputs["link_key"])},
            "release": {release_key_id: release_key},
        },
    )


def test_trust_root_loader_validates_roles_and_allows_key_rotation(tmp_path: Path) -> None:
    from orchestrator.eval.multimodal_page_qrels import load_trusted_page_qrels_signers

    public_key = base64.b64encode(b"k" * 32).decode("ascii")
    path = _write_json(
        tmp_path / "trust-root.json",
        {
            "schema_version": "page-qrels-trust-root/v1",
            "signers": {
                "evidence": {"evidence-v1": public_key},
                "query_link": {"link-v1": public_key, "link-v2": public_key},
                "release": {"release-v1": public_key},
            },
        },
    )

    loaded = load_trusted_page_qrels_signers(path)

    assert loaded["query_link"]["link-v2"] == public_key
    assert set(loaded) == {"evidence", "query_link", "release"}

    invalid = _write_json(
        tmp_path / "invalid-trust-root.json",
        {
            "schema_version": "page-qrels-trust-root/v1",
            "signers": {"evidence": {"bad": "not-base64"}},
        },
    )
    with pytest.raises(ValueError, match="PAGE_QRELS_TRUST_ROOT_INVALID"):
        load_trusted_page_qrels_signers(invalid)


def test_configured_trust_root_uses_one_byte_snapshot_for_hash_and_keys(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    import orchestrator.eval.multimodal_page_qrels as page_qrels

    initial_key = base64.b64encode(b"i" * 32).decode("ascii")
    replacement_key = base64.b64encode(b"r" * 32).decode("ascii")
    path = _write_json(
        tmp_path / "trust-root.json",
        {
            "schema_version": "page-qrels-trust-root/v1",
            "signers": {
                "evidence": {"evidence-v1": initial_key},
                "query_link": {"link-v1": initial_key},
                "release": {"release-v1": initial_key},
            },
        },
    )
    initial_bytes = path.read_bytes()
    original_read_text = Path.read_text
    original_read_bytes = Path.read_bytes
    read_text_calls = 0
    read_bytes_calls = 0

    def replace_after_text_read(self: Path, *args: object, **kwargs: object) -> str:
        nonlocal read_text_calls
        if self == path:
            read_text_calls += 1
            value = original_read_text(self, *args, **kwargs)
            path.write_text(
                json.dumps(
                    {
                        "schema_version": "page-qrels-trust-root/v1",
                        "signers": {
                            "evidence": {"evidence-v1": replacement_key},
                            "query_link": {"link-v1": replacement_key},
                            "release": {"release-v1": replacement_key},
                        },
                    },
                    sort_keys=True,
                )
                + "\n",
                encoding="utf-8",
            )
            return value
        return original_read_text(self, *args, **kwargs)

    def count_read_bytes(self: Path, *args: object, **kwargs: object) -> bytes:
        nonlocal read_bytes_calls
        if self == path:
            read_bytes_calls += 1
        return original_read_bytes(self, *args, **kwargs)

    monkeypatch.setenv(page_qrels.TRUST_ROOT_ENV, str(path))
    monkeypatch.setattr(Path, "read_text", replace_after_text_read)
    monkeypatch.setattr(Path, "read_bytes", count_read_bytes)

    loaded, digest = page_qrels._configured_trust_root()

    assert loaded["release"]["release-v1"] == initial_key
    assert digest == hashlib.sha256(initial_bytes).hexdigest()
    assert read_text_calls == 0
    assert read_bytes_calls == 1


def test_release_page_qrels_uses_external_trust_root_and_records_selected_signers(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    import orchestrator.eval.multimodal_page_qrels as page_qrels
    from orchestrator.eval.multimodal_page_qrels import (
        release_human_reviewed_page_qrels,
    )

    page_qrels_dir = tmp_path / "page-qrels"
    (
        inputs,
        split,
        contamination,
        contamination_report,
        scorer,
        scorer_predictions,
        release_key,
        release_key_id,
    ) = _write_release_inputs(page_qrels_dir, count=120)
    trust_root = _write_json(
        tmp_path / "trust-root.json",
        {
            "schema_version": "page-qrels-trust-root/v1",
            "signers": {
                "evidence": {str(inputs["evidence_key_id"]): str(inputs["evidence_key"])},
                "query_link": {str(inputs["link_key_id"]): str(inputs["link_key"])},
                "release": {release_key_id: release_key},
            },
        },
    )
    monkeypatch.setenv("CODE_AGENT_PAGE_QRELS_TRUST_ROOT", str(trust_root))
    monkeypatch.setattr(
        page_qrels,
        "TRUSTED_PAGE_QRELS_SIGNERS",
        {"evidence": {}, "query_link": {}, "release": {}},
    )

    released = release_human_reviewed_page_qrels(
        page_qrels_dir=page_qrels_dir,
        source_materialization_kwargs=_materialization_kwargs(inputs),
        split_freeze_receipt_path=split,
        contamination_receipt_path=contamination,
        contamination_report_path=contamination_report,
        independent_scorer_receipt_path=scorer,
        scorer_predictions_path=scorer_predictions,
        release_reviewer_public_key_b64=release_key,
        expected_release_reviewer_key_id=release_key_id,
        out_dir=tmp_path / "released",
    )

    manifest = json.loads((released / "manifest.json").read_text(encoding="utf-8"))
    assert manifest["trust_root_sha256"] == _sha256(trust_root)
    assert manifest["trusted_signer_key_ids"] == {
        "evidence": [str(inputs["evidence_key_id"])],
        "query_link": [str(inputs["link_key_id"])],
        "release": [release_key_id],
    }
    assert manifest["signer_key_ids"] == {
        "evidence": str(inputs["evidence_key_id"]),
        "query_link": str(inputs["link_key_id"]),
        "release": release_key_id,
    }


def test_release_page_qrels_rejects_less_than_120_unique_queries(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        HumanPageQrelsError,
        release_human_reviewed_page_qrels,
    )

    page_qrels_dir = tmp_path / "page-qrels"
    inputs, split, contamination, contamination_report, scorer, scorer_predictions, release_key, release_key_id = _write_release_inputs(page_qrels_dir, count=119)
    _install_test_trust_root(monkeypatch, inputs, release_key, release_key_id)

    with pytest.raises(HumanPageQrelsError, match="PAGE_QRELS_MINIMUM_QUERY_COUNT"):
        release_human_reviewed_page_qrels(
            page_qrels_dir=page_qrels_dir,
            source_materialization_kwargs=_materialization_kwargs(inputs),
            split_freeze_receipt_path=split,
            contamination_receipt_path=contamination,
            contamination_report_path=contamination_report,
            independent_scorer_receipt_path=scorer,
            scorer_predictions_path=scorer_predictions,
            release_reviewer_public_key_b64=release_key,
            expected_release_reviewer_key_id=release_key_id,
            out_dir=tmp_path / "released",
        )


def test_release_page_qrels_hash_binds_all_required_release_receipts(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        release_human_reviewed_page_qrels,
    )

    page_qrels_dir = tmp_path / "page-qrels"
    inputs, split, contamination, contamination_report, scorer, scorer_predictions, release_key, release_key_id = _write_release_inputs(page_qrels_dir, count=120)
    _install_test_trust_root(monkeypatch, inputs, release_key, release_key_id)

    released = release_human_reviewed_page_qrels(
        page_qrels_dir=page_qrels_dir,
        source_materialization_kwargs=_materialization_kwargs(inputs),
        split_freeze_receipt_path=split,
        contamination_receipt_path=contamination,
        contamination_report_path=contamination_report,
        independent_scorer_receipt_path=scorer,
        scorer_predictions_path=scorer_predictions,
        release_reviewer_public_key_b64=release_key,
        expected_release_reviewer_key_id=release_key_id,
        out_dir=tmp_path / "released",
    )

    manifest = json.loads((released / "manifest.json").read_text(encoding="utf-8"))
    assert manifest["status"] == "HUMAN_REVIEWED_PAGE_QRELS_RELEASED"
    assert manifest["scoreable"] is True
    assert manifest["qrels_count"] == 120
    assert manifest["split_freeze_receipt_sha256"] == _sha256(split)
    assert manifest["contamination_receipt_sha256"] == _sha256(contamination)
    assert manifest["independent_scorer_receipt_sha256"] == _sha256(scorer)
    assert len(manifest["trust_root_sha256"]) == 64
    assert manifest["trusted_signer_key_ids"]["release"] == [release_key_id]


def test_release_page_qrels_publishes_the_validated_qrels_byte_snapshot(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        release_human_reviewed_page_qrels,
    )

    page_qrels_dir = tmp_path / "page-qrels"
    inputs, split, contamination, contamination_report, scorer, scorer_predictions, release_key, release_key_id = _write_release_inputs(page_qrels_dir, count=120)
    _install_test_trust_root(monkeypatch, inputs, release_key, release_key_id)
    qrels_path = page_qrels_dir / "qrels.jsonl"
    validated_bytes = qrels_path.read_bytes()
    replacement_bytes = b'{"query_id":"tampered-after-validation"}\n'
    original_read_bytes = Path.read_bytes
    replaced = False

    def replace_source_after_first_read(self: Path, *args: object, **kwargs: object) -> bytes:
        nonlocal replaced
        value = original_read_bytes(self, *args, **kwargs)
        if self == qrels_path and not replaced:
            replaced = True
            self.write_bytes(replacement_bytes)
        return value

    monkeypatch.setattr(Path, "read_bytes", replace_source_after_first_read)

    released = release_human_reviewed_page_qrels(
        page_qrels_dir=page_qrels_dir,
        source_materialization_kwargs=_materialization_kwargs(inputs),
        split_freeze_receipt_path=split,
        contamination_receipt_path=contamination,
        contamination_report_path=contamination_report,
        independent_scorer_receipt_path=scorer,
        scorer_predictions_path=scorer_predictions,
        release_reviewer_public_key_b64=release_key,
        expected_release_reviewer_key_id=release_key_id,
        out_dir=tmp_path / "released",
    )

    released_bytes = (released / "qrels.jsonl").read_bytes()
    manifest = json.loads((released / "manifest.json").read_text(encoding="utf-8"))
    assert replaced is True
    assert qrels_path.read_bytes() == replacement_bytes
    assert released_bytes == validated_bytes
    assert manifest["qrels_sha256"] == hashlib.sha256(validated_bytes).hexdigest()


def test_release_page_qrels_binds_the_verified_receipt_byte_snapshot(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        release_human_reviewed_page_qrels,
    )

    page_qrels_dir = tmp_path / "page-qrels"
    inputs, split, contamination, contamination_report, scorer, scorer_predictions, release_key, release_key_id = _write_release_inputs(page_qrels_dir, count=120)
    _install_test_trust_root(monkeypatch, inputs, release_key, release_key_id)
    verified_bytes = split.read_bytes()
    replacement_bytes = b'{"tampered":true}\n'
    original_read_text = Path.read_text
    original_read_bytes = Path.read_bytes
    replaced = False

    def replace_receipt_after_read(self: Path, *args: object, **kwargs: object) -> str:
        nonlocal replaced
        value = original_read_text(self, *args, **kwargs)
        if self == split and not replaced:
            replaced = True
            self.write_bytes(replacement_bytes)
        return value

    def replace_receipt_after_read_bytes(
        self: Path, *args: object, **kwargs: object
    ) -> bytes:
        nonlocal replaced
        value = original_read_bytes(self, *args, **kwargs)
        if self == split and not replaced:
            replaced = True
            self.write_bytes(replacement_bytes)
        return value

    monkeypatch.setattr(Path, "read_text", replace_receipt_after_read)
    monkeypatch.setattr(Path, "read_bytes", replace_receipt_after_read_bytes)

    released = release_human_reviewed_page_qrels(
        page_qrels_dir=page_qrels_dir,
        source_materialization_kwargs=_materialization_kwargs(inputs),
        split_freeze_receipt_path=split,
        contamination_receipt_path=contamination,
        contamination_report_path=contamination_report,
        independent_scorer_receipt_path=scorer,
        scorer_predictions_path=scorer_predictions,
        release_reviewer_public_key_b64=release_key,
        expected_release_reviewer_key_id=release_key_id,
        out_dir=tmp_path / "released",
    )

    manifest = json.loads((released / "manifest.json").read_text(encoding="utf-8"))
    assert replaced is True
    assert split.read_bytes() == replacement_bytes
    assert manifest["split_freeze_receipt_sha256"] == hashlib.sha256(
        verified_bytes
    ).hexdigest()


def test_release_page_qrels_rejects_unconfigured_trust_root(tmp_path: Path) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        HumanPageQrelsError,
        release_human_reviewed_page_qrels,
    )

    page_qrels_dir = tmp_path / "page-qrels"
    inputs, split, contamination, contamination_report, scorer, scorer_predictions, release_key, release_key_id = _write_release_inputs(page_qrels_dir, count=120)

    with pytest.raises(HumanPageQrelsError, match="PAGE_QRELS_TRUST_ROOT_UNCONFIGURED"):
        release_human_reviewed_page_qrels(
            page_qrels_dir=page_qrels_dir,
            source_materialization_kwargs=_materialization_kwargs(inputs),
            split_freeze_receipt_path=split,
            contamination_receipt_path=contamination,
            contamination_report_path=contamination_report,
            independent_scorer_receipt_path=scorer,
            scorer_predictions_path=scorer_predictions,
            release_reviewer_public_key_b64=release_key,
            expected_release_reviewer_key_id=release_key_id,
            out_dir=tmp_path / "released",
        )


def test_trust_root_rejects_non_string_reviewer_key_without_leaking_type_error(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    import orchestrator.eval.multimodal_page_qrels as page_qrels

    monkeypatch.setattr(
        page_qrels,
        "TRUSTED_PAGE_QRELS_SIGNERS",
        {
            "evidence": {"evidence-v1": base64.b64encode(b"e" * 32).decode("ascii")},
            "query_link": {"link-v1": base64.b64encode(b"l" * 32).decode("ascii")},
            "release": {"release-v1": base64.b64encode(b"r" * 32).decode("ascii")},
        },
    )

    with pytest.raises(page_qrels.HumanPageQrelsError, match="PAGE_QRELS_TRUST_ROOT_MISMATCH"):
        page_qrels._require_configured_trusted_signer(
            role="release",
            trusted_signers=page_qrels.TRUSTED_PAGE_QRELS_SIGNERS,
            reviewer_public_key_b64=None,
            reviewer_key_id="release-v1",
        )


def test_release_page_qrels_requires_materialized_human_and_ocr_provenance(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        HumanPageQrelsError,
        release_human_reviewed_page_qrels,
    )

    page_qrels_dir = tmp_path / "page-qrels"
    inputs, split, contamination, contamination_report, scorer, scorer_predictions, release_key, release_key_id = _write_release_inputs(page_qrels_dir, count=120)
    _install_test_trust_root(monkeypatch, inputs, release_key, release_key_id)
    manifest_path = page_qrels_dir / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    del manifest["query_links_receipt_sha256"]
    _write_json(manifest_path, manifest)

    with pytest.raises(HumanPageQrelsError, match="PAGE_QRELS_SOURCE_INVALID"):
        release_human_reviewed_page_qrels(
            page_qrels_dir=page_qrels_dir,
            source_materialization_kwargs=_materialization_kwargs(inputs),
            split_freeze_receipt_path=split,
            contamination_receipt_path=contamination,
            contamination_report_path=contamination_report,
            independent_scorer_receipt_path=scorer,
            scorer_predictions_path=scorer_predictions,
            release_reviewer_public_key_b64=release_key,
            expected_release_reviewer_key_id=release_key_id,
            out_dir=tmp_path / "released",
        )


def test_release_page_qrels_rejects_malformed_contamination_layers(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        HumanPageQrelsError,
        release_human_reviewed_page_qrels,
    )

    page_qrels_dir = tmp_path / "page-qrels"
    inputs, split, contamination, contamination_report, scorer, scorer_predictions, release_key, release_key_id = _write_release_inputs(page_qrels_dir, count=120)
    _install_test_trust_root(monkeypatch, inputs, release_key, release_key_id)
    contamination_payload = json.loads(contamination.read_text(encoding="utf-8"))
    contamination_payload["layers_completed"] = 4
    _write_json(contamination, contamination_payload)

    with pytest.raises(HumanPageQrelsError, match="CONTAMINATION_RECEIPT_INVALID"):
        release_human_reviewed_page_qrels(
            page_qrels_dir=page_qrels_dir,
            source_materialization_kwargs=_materialization_kwargs(inputs),
            split_freeze_receipt_path=split,
            contamination_receipt_path=contamination,
            contamination_report_path=contamination_report,
            independent_scorer_receipt_path=scorer,
            scorer_predictions_path=scorer_predictions,
            release_reviewer_public_key_b64=release_key,
            expected_release_reviewer_key_id=release_key_id,
            out_dir=tmp_path / "released",
        )


def test_release_page_qrels_rejects_qrels_rewritten_after_materialization(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        HumanPageQrelsError,
        release_human_reviewed_page_qrels,
    )

    page_qrels_dir = tmp_path / "page-qrels"
    inputs, split, contamination, contamination_report, scorer, scorer_predictions, release_key, release_key_id = _write_release_inputs(page_qrels_dir, count=120)
    _install_test_trust_root(monkeypatch, inputs, release_key, release_key_id)
    qrels_path = page_qrels_dir / "qrels.jsonl"
    rows = [json.loads(line) for line in qrels_path.read_text(encoding="utf-8").splitlines()]
    rows[0]["query"] = "rewritten after signed materialization"
    _write_jsonl(qrels_path, rows)
    manifest_path = page_qrels_dir / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    manifest["qrels_sha256"] = _sha256(qrels_path)
    _write_json(manifest_path, manifest)

    with pytest.raises(HumanPageQrelsError, match="PAGE_QRELS_SOURCE_REBUILD_INVALID"):
        release_human_reviewed_page_qrels(
            page_qrels_dir=page_qrels_dir,
            source_materialization_kwargs=_materialization_kwargs(inputs),
            split_freeze_receipt_path=split,
            contamination_receipt_path=contamination,
            contamination_report_path=contamination_report,
            independent_scorer_receipt_path=scorer,
            scorer_predictions_path=scorer_predictions,
            release_reviewer_public_key_b64=release_key,
            expected_release_reviewer_key_id=release_key_id,
            out_dir=tmp_path / "released",
        )


def test_release_page_qrels_rejects_tampered_signed_contamination_receipt(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    from orchestrator.eval.multimodal_page_qrels import (
        HumanPageQrelsError,
        release_human_reviewed_page_qrels,
    )

    page_qrels_dir = tmp_path / "page-qrels"
    inputs, split, contamination, contamination_report, scorer, scorer_predictions, release_key, release_key_id = _write_release_inputs(page_qrels_dir, count=120)
    _install_test_trust_root(monkeypatch, inputs, release_key, release_key_id)
    receipt = json.loads(contamination.read_text(encoding="utf-8"))
    receipt["verdict"] = "CLEAN_BUT_TAMPERED"
    _write_json(contamination, receipt)

    with pytest.raises(HumanPageQrelsError, match="CONTAMINATION_RECEIPT_INVALID"):
        release_human_reviewed_page_qrels(
            page_qrels_dir=page_qrels_dir,
            source_materialization_kwargs=_materialization_kwargs(inputs),
            split_freeze_receipt_path=split,
            contamination_receipt_path=contamination,
            contamination_report_path=contamination_report,
            independent_scorer_receipt_path=scorer,
            scorer_predictions_path=scorer_predictions,
            release_reviewer_public_key_b64=release_key,
            expected_release_reviewer_key_id=release_key_id,
            out_dir=tmp_path / "released",
        )
