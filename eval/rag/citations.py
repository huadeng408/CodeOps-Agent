"""RAG answer citation metrics (plan Task 7.2).

Citation precision/recall measure whether the answer's citations actually
support the claims, against an evidence map of (document,page[,element])
keys that were retrievable. The scorer is deterministic; an optional LLM
judge (fixed model/prompt) can be plugged in for faithfulness, and judge
disagreement must be reported.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Citation:
    key: str  # document_id/page_id/element_id or file:chunk fallback
    supports_claim: bool = True


@dataclass(frozen=True)
class CitationReport:
    precision: float
    recall: float
    total_citations: int
    supported_citations: int
    judge_disagreement: float = 0.0


def parse_citation_keys(citation_text: str) -> list[str]:
    """Split a citation string on commas/newlines; trims whitespace."""
    if not citation_text:
        return []
    return [part.strip() for part in citation_text.replace("\n", ",").split(",") if part.strip()]


def citation_precision(citations: list[Citation]) -> float:
    """Fraction of citations that support their claims."""
    if not citations:
        return 0.0
    supported = sum(1 for c in citations if c.supports_claim)
    return supported / len(citations)


def citation_recall(citations: list[Citation], required_keys: list[str]) -> float:
    """Fraction of required evidence keys the answer actually cited."""
    if not required_keys:
        return 0.0
    cited = {c.key for c in citations}
    found = sum(1 for key in required_keys if key in cited)
    return found / len(required_keys)


def report(citations: list[Citation], required_keys: list[str], judge_disagreement: float = 0.0) -> CitationReport:
    supported = sum(1 for c in citations if c.supports_claim)
    return CitationReport(
        precision=citation_precision(citations),
        recall=citation_recall(citations, required_keys),
        total_citations=len(citations),
        supported_citations=supported,
        judge_disagreement=judge_disagreement,
    )
