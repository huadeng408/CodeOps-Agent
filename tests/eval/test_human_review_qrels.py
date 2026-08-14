"""Promotion of a user-confirmed text review package is offline and immutable."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path

import pytest

from orchestrator.eval.human_review_qrels import HumanReviewPromotionError, promote_human_reviewed_qrels


REVIEW_FIELDS = (
    "verdict_relevant",
    "verdict_answerable",
    "verdict_language_correct",
    "verdict_query_type_correct",
    "verdict_evidence_sufficient",
    "reviewer_notes",
)


def _write_jsonl(path: Path, rows: list[dict]) -> None:
    path.write_text("".join(json.dumps(row) + "\n" for row in rows), encoding="utf-8")


def _review_row(*, relevant: str = "no") -> dict:
    return {
        "query_id": "go-q001",
        "document_id": "go@abc123:doc/go_spec.html",
        "source_id": "go",
        "language": "en",
        "query_type": "concept",
        "query": "What is Go?",
        "reviewer": "local-human-1",
        "row_hash": "row-001",
        "section_path_claimed": ["Introduction"],
        "document_sections": [{"chunk_id": 0, "text": "Go is a language."}],
        "verdict_relevant": relevant,
        "verdict_answerable": "yes",
        "verdict_language_correct": "yes",
        "verdict_query_type_correct": "yes",
        "verdict_evidence_sufficient": "yes",
        "reviewer_notes": "Checked against the supplied evidence.",
    }


def _source_declaration(path: Path, *, permission: str = "yes") -> None:
    path.write_text(
        json.dumps(
            {
                "artifact_type": "UNSUBMITTED_SOURCE_DECLARATION",
                "declaration": {
                    "source_location": "https://github.com/golang/go",
                    "source_revision": "abc123",
                    "license": "BSD-3-Clause",
                    "ocr_permission": permission,
                    "page_render_permission": permission,
                    "vectorization_permission": permission,
                    "internal_evaluation_permission": permission,
                    "public_display_permission": permission,
                },
            }
        ),
        encoding="utf-8",
    )


def _inputs(tmp_path: Path, *, permission: str = "yes", draft_hash: str | None = None) -> dict[str, Path]:
    original = _review_row(relevant="")
    for field in REVIEW_FIELDS:
        original[field] = ""
    original_path = tmp_path / "worksheet.orig.jsonl"
    filled_path = tmp_path / "worksheet.jsonl"
    _write_jsonl(original_path, [original])
    _write_jsonl(filled_path, [_review_row()])
    declared_hash = draft_hash or hashlib.sha256(original_path.read_bytes()).hexdigest()
    draft_path = tmp_path / "draft.json"
    draft_path.write_text(
        json.dumps(
            {
                "artifact_type": "UNSUBMITTED_REVIEW_DRAFT",
                "worksheet_sha256": declared_hash,
                "decisions": [{key: _review_row()[key] for key in ("row_hash", *REVIEW_FIELDS)}],
            }
        ),
        encoding="utf-8",
    )
    qrels_path = tmp_path / "arbitrated.jsonl"
    _write_jsonl(
        qrels_path,
        [
            {
                "query_id": "go-q001", "document_id": "go@abc123:doc/go_spec.html",
                "section_path": ["Introduction"], "relevance": 1,
                "source_id": "go", "language": "en", "query_type": "concept",
                "review_status": "DISPUTED",
            },
            {
                "query_id": "go-q002", "document_id": "go@abc123:doc/other.html",
                "section_path": [], "relevance": 1,
                "source_id": "go", "language": "en", "query_type": "concept",
                "review_status": "AI_REVIEWED",
            },
        ],
    )
    declaration = tmp_path / "source.go.json"
    _source_declaration(declaration, permission=permission)
    return {
        "qrels": qrels_path, "original": original_path, "filled": filled_path,
        "draft": draft_path, "declaration": declaration,
        "out": tmp_path / "human-reviewed.jsonl", "receipt": tmp_path / "receipt.json",
    }


def test_promote_writes_new_human_qrels_and_a_hash_bound_receipt(tmp_path: Path) -> None:
    paths = _inputs(tmp_path)
    receipt = promote_human_reviewed_qrels(
        paths["qrels"], paths["original"], paths["filled"], paths["draft"],
        [paths["declaration"]], paths["out"], paths["receipt"],
        promoted_at="2026-08-14T06:00:00+00:00",
    )

    rows = [json.loads(line) for line in paths["out"].read_text(encoding="utf-8").splitlines()]
    assert rows[0]["relevance"] == 0
    assert rows[0]["review_status"] == "HUMAN_REVIEWED"
    assert len(rows[0]["reviewer_hash"]) == 16
    assert rows[1]["review_status"] == "AI_REVIEWED"
    assert receipt["counts"] == {"human_reviewed": 1, "unchanged": 1}
    assert receipt["inputs"]["worksheet_original_sha256"] == hashlib.sha256(paths["original"].read_bytes()).hexdigest()
    assert receipt["outputs"]["qrels_sha256"] == hashlib.sha256(paths["out"].read_bytes()).hexdigest()


def test_promote_projects_ai_workflow_metadata_out_of_the_schema_valid_qrels(tmp_path: Path) -> None:
    paths = _inputs(tmp_path)
    rows = [json.loads(line) for line in paths["qrels"].read_text(encoding="utf-8").splitlines()]
    rows[0]["pass_a_verdict"] = {"relevance_correct": False}
    rows[0]["dispute_reason"] = "disagreement:relevance_correct"
    _write_jsonl(paths["qrels"], rows)

    promote_human_reviewed_qrels(
        paths["qrels"], paths["original"], paths["filled"], paths["draft"],
        [paths["declaration"]], paths["out"], paths["receipt"],
        promoted_at="2026-08-14T06:00:00+00:00",
    )

    promoted = json.loads(paths["out"].read_text(encoding="utf-8").splitlines()[0])
    assert "pass_a_verdict" not in promoted
    assert "dispute_reason" not in promoted


@pytest.mark.parametrize("kind", ["draft_hash", "source_permission"])
def test_promote_rejects_unbound_or_unlicensed_inputs_before_writing(tmp_path: Path, kind: str) -> None:
    kwargs = {"draft_hash": "0" * 64} if kind == "draft_hash" else {"permission": "no"}
    paths = _inputs(tmp_path, **kwargs)

    with pytest.raises(HumanReviewPromotionError):
        promote_human_reviewed_qrels(
            paths["qrels"], paths["original"], paths["filled"], paths["draft"],
            [paths["declaration"]], paths["out"], paths["receipt"],
            promoted_at="2026-08-14T06:00:00+00:00",
        )

    assert not paths["out"].exists()
    assert not paths["receipt"].exists()
