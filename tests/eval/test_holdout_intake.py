"""A new holdout must be sealed from net-new external inputs, never dev data."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path

import pytest

from orchestrator.eval.holdout import (
    HoldoutIntakeError,
    load_verified_holdout_qrels,
    seal_holdout,
    verify_sealed_holdout,
)


def _write_jsonl(path: Path, rows: list[dict]) -> Path:
    path.write_text("".join(json.dumps(row) + "\n" for row in rows), encoding="utf-8")
    return path


def _inputs(tmp_path: Path) -> tuple[Path, Path, Path]:
    questions = _write_jsonl(
        tmp_path / "questions.jsonl",
        [{
            "query_id": "holdout-001",
            "query": "A question authored outside the dev set.",
            "source_id": "go",
            "language": "en",
            "query_type": "concept",
        }],
    )
    qrels = _write_jsonl(
        tmp_path / "qrels.jsonl",
        [{
            "query_id": "holdout-001",
            "document_id": "go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/holdout-only.html",
            "section_path": ["Types", "Interface types"],
            "relevance": 1,
            "source_id": "go",
            "language": "en",
            "query_type": "concept",
        }],
    )
    attestation = tmp_path / "attestation.json"
    attestation.write_text(
        json.dumps(
            {
                "attestation_type": "HUMAN_NET_NEW",
                "author_role": "independent_dataset_steward",
                "created_utc": "2026-08-14T00:00:00Z",
                "statement": "These questions were authored outside the agent-visible dev corpus and have not been used for tuning or scoring.",
            }
        ),
        encoding="utf-8",
    )
    return questions, qrels, attestation


def test_seal_holdout_emits_agent_safe_question_view_and_hash_bound_manifest(tmp_path: Path) -> None:
    questions, qrels, attestation = _inputs(tmp_path)

    manifest_path = seal_holdout(questions, qrels, attestation, tmp_path / "sealed")

    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    assert manifest["schema_version"] == "techdocs-holdout-seal/v1"
    assert manifest["status"] == "SEALED_NOT_SCORED"
    assert manifest["semantic_unseen"] == "HUMAN_ATTESTED_NOT_MACHINE_PROVABLE"
    assert manifest["question_count"] == 1
    assert manifest["qrel_count"] == 1
    assert manifest["questions_sha256"] == hashlib.sha256(questions.read_bytes()).hexdigest()
    assert manifest["qrels_sha256"] == hashlib.sha256(qrels.read_bytes()).hexdigest()
    assert not (manifest_path.parent / "qrels.jsonl").exists()
    assert json.loads((manifest_path.parent / "evaluation-questions.jsonl").read_text(encoding="utf-8"))[
        "query_id"
    ] == "holdout-001"

    verified = verify_sealed_holdout(manifest_path, questions, qrels, attestation)
    assert verified["qrels_sha256"] == manifest["qrels_sha256"]


def test_seal_holdout_rejects_a_query_id_seen_in_permanent_dev(tmp_path: Path) -> None:
    questions, qrels, attestation = _inputs(tmp_path)
    question = json.loads(questions.read_text(encoding="utf-8"))
    question["query_id"] = "go-q001"
    questions.write_text(json.dumps(question) + "\n", encoding="utf-8")
    qrel = json.loads(qrels.read_text(encoding="utf-8"))
    qrel["query_id"] = "go-q001"
    qrels.write_text(json.dumps(qrel) + "\n", encoding="utf-8")

    with pytest.raises(HoldoutIntakeError, match="DEV_QID_LEAKED_TO_HOLDOUT"):
        seal_holdout(questions, qrels, attestation, tmp_path / "sealed")


def test_seal_holdout_rejects_a_dev_question_text_or_dev_referenced_document(tmp_path: Path) -> None:
    questions, qrels, attestation = _inputs(tmp_path)
    question = json.loads(questions.read_text(encoding="utf-8"))
    question["query"] = "What is an interface type in Go, and what does it mean for a type to satisfy an interface?"
    questions.write_text(json.dumps(question) + "\n", encoding="utf-8")

    with pytest.raises(HoldoutIntakeError, match="DEV_QUERY_TEXT_LEAKED_TO_HOLDOUT"):
        seal_holdout(questions, qrels, attestation, tmp_path / "sealed")

    question["query"] = "An otherwise new question."
    questions.write_text(json.dumps(question) + "\n", encoding="utf-8")
    qrel = json.loads(qrels.read_text(encoding="utf-8"))
    qrel["document_id"] = "go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/go_spec.html"
    qrels.write_text(json.dumps(qrel) + "\n", encoding="utf-8")
    with pytest.raises(HoldoutIntakeError, match="DEV_DOCUMENT_LEAKED_TO_HOLDOUT"):
        seal_holdout(questions, qrels, attestation, tmp_path / "sealed")


def test_verify_sealed_holdout_rejects_tampered_qrels_after_sealing(tmp_path: Path) -> None:
    questions, qrels, attestation = _inputs(tmp_path)
    manifest_path = seal_holdout(questions, qrels, attestation, tmp_path / "sealed")
    qrels.write_text(qrels.read_text(encoding="utf-8").replace('"relevance": 1', '"relevance": 0'), encoding="utf-8")

    with pytest.raises(HoldoutIntakeError, match="HOLDOUT_QRELS_HASH_MISMATCH"):
        verify_sealed_holdout(manifest_path, questions, qrels, attestation)


def test_seal_holdout_rejects_answer_bearing_question_rows(tmp_path: Path) -> None:
    questions, qrels, attestation = _inputs(tmp_path)
    question = json.loads(questions.read_text(encoding="utf-8"))
    question["relevance"] = 1
    questions.write_text(json.dumps(question) + "\n", encoding="utf-8")

    with pytest.raises(HoldoutIntakeError, match="HOLDOUT_QUESTION_FIELDS_INVALID"):
        seal_holdout(questions, qrels, attestation, tmp_path / "sealed")


def test_verify_sealed_holdout_rejects_tampered_agent_question_view(tmp_path: Path) -> None:
    questions, qrels, attestation = _inputs(tmp_path)
    manifest_path = seal_holdout(questions, qrels, attestation, tmp_path / "sealed")
    view = manifest_path.parent / "evaluation-questions.jsonl"
    view.write_text('{"query_id":"holdout-001","query":"leaked label","source_id":"go","language":"en","query_type":"concept","relevance":1}\n', encoding="utf-8")

    with pytest.raises(HoldoutIntakeError, match="HOLDOUT_EVALUATION_VIEW_HASH_MISMATCH"):
        verify_sealed_holdout(manifest_path, questions, qrels, attestation)


def test_privileged_holdout_loader_revalidates_before_releasing_qrels(tmp_path: Path) -> None:
    questions, qrels, attestation = _inputs(tmp_path)
    manifest_path = seal_holdout(questions, qrels, attestation, tmp_path / "sealed")
    rows = load_verified_holdout_qrels(manifest_path, questions, qrels, attestation)
    assert rows[0]["query_id"] == "holdout-001"

    qrels.write_text(qrels.read_text(encoding="utf-8").replace('"relevance": 1', '"relevance": 0'), encoding="utf-8")
    with pytest.raises(HoldoutIntakeError, match="HOLDOUT_QRELS_HASH_MISMATCH"):
        load_verified_holdout_qrels(manifest_path, questions, qrels, attestation)


def test_seal_holdout_rejects_partial_label_coverage(tmp_path: Path) -> None:
    questions, qrels, attestation = _inputs(tmp_path)
    rows = [json.loads(qrels.read_text(encoding="utf-8"))]
    rows.append({**rows[0], "query_id": "unknown-holdout-qid"})
    _write_jsonl(qrels, rows)

    with pytest.raises(HoldoutIntakeError, match="HOLDOUT_QREL_QUERY_MISMATCH"):
        seal_holdout(questions, qrels, attestation, tmp_path / "sealed")
