"""Deterministic multimodal metric tests (plan Task 5.2)."""

from __future__ import annotations

from eval.retrieval.multimodal_metrics import (
    Hit,
    Qrel,
    aggregate,
    bbox_hit_rate,
    mrr_at_k,
    ndcg_at_k,
    recall_at_k,
)


def qrel(page: str, element: str = "", bbox: list[float] | None = None) -> Qrel:
    return Qrel(query_id="q1", document_id="doc-1", page_id=page, element_id=element, bbox=bbox)


def hit(page: str, element: str = "", bbox: list[float] | None = None, score: float = 1.0) -> Hit:
    return Hit(document_id="doc-1", page_id=page, element_id=element, bbox=bbox, score=score)


def test_ndcg_at_k_perfect_ranking_is_1() -> None:
    ranked = [hit("p1"), hit("p2"), hit("p3")]
    qrels = [qrel("p1"), qrel("p2"), qrel("p3")]
    assert ndcg_at_k(ranked, qrels, 10) == 1.0


def test_ndcg_at_k_degrades_with_wrong_order() -> None:
    # With a fully-relevant ranking, any permutation of the relevant pages
    # still yields nDCG 1.0 (nDCG normalizes by the ideal ranking). To see a
    # penalty you need irrelevant hits interleaved before relevant ones.
    ranked = [hit("p9"), hit("p1"), hit("p2")]
    qrels = [qrel("p1"), qrel("p2")]
    perfect = ndcg_at_k([hit("p1"), hit("p2")], qrels, 10)
    degraded = ndcg_at_k(ranked, qrels, 10)
    assert degraded < perfect


def test_ndcg_at_k_ignores_irrelevant() -> None:
    ranked = [hit("p9"), hit("p1")]
    qrels = [qrel("p1")]
    # p9 is irrelevant; nDCG@10 must still find p1 at rank 2.
    assert ndcg_at_k(ranked, qrels, 10) > 0.0
    assert ndcg_at_k(ranked, qrels, 1) == 0.0


def test_recall_at_k_counts_unique_pages() -> None:
    ranked = [hit("p1"), hit("p1"), hit("p2"), hit("p3")]
    qrels = [qrel("p1"), qrel("p2")]
    # Both relevant pages are found in top 3 even with duplicates.
    assert recall_at_k(ranked, qrels, 3) == 1.0
    assert recall_at_k(ranked, qrels, 1) == 0.5


def test_recall_at_k_respects_element_scope() -> None:
    ranked = [hit("p1", "e2"), hit("p1", "e1")]
    qrels = [qrel("p1", "e1")]
    assert recall_at_k(ranked, qrels, 2) == 1.0
    assert recall_at_k(ranked, qrels, 1) == 0.0


def test_mrr_at_k() -> None:
    ranked = [hit("p9"), hit("p2"), hit("p1")]
    qrels = [qrel("p1")]
    assert mrr_at_k(ranked, qrels, 10) == 1 / 3
    assert mrr_at_k(ranked, qrels, 1) == 0.0


def test_bbox_hit_rate_overlapping_bbox() -> None:
    ranked = [hit("p1", bbox=[0.0, 0.0, 100.0, 100.0])]
    qrels = [qrel("p1", bbox=[10.0, 10.0, 90.0, 90.0])]
    assert bbox_hit_rate(ranked, qrels, 10) == 1.0


def test_bbox_hit_rate_non_overlapping() -> None:
    ranked = [hit("p1", bbox=[0.0, 0.0, 10.0, 10.0])]
    qrels = [qrel("p1", bbox=[500.0, 500.0, 600.0, 600.0])]
    assert bbox_hit_rate(ranked, qrels, 10) == 0.0


def test_bbox_hit_rate_ignores_qrels_without_bbox() -> None:
    ranked = [hit("p1")]
    qrels = [qrel("p1")]  # no bbox declared
    assert bbox_hit_rate(ranked, qrels, 10) == 0.0


def test_aggregate_mean() -> None:
    assert aggregate([("q1", 0.8), ("q2", 0.6)]) == 0.7
    assert aggregate([]) == 0.0
