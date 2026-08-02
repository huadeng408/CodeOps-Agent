"""Retrieval qrels metrics tests (plan Task 1: qrels schema + deterministic metrics).

The metric runner lives in orchestrator/eval/metrics.py and scores runs against
stable document_id/section_path qrels without randomness or unstable chunk
ordinals.
"""

from __future__ import annotations

import json
import math

import pytest

from orchestrator.eval.metrics import score_run, validate_qrels


def _qrel(
    query_id: str = "q1",
    document_id: str = "doc-1",
    section: tuple[str, ...] = ("API",),
    relevance: int = 1,
    **extra: object,
) -> dict:
    record: dict = {
        "query_id": query_id,
        "document_id": document_id,
        "section_path": list(section),
        "relevance": relevance,
        "source_id": "go",
        "language": "en",
        "query_type": "concept",
    }
    record.update(extra)
    return record


def _hit(
    query_id: str = "q1",
    document_id: str = "doc-1",
    section: tuple[str, ...] = ("API",),
    rank: int = 1,
) -> dict:
    return {
        "query_id": query_id,
        "document_id": document_id,
        "section_path": list(section),
        "rank": rank,
    }


def test_plan_example_perfect_run_scores_1_0() -> None:
    """The plan's example: one graded qrel, perfect first-rank hit."""
    qrels = [_qrel(document_id="doc-1", section=("API",), relevance=2)]
    run = [_hit(document_id="doc-1", section=("API",), rank=1)]
    scores = score_run(qrels, run, ks=(5, 10))
    assert scores["recall@5"] == 1.0
    assert scores["recall@10"] == 1.0
    assert scores["mrr@10"] == 1.0
    assert scores["ndcg@10"] == 1.0


def test_prefix_section_path_match_counts_as_hit() -> None:
    """A run section_path that is a prefix of the qrel section_path is a hit."""
    qrels = [_qrel(section=("API", "Installation"), relevance=2)]
    run = [_hit(section=("API",), rank=1)]
    scores = score_run(qrels, run, ks=(5, 10))
    assert scores["recall@5"] == 1.0
    assert scores["mrr@10"] == 1.0
    assert scores["ndcg@10"] == 1.0


def test_run_section_path_must_be_a_prefix_of_qrel() -> None:
    """A deeper run path never matches a shallower qrel path."""
    qrels = [_qrel(section=("API",))]
    run = [_hit(section=("API", "Installation"), rank=1)]
    scores = score_run(qrels, run, ks=(5, 10))
    assert scores["recall@5"] == 0.0
    assert scores["mrr@10"] == 0.0
    assert scores["ndcg@10"] == 0.0


def test_multi_query_macro_average() -> None:
    qrels = [
        _qrel(query_id="q1", document_id="doc-1", section=("API",)),
        _qrel(query_id="q2", document_id="doc-2", section=("Setup",)),
    ]
    run = [
        _hit(query_id="q1", document_id="doc-1", section=("API",), rank=1),
        _hit(query_id="q2", document_id="doc-2", section=("Setup",), rank=6),
    ]
    scores = score_run(qrels, run, ks=(5, 10))
    assert scores["recall@5"] == 0.5
    assert scores["recall@10"] == 1.0
    assert scores["mrr@10"] == pytest.approx((1.0 + 1.0 / 6.0) / 2.0)
    assert scores["ndcg@10"] == pytest.approx((1.0 + 1.0 / math.log2(6.0)) / 2.0)


def test_metrics_are_deterministic_run_twice() -> None:
    """Same inputs => identical outputs, bit for bit."""
    qrels = [
        _qrel(query_id="q1", document_id="doc-a", section=("X",), relevance=3),
        _qrel(query_id="q1", document_id="doc-b", section=("Y",), relevance=1),
        _qrel(query_id="q1", document_id="doc-a", section=("X", "D"), relevance=2),
        _qrel(query_id="q2", document_id="doc-c", section=("Z",), relevance=2),
    ]
    run = [
        _hit(query_id="q1", document_id="doc-b", section=("Y",), rank=1),
        _hit(query_id="q1", document_id="doc-a", section=("X",), rank=2),
        _hit(query_id="q1", document_id="doc-a", section=("X", "D"), rank=3),
        _hit(query_id="q2", document_id="doc-c", section=("Z",), rank=2),
    ]
    assert score_run(qrels, run, ks=(5, 10)) == score_run(qrels, run, ks=(5, 10))


def test_metrics_stable_under_qrel_input_ordering() -> None:
    """Reordering the qrels list must not change any score (stable ordering)."""
    qrels = [
        _qrel(query_id="q1", document_id="doc-a", section=("X",), relevance=3),
        _qrel(query_id="q1", document_id="doc-b", section=("Y",), relevance=1),
        _qrel(query_id="q1", document_id="doc-a", section=("X", "D"), relevance=2),
    ]
    reordered = [qrels[2], qrels[0], qrels[1]]
    run = [
        _hit(query_id="q1", document_id="doc-b", section=("Y",), rank=1),
        _hit(query_id="q1", document_id="doc-a", section=("X",), rank=2),
    ]
    assert score_run(qrels, run, ks=(5, 10)) == score_run(reordered, run, ks=(5, 10))


def test_empty_inputs_return_zeros_not_errors() -> None:
    zeros = {"recall@5": 0.0, "recall@10": 0.0, "mrr@10": 0.0, "ndcg@10": 0.0}
    assert score_run([], []) == zeros
    assert score_run([], [_hit()]) == zeros
    assert score_run([_qrel()], []) == zeros


def test_validate_qrels_accepts_valid_records() -> None:
    qrels = [
        _qrel(),
        _qrel(
            query_id="q2",
            document_id="python@abc1234#Doc/guide.rst",
            section=("API", "Introduction"),
            relevance=3,
            source_id="python",
            language="zh",
            query_type="command",
            reviewer_hash="a1b2c3d4e5f6a7b8",
        ),
    ]
    assert validate_qrels(qrels) == []


def test_validate_qrels_rejects_missing_required_fields() -> None:
    for field in ("query_id", "document_id", "section_path", "relevance", "source_id", "language", "query_type"):
        record = {k: v for k, v in _qrel().items() if k != field}
        issues = validate_qrels([record])
        assert issues, f"expected issue for record missing {field!r}"


def test_validate_qrels_rejects_invalid_field_values() -> None:
    cases = [
        _qrel(section=()),  # section_path must be non-empty
        _qrel(section=("API", 7)),  # section_path items must be strings
        {**_qrel(), "language": "fr"},  # language must be zh|en
        {**_qrel(), "relevance": "2"},  # relevance must be an int
        {**_qrel(), "relevance": -1},  # relevance must be non-negative
        {**_qrel(), "reviewer_hash": "nothex!!"},  # reviewer_hash must be 16-hex when present
    ]
    for record in cases:
        issues = validate_qrels([record])
        assert issues, f"expected issue for record {record}"


def test_validate_qrels_accepts_empty_reviewer_hash() -> None:
    """Empty reviewer_hash is the documented unreviewed convention (README.md)."""
    record = {**_qrel(), "reviewer_hash": ""}
    assert validate_qrels([record]) == []


def test_validate_qrels_rejects_unknown_properties() -> None:
    record = {**_qrel(), "answer_text": "never store answer text in qrels"}
    issues = validate_qrels([record])
    assert issues
    assert any("answer_text" in issue for issue in issues)


def test_validate_qrels_rejects_unstable_ids() -> None:
    """qrels must reference stable IDs, never volatile chunk/page/element ordinals."""
    cases = [
        _qrel(document_id="doc-1:chunk:1"),
        _qrel(document_id="doc-1:parent:3"),
        _qrel(document_id="doc-1:p0"),
        _qrel(document_id="doc-1:p0:e5"),
        _qrel(document_id="42"),
        _qrel(document_id="chunk-1"),
        _qrel(section=("API", "p0")),
        _qrel(section=("e12",)),
    ]
    for record in cases:
        issues = validate_qrels([record])
        assert issues, f"expected unstable-ID issue for record {record}"


def test_validate_qrels_reads_json_and_jsonl_files(tmp_path) -> None:
    jsonl = tmp_path / "qrels.jsonl"
    jsonl.write_text(
        json.dumps(_qrel()) + "\n" + json.dumps(_qrel(query_id="q2", section=("Setup",))) + "\n",
        encoding="utf-8",
    )
    assert validate_qrels(jsonl) == []

    json_file = tmp_path / "qrels.json"
    json_file.write_text(json.dumps([_qrel(), _qrel(query_id="q2")]), encoding="utf-8")
    assert validate_qrels(json_file) == []

    bad_line = tmp_path / "qrels.badline.jsonl"
    bad_line.write_text(json.dumps(_qrel()) + "\n{broken\n", encoding="utf-8")
    with pytest.raises(ValueError):
        validate_qrels(bad_line)


def test_validate_qrels_raises_on_unreadable_json(tmp_path) -> None:
    bad = tmp_path / "qrels.broken.json"
    bad.write_text("{not json", encoding="utf-8")
    with pytest.raises(ValueError):
        validate_qrels(bad)
