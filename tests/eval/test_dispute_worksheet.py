"""The arbitration worksheet must not leak the answer it asks for.

157 of 180 techdocs qrels are DISPUTED and need human arbitration.  The rows
carry more answer-bearing material than ``relevance`` alone:
``pass_a_verdict`` and ``pass_b_verdict`` each contain ``relevance_correct``,
and ``dispute_reason`` / ``review_confidence`` reveal how the earlier passes
landed.  Exporting any of them anchors the arbitration on the prior AI
judgement, which is the leakage the blind-review firewall exists to stop.

These tests pin: nothing answer-bearing survives the export, the reviewer does
receive real evidence (query text plus the document's actual sections), the
order is deterministic, and a missing index refuses rather than emitting a
worksheet full of blank evidence.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from orchestrator.eval import dispute_worksheet as dw


def _qrel(qid: str, *, status="DISPUTED", relevance=1.0, source="docker"):
    return {
        "query_id": qid,
        "document_id": f"{source}@abc:docs/{qid}.md",
        "section_path": ["Claimed Heading"],
        "relevance": relevance,
        "source_id": source,
        "language": "en",
        "query_type": "concept",
        "pass_a_verdict": {"relevance_correct": False, "confidence": 0.0},
        "pass_b_verdict": {"relevance_correct": True, "confidence": 0.98},
        "review_prompt_hash": "deadbeefdeadbeef",
        "review_evidence": "es:idx:doc|es:idx:doc",
        "review_timestamp": "2026-08-09T03:45:29+00:00",
        "review_status": status,
        "review_confidence": 0.0,
        "dispute_reason": "pass_failed:parse_failed",
    }


@pytest.fixture()
def corpus(tmp_path: Path):
    qrels = tmp_path / "qrels.jsonl"
    rows = [_qrel(f"q{i:03d}") for i in range(5)]
    rows.append(_qrel("ok001", status="AI_REVIEWED"))
    qrels.write_text(
        "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in rows),
        encoding="utf-8",
    )
    queries = tmp_path / "queries.jsonl"
    queries.write_text(
        "".join(
            json.dumps({"query_id": f"q{i:03d}", "query": f"question {i}?"}) + "\n"
            for i in range(5)
        )
        + json.dumps({"query_id": "ok001", "query": "arbitrated question?"}) + "\n",
        encoding="utf-8",
    )
    return qrels, queries


def _build(corpus, tmp_path: Path, monkeypatch, **kwargs):
    qrels, queries = corpus
    monkeypatch.setattr(
        dw,
        "fetch_document_sections",
        lambda document_id, **kw: [
            {
                "chunk_id": 0,
                "section_path": ["Explanation"],
                "text": f"real text for {document_id}",
                "truncated": False,
            }
        ],
    )
    out = tmp_path / "worksheet.jsonl"
    return dw.build_worksheet(qrels, queries, out, **kwargs)


def _rows(path: Path) -> list[dict]:
    return [
        json.loads(line)
        for line in path.read_text(encoding="utf-8").splitlines()
        if line.strip()
    ]


# ---------------------------------------------------------------------------
# Leakage
# ---------------------------------------------------------------------------


def test_no_answer_bearing_field_survives(corpus, tmp_path: Path, monkeypatch) -> None:
    path, count = _build(corpus, tmp_path, monkeypatch)
    assert count == 5, "only DISPUTED rows belong in the arbitration worksheet"
    for row in _rows(path):
        for field in dw.ANSWER_FIELDS:
            assert field not in row, f"{field} leaked into the worksheet"


def test_verdict_dicts_are_withheld_because_they_carry_the_label(
    corpus, tmp_path: Path, monkeypatch
) -> None:
    """pass_a/pass_b contain relevance_correct — the label in disguise."""
    path, _ = _build(corpus, tmp_path, monkeypatch)
    blob = path.read_text(encoding="utf-8")
    assert "relevance_correct" not in blob
    assert "pass_a_verdict" not in blob
    assert "pass_b_verdict" not in blob
    assert "parse_failed" not in blob, "dispute_reason reveals the prior outcome"


def test_arbitrated_rows_are_excluded(corpus, tmp_path: Path, monkeypatch) -> None:
    path, _ = _build(corpus, tmp_path, monkeypatch)
    assert all(r["query_id"] != "ok001" for r in _rows(path))


# ---------------------------------------------------------------------------
# The reviewer must receive real evidence
# ---------------------------------------------------------------------------


def test_reviewer_gets_query_text_and_real_sections(
    corpus, tmp_path: Path, monkeypatch
) -> None:
    path, _ = _build(corpus, tmp_path, monkeypatch)
    for row in _rows(path):
        assert row["query"], "a reviewer cannot judge relevance without the query"
        sections = row["document_sections"]
        assert sections and sections[0]["text"], "evidence must carry document text"


def test_claimed_section_is_relabelled_not_presented_as_fact(
    corpus, tmp_path: Path, monkeypatch
) -> None:
    """178/180 claimed sections do not exist; the field must say so."""
    path, _ = _build(corpus, tmp_path, monkeypatch)
    for row in _rows(path):
        assert "section_path" not in row
        assert row["section_path_claimed"] == ["Claimed Heading"]
        assert "NOT found in the index" in row["section_path_note"]


def test_blank_review_fields_are_present_for_the_reviewer(
    corpus, tmp_path: Path, monkeypatch
) -> None:
    path, _ = _build(corpus, tmp_path, monkeypatch)
    for row in _rows(path):
        for field in dw.REVIEW_FIELDS:
            assert row[field] == "", f"{field} must be blank for the reviewer"


# ---------------------------------------------------------------------------
# Determinism and fail-closed evidence
# ---------------------------------------------------------------------------


def test_worksheet_order_is_deterministic(corpus, tmp_path: Path, monkeypatch) -> None:
    first, _ = _build(corpus, tmp_path / "a", monkeypatch)
    second, _ = _build(corpus, tmp_path / "b", monkeypatch)
    assert [r["row_hash"] for r in _rows(first)] == [
        r["row_hash"] for r in _rows(second)
    ]


def test_row_hash_is_stable_and_reviewer_scoped() -> None:
    a = dw.redact_hash("q1", "doc1", "reviewer-1")
    assert a == dw.redact_hash("q1", "doc1", "reviewer-1")
    assert a != dw.redact_hash("q1", "doc1", "reviewer-2")
    assert len(a) == 16


def test_unreachable_index_raises_instead_of_blank_evidence(tmp_path: Path) -> None:
    """A worksheet with silently-empty evidence sends review to a blank page."""
    with pytest.raises(RuntimeError, match="INDEX_UNREACHABLE"):
        dw.fetch_document_sections(
            "docker@abc:docs/x.md", base_url="http://127.0.0.1:1"
        )
