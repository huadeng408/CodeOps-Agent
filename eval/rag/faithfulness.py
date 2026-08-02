"""RAG faithfulness scoring (plan Task 7.2).

Faithfulness prioritizes exact/evidence-rule checks: a claim is faithful when
its text (normalized n-grams) appears in the cited evidence. The LLM judge is
an OPTIONAL second opinion with a fixed model/prompt; every judge call must
record whether it disagreed with the evidence rule so disagreement rates are
reported, not hidden.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

# LLM judge identity — fixed so runs are reproducible; changing it changes
# faithfulness numbers and must bump the run manifest.
JUDGE_MODEL = "deepseek-v4-pro"
JUDGE_PROMPT = (
    "You are a faithfulness judge. Given a claim and the evidence it cites, "
    "answer only YES or NO: is every factual assertion in the claim directly "
    "supported by the evidence?\n\n"
    "Claim: {claim}\n\nEvidence: {evidence}"
)


@dataclass(frozen=True)
class FaithfulnessResult:
    claim: str
    evidence: str
    evidence_rule_verdict: bool
    judge_verdict: bool | None = None
    judge_disagrees: bool = False


def _normalize(text: str) -> str:
    text = text.lower()
    text = re.sub(r"[^\w一-鿿\s]", "", text)
    return re.sub(r"\s+", " ", text).strip()


def evidence_rule_faithful(claim: str, evidence: str, ngram: int = 5) -> bool:
    """True when every claim n-gram of length ngram appears in the evidence.

    This is a strict exact-match rule: it never invents support. Claims with
    no n-grams (too short) are considered unfaithful (fail closed).
    """
    claim_tokens = _normalize(claim).split()
    if len(claim_tokens) < ngram:
        return False
    evidence_norm = _normalize(evidence)
    for i in range(len(claim_tokens) - ngram + 1):
        gram = " ".join(claim_tokens[i : i + ngram])
        if gram not in evidence_norm:
            return False
    return True


def judge_verdict(claim: str, evidence: str) -> bool:
    """Placeholder for the fixed LLM judge; subclasses wire the API call.

    The real implementation calls JUDGE_MODEL with JUDGE_PROMPT and parses
    YES/NO. Tests never invoke it (no network).
    """
    raise NotImplementedError("LLM judge requires the pinned model; use evidence_rule_faithful offline")


def score(claim: str, evidence: str, judge: bool = False) -> FaithfulnessResult:
    """Score one claim against its evidence; judge is optional and recorded."""
    rule = evidence_rule_faithful(claim, evidence)
    result = FaithfulnessResult(claim=claim, evidence=evidence, evidence_rule_verdict=rule)
    if judge:
        try:
            verdict = judge_verdict(claim, evidence)
        except NotImplementedError:
            verdict = None
        result = FaithfulnessResult(
            claim=claim,
            evidence=evidence,
            evidence_rule_verdict=rule,
            judge_verdict=verdict,
            judge_disagrees=verdict is not None and verdict != rule,
        )
    return result


def disagreement_rate(results: list[FaithfulnessResult]) -> float:
    """Fraction of judged claims where judge and evidence rule disagree."""
    judged = [r for r in results if r.judge_verdict is not None]
    if not judged:
        return 0.0
    return sum(1 for r in judged if r.judge_disagrees) / len(judged)
