"""E5 — RAG release report that refuses to score an inadequate golden set.

Design map line 1108 asks E5 to score "only the locked golden set", report
pure negatives separately, bind every hash, and emit per-query detail with a
bootstrap CI.  Measured against the corpus as it actually exists, the honest
deliverable is a scorer that **refuses**, because the golden set cannot carry
a release claim:

* 23 of 180 rows passed arbitration (12.8%); the other 157 are DISPUTED;
* all 23 carry relevance 1.0, so precision and false-positive rate are
  undefined rather than merely noisy;
* docker and kubernetes contribute 0 of the 23, so no claim spans the 6
  declared sources;
* all 17 pure negatives are DISPUTED, so E5's own pure-negative deliverable
  has no eligible data;
* 178 of 180 ``section_path`` labels do not resolve against the live index,
  so section-level metrics are structurally uncomputable.

This module follows the E3 precedent in ``split.py``: an unmeasurable quantity
*raises* instead of returning ``0.0``.  A ``0.0`` flows into a release table
and is indistinguishable from a measured zero, which is strictly worse than a
crash.  Every refusal carries a named code from the policy's
``failure_semantics``.

Exit codes (policy ``exit_codes``):

* ``0`` RELEASE_ELIGIBLE — every gate passed and all bindings present
* ``1`` RESERVED_UNUSED
* ``2`` data fault or contract violation; nothing was measured
* ``3`` NOT_RELEASE_ELIGIBLE — gates evaluated, at least one failed

``1`` is deliberately unused.  In the contamination scanner exit 1 is the
BLOCKING business verdict "contamination found", so an escaping exception
there makes a data fault masquerade as a verdict.  Here ``2`` always means
"nothing was measured" and ``3`` always means "measured, then refused", so a
crash can never be read as a release decision.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import random
import statistics
import sys
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

__all__ = [
    "EXIT_ELIGIBLE",
    "EXIT_RESERVED_UNUSED",
    "EXIT_DATA_FAULT",
    "EXIT_NOT_ELIGIBLE",
    "GateResult",
    "KNOWN_POLICY_VERSIONS",
    "POLICY_PATH",
    "QRELS_PATH",
    "bootstrap_ci",
    "evaluate_gates",
    "load_policy",
    "macro_average",
    "main",
    "policy_sha256",
    "require_bindings",
    "require_granularity_allowed",
    "require_metric_emission_allowed",
    "select_scoreable_rows",
]


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


REPO_ROOT = _repo_root()
POLICY_PATH = REPO_ROOT / "data" / "eval" / "techdocs" / "release-policy.v1.json"
QRELS_PATH = REPO_ROOT / "data" / "eval" / "techdocs" / "qrels.sol-review.jsonl"

KNOWN_POLICY_VERSIONS = ("v1",)

#: Only rows that survived arbitration may be scored.
ELIGIBLE_REVIEW_STATUS = "AI_REVIEWED"
#: Rows whose labels were never resolved.
DISPUTED_REVIEW_STATUS = "DISPUTED"
#: Fields every qrel row must carry before it can be reasoned about at all.
REQUIRED_QREL_FIELDS = ("query_id", "document_id", "relevance", "source_id", "review_status")

EXIT_ELIGIBLE = 0
EXIT_RESERVED_UNUSED = 1
EXIT_DATA_FAULT = 2
EXIT_NOT_ELIGIBLE = 3


# ---------------------------------------------------------------------------
# Policy binding
# ---------------------------------------------------------------------------


def _sidecar_for(path: Path) -> Path:
    return path.with_name(path.name + ".sha256")


def policy_sha256(path: Path | str | None = None) -> str:
    """sha256 of the policy bytes on disk."""
    target = Path(path) if path is not None else POLICY_PATH
    if not target.is_file():
        raise ValueError(f"POLICY_MISSING: no release policy at {target}")
    return hashlib.sha256(target.read_bytes()).hexdigest()


def load_policy(path: Path | str | None = None) -> dict[str, Any]:
    """Load the release policy, verifying its hash sidecar and version.

    There is no implicit fallback policy: a release decision made against an
    unknown or drifted policy is not reproducible, and a scorer that quietly
    invents its own thresholds is not measuring anything a reader can check.
    """
    target = Path(path) if path is not None else POLICY_PATH
    if not target.is_file():
        raise ValueError(f"POLICY_MISSING: no release policy at {target}")

    raw = target.read_bytes()
    actual = hashlib.sha256(raw).hexdigest()

    sidecar = _sidecar_for(target)
    if not sidecar.is_file():
        raise ValueError(f"POLICY_MISSING: no sha256 sidecar at {sidecar}")
    recorded = sidecar.read_text(encoding="utf-8").split()
    if not recorded:
        raise ValueError(f"POLICY_HASH_MISMATCH: empty sidecar at {sidecar}")
    if recorded[0].lower() != actual:
        raise ValueError(
            "POLICY_HASH_MISMATCH: policy bytes do not match the sidecar; "
            f"expected {recorded[0]}, computed {actual}"
        )

    try:
        policy = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ValueError(f"POLICY_MISSING: policy at {target} is not valid JSON: {exc}") from exc
    if not isinstance(policy, dict):
        raise ValueError(f"POLICY_MISSING: policy at {target} is not a JSON object")

    version = policy.get("policy_version")
    if version not in KNOWN_POLICY_VERSIONS:
        raise ValueError(
            f"POLICY_VERSION_UNKNOWN: {version!r} is not understood by this code "
            f"(known: {', '.join(KNOWN_POLICY_VERSIONS)}). A newer policy must never "
            "be accepted silently."
        )
    return policy


# ---------------------------------------------------------------------------
# Gate evaluation
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class GateResult:
    """Outcome of evaluating every release gate against a qrels file.

    ``eligible`` is only ``True`` when ``failed_codes`` is empty.  The counts
    are carried alongside the verdict so a reader never has to trust the
    boolean on its own.
    """

    eligible: bool
    failed_codes: tuple[str, ...]
    total_rows: int
    golden_rows: int
    disputed_rows: int
    golden_positive_rows: int
    golden_negative_rows: int
    pure_negative_rows: int
    pure_negative_golden_rows: int
    missing_sources: tuple[str, ...]
    golden_sources: Mapping[str, int] = field(default_factory=dict)
    detail: Mapping[str, str] = field(default_factory=dict)


def _load_qrels(path: Path | str) -> list[dict[str, Any]]:
    target = Path(path)
    if not target.is_file():
        raise ValueError(f"QRELS_MISSING: no qrels file at {target}")
    rows: list[dict[str, Any]] = []
    for lineno, line in enumerate(
        target.read_text(encoding="utf-8").splitlines(), start=1
    ):
        line = line.strip()
        if not line:
            continue
        try:
            row = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(
                f"QRELS_SCHEMA_INVALID: line {lineno} of {target} is not valid JSON: {exc}"
            ) from exc
        if not isinstance(row, dict):
            raise ValueError(
                f"QRELS_SCHEMA_INVALID: line {lineno} of {target} is not an object"
            )
        missing = [f for f in REQUIRED_QREL_FIELDS if f not in row]
        if missing:
            raise ValueError(
                f"QRELS_SCHEMA_INVALID: line {lineno} of {target} lacks "
                f"{', '.join(missing)}"
            )
        rows.append(row)
    if not rows:
        raise ValueError(f"QRELS_MISSING: {target} contains no rows")
    return rows


def _relevance(row: Mapping[str, Any]) -> float:
    try:
        return float(row["relevance"])
    except (TypeError, ValueError) as exc:
        raise ValueError(
            f"QRELS_SCHEMA_INVALID: relevance {row.get('relevance')!r} for "
            f"{row.get('query_id')!r} is not a number"
        ) from exc


def evaluate_gates(
    qrels_path: Path | str | None = None,
    *,
    policy: Mapping[str, Any] | None = None,
) -> GateResult:
    """Evaluate every release gate. A gate that cannot be evaluated FAILS.

    Never raises for an inadequate set — that is the honest verdict
    (:data:`EXIT_NOT_ELIGIBLE`).  It raises only for a data fault, where
    nothing was measured at all.
    """
    resolved_policy = dict(policy) if policy is not None else load_policy()
    gates = resolved_policy.get("gates", {})
    rows = _load_qrels(qrels_path if qrels_path is not None else QRELS_PATH)

    total = len(rows)
    golden = [r for r in rows if r.get("review_status") == ELIGIBLE_REVIEW_STATUS]
    disputed = [r for r in rows if r.get("review_status") == DISPUTED_REVIEW_STATUS]
    pure_negative = [r for r in rows if _relevance(r) <= 0.0]
    pure_negative_golden = [
        r for r in pure_negative if r.get("review_status") == ELIGIBLE_REVIEW_STATUS
    ]
    golden_positive = [r for r in golden if _relevance(r) > 0.0]
    golden_negative = [r for r in golden if _relevance(r) <= 0.0]

    golden_sources: dict[str, int] = {}
    for row in golden:
        key = str(row.get("source_id", ""))
        golden_sources[key] = golden_sources.get(key, 0) + 1

    declared = tuple(gates.get("declared_sources", ()))
    missing_sources = tuple(s for s in declared if golden_sources.get(s, 0) == 0)

    failed: list[str] = []
    detail: dict[str, str] = {}

    min_rows = int(gates.get("min_golden_rows", 0))
    if len(golden) < min_rows:
        failed.append("GOLDEN_SET_TOO_SMALL")
        detail["GOLDEN_SET_TOO_SMALL"] = (
            f"{len(golden)} arbitrated rows < required {min_rows}"
        )

    min_fraction = float(gates.get("min_golden_fraction", 0.0))
    if total and (len(golden) / total) < min_fraction:
        code = "GOLDEN_FRACTION_TOO_LOW"
        failed.append(code)
        detail[code] = (
            f"{len(golden)}/{total} = {len(golden) / total:.3f} < required {min_fraction}"
        )

    if gates.get("require_negative_in_golden", False):
        min_neg = int(gates.get("min_negative_golden_rows", 1))
        if not golden_negative:
            failed.append("GOLDEN_SET_ALL_POSITIVE")
            detail["GOLDEN_SET_ALL_POSITIVE"] = (
                f"all {len(golden)} arbitrated rows are positive; precision and "
                "false-positive rate are undefined without a labelled negative"
            )
        elif len(golden_negative) < min_neg:
            code = "GOLDEN_NEGATIVES_TOO_FEW"
            failed.append(code)
            detail[code] = f"{len(golden_negative)} negative rows < required {min_neg}"

    if gates.get("require_all_declared_sources", False) and missing_sources:
        failed.append("SOURCE_COVERAGE_INCOMPLETE")
        detail["SOURCE_COVERAGE_INCOMPLETE"] = (
            f"no arbitrated rows for: {', '.join(missing_sources)}"
        )

    max_disputed = float(gates.get("max_disputed_fraction", 1.0))
    if total and (len(disputed) / total) > max_disputed:
        failed.append("TOO_MANY_DISPUTED")
        detail["TOO_MANY_DISPUTED"] = (
            f"{len(disputed)}/{total} = {len(disputed) / total:.3f} disputed > "
            f"allowed {max_disputed}"
        )

    # E5's own pure-negative deliverable needs arbitrated negatives to exist.
    if pure_negative and not pure_negative_golden:
        failed.append("PURE_NEGATIVE_SET_UNREVIEWED")
        detail["PURE_NEGATIVE_SET_UNREVIEWED"] = (
            f"all {len(pure_negative)} pure-negative rows are unarbitrated, so the "
            "false-positive / empty-rate report has no eligible data"
        )

    return GateResult(
        eligible=not failed,
        failed_codes=tuple(failed),
        total_rows=total,
        golden_rows=len(golden),
        disputed_rows=len(disputed),
        golden_positive_rows=len(golden_positive),
        golden_negative_rows=len(golden_negative),
        pure_negative_rows=len(pure_negative),
        pure_negative_golden_rows=len(pure_negative_golden),
        missing_sources=missing_sources,
        golden_sources=golden_sources,
        detail=detail,
    )


# ---------------------------------------------------------------------------
# Refusal guards
# ---------------------------------------------------------------------------


def require_metric_emission_allowed(result: GateResult) -> None:
    """Raise unless every gate passed.

    The E3 precedent: returning ``0.0`` here would be far worse than raising,
    because a zero reaches a release table and reads as a measurement.
    """
    if not result.eligible:
        raise ValueError(
            "METRIC_REQUESTED_WHILE_INELIGIBLE: refusing to emit a release metric; "
            f"failed gates: {', '.join(result.failed_codes)}"
        )


def require_granularity_allowed(
    granularity: str, *, policy: Mapping[str, Any] | None = None
) -> None:
    """Only document-level scoring is admissible at policy v1.

    178 of 180 ``section_path`` labels do not resolve against the index, so a
    section metric would report a structural label defect as retrieval quality.
    """
    resolved = dict(policy) if policy is not None else load_policy()
    allowed = str(resolved.get("scoring", {}).get("granularity", "document"))
    if granularity != allowed:
        raise ValueError(
            f"SECTION_METRIC_REQUESTED: granularity {granularity!r} is not permitted "
            f"at policy {resolved.get('policy_version')}; only {allowed!r} is "
            "admissible because section_path does not resolve against the index"
        )


def select_scoreable_rows(
    rows: Iterable[Mapping[str, Any]],
    *,
    policy: Mapping[str, Any] | None = None,
    strict: bool = False,
) -> tuple[dict[str, Any], ...]:
    """Return only arbitrated rows.

    With ``strict=True`` a DISPUTED row is an error rather than something to
    filter out, so a caller cannot quietly widen its denominator by passing
    unarbitrated rows in.
    """
    selected: list[dict[str, Any]] = []
    for row in rows:
        status = row.get("review_status")
        if status == ELIGIBLE_REVIEW_STATUS:
            selected.append(dict(row))
            continue
        if strict:
            raise ValueError(
                f"DISPUTED_ROW_SCORED: {row.get('query_id')!r} has review_status "
                f"{status!r}; only {ELIGIBLE_REVIEW_STATUS} rows may be scored"
            )
    return tuple(selected)


def macro_average(
    per_query: Mapping[str, float],
    *,
    pure_negative_qids: Iterable[str] = (),
    policy: Mapping[str, Any] | None = None,
) -> float:
    """Macro-average per-query scores, refusing to absorb pure negatives.

    A query with no relevant document scores 0.0 by construction.  Averaging
    it in silently deflates the mean and makes the corpus look worse in a way
    that has nothing to do with retrieval — the defect already present in
    ``bm25-report.json``, which averaged all 180 rows including 17 pure
    negatives.
    """
    negatives = set(pure_negative_qids)
    overlap = sorted(negatives & set(per_query))
    if overlap:
        raise ValueError(
            "PURE_NEGATIVE_IN_MACRO_AVERAGE: "
            f"{', '.join(overlap)} are pure negatives and must be reported as "
            "false-positive / empty-rate instead of averaged into a relevance metric"
        )
    if not per_query:
        raise ValueError(
            "METRIC_REQUESTED_WHILE_INELIGIBLE: no scoreable queries, so a "
            "macro-average would be an empty mean reported as a number"
        )
    return statistics.fmean(per_query.values())


def require_bindings(
    bindings: Mapping[str, Any], *, policy: Mapping[str, Any] | None = None
) -> None:
    """Every ``required_bindings`` entry must be present and non-empty."""
    resolved = dict(policy) if policy is not None else load_policy()
    required = tuple(resolved.get("scoring", {}).get("required_bindings", ()))
    missing = [
        key for key in required if not str(bindings.get(key, "") or "").strip()
    ]
    if missing:
        raise ValueError(
            f"BINDING_MISSING: {', '.join(missing)} absent or empty; a report whose "
            "provenance is not fully bound cannot be release evidence"
        )


def bootstrap_ci(
    values: Sequence[float],
    *,
    iterations: int = 1000,
    confidence: float = 0.95,
    seed: int = 20260810,
) -> tuple[float, float]:
    """Percentile bootstrap CI over *values*.

    Reported to expose its own width on a small set, never to convert an
    ineligible set into a releasable one.
    """
    if not values:
        raise ValueError(
            "METRIC_REQUESTED_WHILE_INELIGIBLE: bootstrap over an empty sample"
        )
    if iterations < 1:
        raise ValueError("METRIC_REQUESTED_WHILE_INELIGIBLE: iterations must be >= 1")
    rng = random.Random(seed)
    n = len(values)
    means = sorted(
        statistics.fmean(rng.choice(values) for _ in range(n))
        for _ in range(iterations)
    )
    tail = (1.0 - confidence) / 2.0
    lo_idx = min(int(tail * iterations), iterations - 1)
    hi_idx = min(int((1.0 - tail) * iterations), iterations - 1)
    return means[lo_idx], means[hi_idx]


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def _build_report(result: GateResult, *, policy: Mapping[str, Any], policy_hash: str,
                  qrels_hash: str, qrels_path: str) -> dict[str, Any]:
    return {
        "report_kind": "e5-rag-release-report",
        "policy_id": policy.get("policy_id", ""),
        "policy_version": policy.get("policy_version", ""),
        "policy_sha256": policy_hash,
        "qrels_path": qrels_path,
        "qrels_sha256": qrels_hash,
        "verdict": "RELEASE_ELIGIBLE" if result.eligible else "NOT_RELEASE_ELIGIBLE",
        "failed_codes": list(result.failed_codes),
        "failed_detail": dict(result.detail),
        "counts": {
            "total_rows": result.total_rows,
            "golden_rows": result.golden_rows,
            "disputed_rows": result.disputed_rows,
            "golden_positive_rows": result.golden_positive_rows,
            "golden_negative_rows": result.golden_negative_rows,
            "pure_negative_rows": result.pure_negative_rows,
            "pure_negative_golden_rows": result.pure_negative_golden_rows,
        },
        "golden_sources": dict(result.golden_sources),
        "missing_sources": list(result.missing_sources),
        "granularity": policy.get("scoring", {}).get("granularity", "document"),
        # Empty by contract while ineligible: no metric may escape a failed gate.
        "metrics": {},
    }


def main(argv: Sequence[str] | None = None) -> int:
    """Evaluate the gates and write a report. Returns the policy exit code."""
    parser = argparse.ArgumentParser(
        prog="release_report",
        description="E5 RAG release report (refuses to score an ineligible set)",
    )
    parser.add_argument("--qrels", default=str(QRELS_PATH))
    parser.add_argument("--policy", default=str(POLICY_PATH))
    parser.add_argument("--out", default="")
    args = parser.parse_args(list(argv) if argv is not None else None)

    try:
        policy = load_policy(args.policy)
        policy_hash = policy_sha256(args.policy)
        qrels_path = Path(args.qrels)
        result = evaluate_gates(qrels_path, policy=policy)
        qrels_hash = hashlib.sha256(qrels_path.read_bytes()).hexdigest()
    except ValueError as exc:
        # Any named failure code above means nothing was measured.
        print(f"ERROR: {exc}", file=sys.stderr)
        return EXIT_DATA_FAULT
    except OSError as exc:
        print(f"ERROR: QRELS_MISSING: {exc}", file=sys.stderr)
        return EXIT_DATA_FAULT

    report = _build_report(
        result,
        policy=policy,
        policy_hash=policy_hash,
        qrels_hash=qrels_hash,
        qrels_path=str(qrels_path),
    )

    if args.out:
        out = Path(args.out)
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_text(
            json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )

    print(f"verdict      : {report['verdict']}")
    print(f"golden rows  : {result.golden_rows} of {result.total_rows}")
    print(f"disputed     : {result.disputed_rows}")
    if result.failed_codes:
        print("failed gates :")
        for code in result.failed_codes:
            print(f"  - {code}: {result.detail.get(code, '')}")

    if not result.eligible:
        return EXIT_NOT_ELIGIBLE

    try:
        require_metric_emission_allowed(result)
    except ValueError as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return EXIT_DATA_FAULT
    return EXIT_ELIGIBLE


if __name__ == "__main__":
    raise SystemExit(main())
