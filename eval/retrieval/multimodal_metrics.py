"""Multimodal retrieval metrics for the visual pilot bake-off (plan Task 5.2).

Deterministic implementations of nDCG@k, Recall@k and bbox hit rate that
consume qrels pointing at page/element/bbox. The bake-off compares three
paths on the SAME qrels:
  - text-only retrieval (existing text index)
  - page visual retrieval (visual pilot index)
  - late-interaction over text top-N pages

Every function is pure and offline-runnable; no model is loaded.
"""

from __future__ import annotations

import math
from dataclasses import dataclass


@dataclass(frozen=True)
class Qrel:
    query_id: str
    document_id: str
    page_id: str
    element_id: str = ""
    bbox: list[float] | None = None
    relevance: float = 1.0


@dataclass(frozen=True)
class Hit:
    document_id: str
    page_id: str
    element_id: str = ""
    bbox: list[float] | None = None
    score: float = 0.0


def _dcg_at_k(relevances: list[float], k: int) -> float:
    total = 0.0
    for i, rel in enumerate(relevances[:k]):
        if i == 0:
            total += rel
        else:
            total += rel / math.log2(i + 1)
    return total


def ndcg_at_k(ranked: list[Hit], qrels: list[Qrel], k: int) -> float:
    """nDCG@k for one query against its qrels."""
    if k <= 0 or not ranked:
        return 0.0
    ideal = sorted((q.relevance for q in qrels), reverse=True)
    if not ideal:
        return 0.0
    ideal_dcg = _dcg_at_k(ideal, k)
    if ideal_dcg <= 0:
        return 0.0
    # A hit matches a qrel when document+page (and element, when given) align.
    matches: dict[tuple[str, str, str], float] = {}
    for qrel in qrels:
        matches[(qrel.document_id, qrel.page_id, qrel.element_id)] = qrel.relevance
    relevances = [
        matches.get((h.document_id, h.page_id, h.element_id), 0.0)
        for h in ranked
    ]
    return _dcg_at_k(relevances, k) / ideal_dcg


def recall_at_k(ranked: list[Hit], qrels: list[Qrel], k: int) -> float:
    """Recall@k = fraction of relevant (document,page[,element]) found in top k."""
    if not qrels:
        return 0.0
    relevant = {(q.document_id, q.page_id, q.element_id) for q in qrels}
    if not relevant:
        return 0.0
    found: set[tuple[str, str, str]] = set()
    for hit in ranked[:k]:
        key = (hit.document_id, hit.page_id, hit.element_id)
        if key in relevant:
            found.add(key)
    return len(found) / len(relevant)


def mrr_at_k(ranked: list[Hit], qrels: list[Qrel], k: int) -> float:
    """MRR@k: reciprocal rank of the first relevant hit."""
    if k <= 0 or not ranked:
        return 0.0
    relevant = {(q.document_id, q.page_id, q.element_id) for q in qrels}
    for i, hit in enumerate(ranked[:k]):
        if (hit.document_id, hit.page_id, hit.element_id) in relevant:
            return 1.0 / (i + 1)
    return 0.0


def bbox_hit_rate(ranked: list[Hit], qrels: list[Qrel], k: int) -> float:
    """Fraction of qrels whose page was found AND whose bbox overlaps the hit.

    A bbox hit requires the qrel to declare a bbox AND the matching hit to
    carry an overlapping bbox on the same page. Qrels without a bbox do not
    count toward the denominator (bbox hit is reported on bbox-bearing qrels
    only).
    """
    bbox_qrels = [q for q in qrels if q.bbox is not None]
    if not bbox_qrels:
        return 0.0
    # Map page -> hits with bbox from the ranked list.
    page_hits: dict[str, list[Hit]] = {}
    for hit in ranked[:k]:
        if hit.bbox is not None:
            page_hits.setdefault(hit.page_id, []).append(hit)
    hit_count = 0
    for qrel in bbox_qrels:
        for hit in page_hits.get(qrel.page_id, []):
            if _bbox_overlap(qrel.bbox or [], hit.bbox or []):
                hit_count += 1
                break
    return hit_count / len(bbox_qrels)


def _bbox_overlap(a: list[float], b: list[float]) -> bool:
    if len(a) != 4 or len(b) != 4:
        return False
    ax1, ay1, ax2, ay2 = a
    bx1, by1, bx2, by2 = b
    inter_w = min(ax2, bx2) - max(ax1, bx1)
    inter_h = min(ay2, by2) - max(ay1, by1)
    if inter_w <= 0 or inter_h <= 0:
        return False
    # IoU >= 0.5 counts as a bbox hit.
    union = (ax2 - ax1) * (ay2 - ay1) + (bx2 - bx1) * (by2 - by1) - inter_w * inter_h
    if union <= 0:
        return False
    return (inter_w * inter_h) / union >= 0.5


def aggregate(per_query: list[tuple[str, float]]) -> float:
    """Simple mean over per-query scores; empty input => 0.0."""
    if not per_query:
        return 0.0
    return sum(score for _, score in per_query) / len(per_query)
