"""Choosing among several candidate patches, deterministically.

Sampling the same instance more than once and keeping the best candidate is the
cheapest known way to buy accuracy with compute. It is also the easiest place to
lose the accuracy again: the published test-time-compute results record an
*outright regression* at a sampling budget of 8, and the two causes named were
low-coverage validation signals and **random tie-breaking**. This module exists
mainly to make the second cause impossible.

The ordering rules
------------------
Candidates are ranked on a strict total order, evaluated in sequence:

1. **Not disqualified** before disqualified. An empty or tests-only patch cannot
   pass, so no amount of anything else rescues it.
2. **Fewer unverifiable parts** first — checks that returned
   :attr:`~eval.harness.validate.Evidence.NO_EVIDENCE` because a patched file was
   missing from the worktree or could not be read. Those are gaps in what is
   known about the candidate, not opinions about it.
3. **Fewer files touched** first. A patch that edits one file to fix one bug is
   more likely to be the intended change than one that edits five.
4. **Smaller diff** first, for the same reason.

A first attempt ranked on *more weak-positive checks* instead of rule 2, which
looked like "prefer the better-evidenced patch" and was actually backwards:
:func:`~eval.harness.validate.check_syntax` emits one weak positive per patched
Python file, so a patch touching five files collected five and beat a one-file
patch on the very rule that rule 3 exists to express. Counting corroborations
rewards surface area. Counting *gaps* does not.
5. **Lexicographically smaller sha256 of the diff.** Arbitrary — and that is the
   point. It is arbitrary *and fixed*, so the same candidate set always yields
   the same winner, on any machine, in any order of arrival.

Rule 5 is what separates this from a coin flip. Rules 3 and 4 are heuristics and
are labelled as such; they are applied only after the evidence-based rules have
had their say.
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterable, Sequence

from eval.harness.validate import Evidence, ValidationReport, patched_paths, validate_patch


@dataclass
class Candidate:
    """One sampled patch, with the metadata the ordering needs."""

    diff: str
    label: str = ""
    temperature: float | None = None
    report: ValidationReport | None = None

    @property
    def digest(self) -> str:
        return hashlib.sha256((self.diff or "").encode("utf-8")).hexdigest()

    @property
    def file_count(self) -> int:
        return len(patched_paths(self.diff))

    @property
    def size(self) -> int:
        return len(self.diff or "")

    @property
    def disqualified(self) -> bool:
        return bool(self.report and self.report.disqualified)

    @property
    def weak_positives(self) -> int:
        """Corroborating checks. Reported, but never ranked on — see rule 2."""
        if not self.report:
            return 0
        return sum(
            1 for check in self.report.checks if check.evidence is Evidence.WEAK_POSITIVE
        )

    @property
    def unverifiable(self) -> int:
        """Checks that could not run, i.e. gaps in what is known."""
        if not self.report:
            return 0
        return sum(
            1 for check in self.report.checks if check.evidence is Evidence.NO_EVIDENCE
        )


def _sort_key(candidate: Candidate) -> tuple:
    return (
        1 if candidate.disqualified else 0,      # rule 1
        candidate.unverifiable,                  # rule 2
        candidate.file_count or 10**6,           # rule 3 (0 files = unparseable)
        candidate.size,                          # rule 4
        candidate.digest,                        # rule 5 — arbitrary but fixed
    )


def rank(candidates: Iterable[Candidate]) -> list[Candidate]:
    """Return *candidates* in strict, reproducible preference order."""
    return sorted(candidates, key=_sort_key)


def select_best(
    diffs: Sequence[str],
    repo_root: str | Path,
    *,
    labels: Sequence[str] | None = None,
    temperatures: Sequence[float] | None = None,
) -> tuple[Candidate | None, list[Candidate]]:
    """Validate every candidate, then return ``(winner, ranked)``.

    The winner is ``None`` only when *diffs* is empty or every candidate is
    disqualified — that is, when there is genuinely nothing worth submitting.
    Returning a disqualified patch as a "winner" would hand the scorer a verdict
    that was already knowable without paying for a container.
    """
    candidates: list[Candidate] = []
    for index, diff in enumerate(diffs):
        candidate = Candidate(
            diff=diff or "",
            label=(labels[index] if labels and index < len(labels) else f"sample-{index}"),
            temperature=(
                temperatures[index] if temperatures and index < len(temperatures) else None
            ),
        )
        candidate.report = validate_patch(candidate.diff, repo_root)
        candidates.append(candidate)

    ranked = rank(candidates)
    if not ranked or ranked[0].disqualified:
        return None, ranked
    return ranked[0], ranked


def selection_record(winner: Candidate | None, ranked: Sequence[Candidate]) -> dict:
    """A manifest-ready description of the choice and why.

    Recorded so a reviewer can reconstruct the decision without rerunning it. A
    selection step whose reasoning is not persisted is indistinguishable from a
    coin flip after the fact, even when it was deterministic at the time.
    """
    return {
        "candidates": len(ranked),
        "winner": winner.label if winner else None,
        "winner_digest": winner.digest if winner else None,
        "all_disqualified": bool(ranked) and all(c.disqualified for c in ranked),
        "ranking": [
            {
                "label": candidate.label,
                "temperature": candidate.temperature,
                "digest": candidate.digest[:16],
                "files": candidate.file_count,
                "bytes": candidate.size,
                "disqualified": candidate.disqualified,
                "weak_positives": candidate.weak_positives,
                "summary": candidate.report.summary if candidate.report else "",
            }
            for candidate in ranked
        ],
        "tie_break": "sha256(diff) ascending — arbitrary but fixed, never random",
    }
