"""Deterministic retrieval metrics for RAG scoring (plan Task 7.2).

Implements Recall@k, MRR@k and nDCG@k on document/page/element qrels in pure
Python. The LLM-judge path (faithfulness) lives in eval.rag and is fixed to
a pinned model/prompt; these metrics never call an LLM.
"""

from __future__ import annotations

import math
from dataclasses import dataclass


@dataclass(frozen=True)
class RetrievalQrel:
    query_id: str
    document_id: str
    page_id: str = ""
    element_id: str = ""
    relevance: float = 1.0


@dataclass(frozen=True)
class RetrievalHit:
    query_id: str
    document_id: str
    page_id: str = ""
    element_id: str = ""
    score: float = 0.0


def _key(hit: RetrievalHit) -> tuple[str, str, str]:
    return (hit.document_id, hit.page_id, hit.element_id)


def recall_at_k(ranked: list[RetrievalHit], qrels: list[RetrievalQrel], k: int) -> float:
    if not qrels or k <= 0:
        return 0.0
    relevant = {_key(q) for q in qrels}
    found = set()
    for hit in ranked[:k]:
        key = _key(hit)
        if key in relevant:
            found.add(key)
    return len(found) / len(relevant)


def mrr_at_k(ranked: list[RetrievalHit], qrels: list[RetrievalQrel], k: int) -> float:
    if not qrels or k <= 0:
        return 0.0
    relevant = {_key(q) for q in qrels}
    for i, hit in enumerate(ranked[:k]):
        if _key(hit) in relevant:
            return 1.0 / (i + 1)
    return 0.0


def ndcg_at_k(ranked: list[RetrievalHit], qrels: list[RetrievalQrel], k: int) -> float:
    if not qrels or k <= 0:
        return 0.0
    relevant = {_key(q): q.relevance for q in qrels}
    ideal = sorted((q.relevance for q in qrels), reverse=True)
    ideal_dcg = _dcg(ideal, k)
    if ideal_dcg <= 0:
        return 0.0
    actual = [relevant.get(_key(h), 0.0) for h in ranked[:k]]
    return _dcg(actual, k) / ideal_dcg


def _dcg(relevances: list[float], k: int) -> float:
    total = 0.0
    for i, rel in enumerate(relevances[:k]):
        if i == 0:
            total += rel
        else:
            total += rel / math.log2(i + 1)
    return total
