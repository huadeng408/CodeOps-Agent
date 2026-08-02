"""Retrieval metrics and citation/faithfulness tests (plan Task 7.2)."""

from __future__ import annotations

from eval.rag.citations import (
    Citation,
    citation_precision,
    citation_recall,
    parse_citation_keys,
    report,
)
from eval.rag.faithfulness import (
    disagreement_rate,
    evidence_rule_faithful,
    score,
)
from eval.retrieval.metrics import (
    RetrievalHit,
    RetrievalQrel,
    mrr_at_k,
    ndcg_at_k,
    recall_at_k,
)


def qrel(doc: str, page: str = "", element: str = "") -> RetrievalQrel:
    return RetrievalQrel(query_id="q1", document_id=doc, page_id=page, element_id=element)


def hit(doc: str, page: str = "", element: str = "") -> RetrievalHit:
    return RetrievalHit(query_id="q1", document_id=doc, page_id=page, element_id=element)


def test_recall_at_k() -> None:
    ranked = [hit("a"), hit("a"), hit("b")]
    qrels = [qrel("a"), qrel("b")]
    # Top-2 contains [a, a]: only one unique relevant doc found.
    assert recall_at_k(ranked, qrels, 2) == 0.5
    assert recall_at_k(ranked, qrels, 3) == 1.0
    assert recall_at_k(ranked, qrels, 1) == 0.5


def test_mrr_at_k() -> None:
    ranked = [hit("x"), hit("b")]
    qrels = [qrel("b")]
    assert mrr_at_k(ranked, qrels, 10) == 0.5
    assert mrr_at_k(ranked, qrels, 1) == 0.0


def test_ndcg_at_k_perfect() -> None:
    ranked = [hit("a"), hit("b"), hit("c")]
    qrels = [qrel("a"), qrel("b"), qrel("c")]
    assert ndcg_at_k(ranked, qrels, 10) == 1.0


def test_parse_citation_keys() -> None:
    assert parse_citation_keys("doc-a/p1/e1, doc-b/p2/e2") == ["doc-a/p1/e1", "doc-b/p2/e2"]
    assert parse_citation_keys("doc-a/p1/e1\ndoc-b/p2/e2") == ["doc-a/p1/e1", "doc-b/p2/e2"]
    assert parse_citation_keys("") == []


def test_citation_precision_and_recall() -> None:
    citations = [
        Citation(key="doc-a/p1/e1", supports_claim=True),
        Citation(key="doc-b/p2/e2", supports_claim=False),
    ]
    assert citation_precision(citations) == 0.5
    assert citation_recall(citations, ["doc-a/p1/e1", "doc-b/p2/e2", "doc-c/p3/e3"]) == 2 / 3


def test_report_counts() -> None:
    citations = [Citation(key="a", supports_claim=True), Citation(key="b", supports_claim=False)]
    result = report(citations, ["a"], judge_disagreement=0.1)
    assert result.total_citations == 2
    assert result.supported_citations == 1
    assert result.judge_disagreement == 0.1


def test_evidence_rule_faithful_exact_support() -> None:
    claim = "The Go scheduler uses work-stealing queues"
    evidence = "The Go scheduler uses work-stealing queues for balanced parallelism."
    assert evidence_rule_faithful(claim, evidence) is True


def test_evidence_rule_faithful_rejects_hallucination() -> None:
    claim = "The Go scheduler uses quantum entanglement"
    evidence = "The Go scheduler uses work-stealing queues for balanced parallelism."
    assert evidence_rule_faithful(claim, evidence) is False


def test_evidence_rule_fail_closed_on_short_claim() -> None:
    assert evidence_rule_faithful("Too short", "Too short claim here") is False


def test_score_without_judge() -> None:
    result = score("A uses B", "A uses B here", judge=False)
    assert result.evidence_rule_verdict is False  # claim too short, fail closed
    assert result.judge_verdict is None


def test_score_with_judge_records_disagreement() -> None:
    # Without a wired judge, judge_verdict stays None and disagreement 0.
    results = [score("X is Y and Z is W", "X is Y and Z is W", judge=True)]
    assert disagreement_rate(results) == 0.0
