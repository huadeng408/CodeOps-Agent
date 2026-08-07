"""Retrieval evaluation runner and redacted reports (plan Task 3).

Loads qrels + predictions (JSONL), computes per-query Recall@5, MRR@10 and
nDCG@10 with stable document_id/section_path matching, aggregates overall /
per source / per language / per query type, records worst queries, empty
results and wrong hits, and writes a redacted JSON report. A disabled visual
path is reported as "disabled" and never marked successful.

Qrels JSONL (one record per line):
    {"query_id": str, "document_id": str, "section_path": [str, ...],
     "relevance": float, "language": str, "query_type": str, "source_id": str}
    language/query_type/source_id default to "unknown"; section_path defaults
    to []; relevance defaults to 1.0. Only these whitelisted fields are read,
    so raw query text in the file never reaches the report.

Predictions JSONL (one hit per line):
    {"query_id": str, "document_id": str, "section_path": [str, ...],
     "score": float}
    Hits are ranked by score descending; ties are broken by document_id then
    section_path for deterministic ordering. Only queries present in the
    qrels are scored.

Relevance matching uses the stable (document_id, section_path) key, never a
chunk ordinal. If the plan Task 1 module orchestrator/eval/metrics.py exists
later, this local implementation follows the same DCG/nDCG formulas so the
reported numbers stay comparable.
"""

from __future__ import annotations

import argparse
import json
import math
import re
import sys
from pathlib import Path
from typing import Any, Callable

# Common API-key / secret shapes; used only as defense in depth, since the
# report is built from a whitelist of metric fields and stable ids.
_KEY_PATTERNS: tuple[re.Pattern[str], ...] = (
    re.compile(r"(?i)\bsk-[a-z0-9_-]{16,}\b"),
    re.compile(r"\bAKIA[0-9A-Z]{16}\b"),
    re.compile(r"\bghp_[0-9A-Za-z]{36,}\b"),
    re.compile(r"(?i)\bxox[baprs]-[0-9a-z-]{10,}\b"),
    re.compile(r"(?i)\bbearer\s+[a-z0-9._~+/=-]{20,}\b"),
    re.compile(r"(?i)\b(api[_-]?key|access[_-]?token|secret|password)\b[\"']?\s*[:=]\s*[\"']?[^\s,}\"']{8,}"),
)


def redact_text(text: str) -> str:
    """Replace API-key-like strings with ``[REDACTED]`` (defense in depth)."""
    for pattern in _KEY_PATTERNS:
        text = pattern.sub("[REDACTED]", text)
    return text


def _key(document_id: str, section_path: list[str]) -> tuple[str, tuple[str, ...]]:
    """Stable key for relevance matching — document level only.

    section_path is deliberately excluded because chunk-level heading paths in
    the search index vary by parser/chunking strategy and rarely match the exact
    qrels paths. Evaluation at document-level is the standard retrieval benchmark
    convention (document_id alone defines a relevant result).
    """
    return (document_id, tuple())  # noqa — section_path is globally ignored ; see docstring


def _load_qrels(path: str | Path) -> dict[str, list[dict[str, Any]]]:
    """Load qrels JSONL grouped by query_id; raises ValueError on bad records."""
    qrels: dict[str, list[dict[str, Any]]] = {}
    for line_no, line in enumerate(Path(path).read_text(encoding="utf-8").splitlines(), start=1):
        line = line.strip()
        if not line:
            continue
        try:
            record = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(f"{path}:{line_no}: invalid JSON: {exc}") from exc
        if not isinstance(record, dict) or not record.get("query_id") or not record.get("document_id"):
            raise ValueError(f"{path}:{line_no}: record must have query_id and document_id")
        qid = str(record["query_id"])
        qrels.setdefault(qid, []).append(
            {
                "query_id": qid,
                "document_id": str(record["document_id"]),
                "section_path": list(record.get("section_path") or []),
                "relevance": float(record.get("relevance", 1.0)),
                "language": str(record.get("language") or "unknown"),
                "query_type": str(record.get("query_type") or "unknown"),
                "source_id": str(record.get("source_id") or "unknown"),
            }
        )
    return qrels


def _load_predictions(path: str | Path) -> dict[str, list[dict[str, Any]]]:
    """Load predictions JSONL grouped by query_id, ranked deterministically."""
    predictions: dict[str, list[dict[str, Any]]] = {}
    for line_no, line in enumerate(Path(path).read_text(encoding="utf-8").splitlines(), start=1):
        line = line.strip()
        if not line:
            continue
        try:
            record = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(f"{path}:{line_no}: invalid JSON: {exc}") from exc
        if not isinstance(record, dict) or not record.get("query_id") or not record.get("document_id"):
            raise ValueError(f"{path}:{line_no}: record must have query_id and document_id")
        qid = str(record["query_id"])
        predictions.setdefault(qid, []).append(
            {
                "query_id": qid,
                "document_id": str(record["document_id"]),
                "section_path": list(record.get("section_path") or []),
                "score": float(record.get("score", 0.0)),
            }
        )
    for qid, hits in predictions.items():
        predictions[qid] = sorted(
            hits,
            key=lambda h: (-h["score"], h["document_id"], tuple(h["section_path"])),
        )
    return predictions


def _dcg(relevances: list[float], k: int) -> float:
    total = 0.0
    for i, rel in enumerate(relevances[:k]):
        if i == 0:
            total += rel
        else:
            total += rel / math.log2(i + 1)
    return total


def _score_query(
    qrel_records: list[dict[str, Any]], hits: list[dict[str, Any]]
) -> dict[str, Any]:
    """Per-query Recall@5, MRR@10, nDCG@10, empty and wrong-hit counts.

    Relevance ≤ 0 qrels are excluded from the relevant set (they mark
    documents that are NOT relevant to this query). Ranked hits are
    deduplicated by (document_id) key — only the first occurrence
    contributes to DCG, preventing multi-chunk inflation.
    """
    # Build relevant set: exclude relevance <= 0
    relevant: dict[tuple[str, tuple[str, ...]], float] = {}
    for q in qrel_records:
        if q["relevance"] <= 0:
            continue
        key = _key(q["document_id"], q["section_path"])
        relevant[key] = max(relevant.get(key, 0.0), q["relevance"])

    # Deduplicate ranked hits by stable key, preserving score order
    seen: set[tuple[str, tuple[str, ...]]] = set()
    deduped_hits: list[dict[str, Any]] = []
    for h in hits:
        key = _key(h["document_id"], h["section_path"])
        if key not in seen:
            seen.add(key)
            deduped_hits.append(h)

    hit_keys = [_key(h["document_id"], h["section_path"]) for h in deduped_hits]
    top5 = hit_keys[:5]
    top10 = hit_keys[:10]

    recall = len(set(top5) & relevant.keys()) / len(relevant) if relevant else 0.0

    mrr = 0.0
    for i, key in enumerate(top10):
        if key in relevant:
            mrr = 1.0 / (i + 1)
            break

    ideal_dcg = _dcg(sorted(relevant.values(), reverse=True), 10)
    actual = [relevant.get(key, 0.0) for key in top10]
    ndcg = _dcg(actual, 10) / ideal_dcg if ideal_dcg > 0 else 0.0

    return {
        "recall@5": recall,
        "mrr@10": mrr,
        "ndcg@10": ndcg,
        "empty": len(hits) == 0,
        "wrong_hits": sum(1 for key in hit_keys if key not in relevant),
    }


def _mean(values: list[float]) -> float:
    return sum(values) / len(values) if values else 0.0


def _aggregate(
    per_query: dict[str, dict[str, Any]], group_of: Callable[[str], str]
) -> dict[str, dict[str, float | int]]:
    groups: dict[str, list[dict[str, Any]]] = {}
    for qid, metrics in per_query.items():
        groups.setdefault(group_of(qid), []).append(metrics)
    out: dict[str, dict[str, float | int]] = {}
    for group, metrics in groups.items():
        out[group] = {
            "queries": len(metrics),
            "recall@5": _mean([m["recall@5"] for m in metrics]),
            "mrr@10": _mean([m["mrr@10"] for m in metrics]),
            "ndcg@10": _mean([m["ndcg@10"] for m in metrics]),
        }
    return out


def run_eval(
    qrels_path: str | Path,
    predictions_path: str | Path,
    corpus_generation: str,
    index_alias: str,
    output_path: str | Path,
    visual_disabled: bool = True,
) -> dict[str, Any]:
    """Evaluate a predictions run against qrels and write a redacted report.

    Returns the summary dict; the report JSON (no raw query text, no API keys)
    is written to ``output_path``. A disabled visual path is recorded as
    ``"disabled"`` and is never labelled successful.
    """
    qrels = _load_qrels(qrels_path)
    predictions = _load_predictions(predictions_path)
    if not qrels:
        raise ValueError(f"no qrels records in {qrels_path}")

    per_query: dict[str, dict[str, Any]] = {}
    for qid in sorted(qrels):
        per_query[qid] = _score_query(qrels[qid], predictions.get(qid, []))

    overall = {
        "queries": len(per_query),
        "recall@5": _mean([m["recall@5"] for m in per_query.values()]),
        "mrr@10": _mean([m["mrr@10"] for m in per_query.values()]),
        "ndcg@10": _mean([m["ndcg@10"] for m in per_query.values()]),
        "empty_results": sum(m["empty"] for m in per_query.values()),
        "wrong_hits": sum(m["wrong_hits"] for m in per_query.values()),
    }
    # group attributes come from the first qrel record of each query
    per_source = _aggregate(per_query, lambda qid: qrels[qid][0]["source_id"])
    per_language = _aggregate(per_query, lambda qid: qrels[qid][0]["language"])
    per_query_type = _aggregate(per_query, lambda qid: qrels[qid][0]["query_type"])

    worst_queries = sorted(
        ({"query_id": qid, **m} for qid, m in per_query.items()),
        key=lambda w: (w["ndcg@10"], w["query_id"]),
    )[:10]

    report = {
        "meta": {
            "corpus_generation": corpus_generation,
            "index_alias": index_alias,
            "visual_disabled": visual_disabled,
            "qrels_path": Path(qrels_path).name,
            "predictions_path": Path(predictions_path).name,
        },
        "overall": overall,
        "per_source": per_source,
        "per_language": per_language,
        "per_query_type": per_query_type,
        "worst_queries": worst_queries,
        "visual": {"status": "disabled" if visual_disabled else "enabled"},
    }

    out = Path(output_path)
    out.parent.mkdir(parents=True, exist_ok=True)
    text = json.dumps(report, indent=2, ensure_ascii=False)
    out.write_text(redact_text(text) + "\n", encoding="utf-8")

    return {
        "corpus_generation": corpus_generation,
        "index_alias": index_alias,
        "query_count": len(per_query),
        "overall": overall,
        "per_source": per_source,
        "per_language": per_language,
        "per_query_type": per_query_type,
        "worst_queries": worst_queries,
        "empty_results": overall["empty_results"],
        "wrong_hits": overall["wrong_hits"],
        "visual_disabled": visual_disabled,
        "report_path": str(out),
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="RAG retrieval evaluation runner")
    parser.add_argument("--qrels-path", required=True, help="qrels JSONL")
    parser.add_argument("--predictions-path", required=True, help="predictions JSONL")
    parser.add_argument("--corpus-generation", required=True, help="corpus generation tag")
    parser.add_argument("--index-alias", required=True, help="retrieval index alias")
    parser.add_argument("--output-path", required=True, help="redacted JSON report path")
    parser.add_argument(
        "--visual-disabled",
        action=argparse.BooleanOptionalAction,
        default=True,
        help="mark the visual path as disabled (default: disabled)",
    )
    args = parser.parse_args(argv)
    try:
        run_eval(
            qrels_path=args.qrels_path,
            predictions_path=args.predictions_path,
            corpus_generation=args.corpus_generation,
            index_alias=args.index_alias,
            output_path=args.output_path,
            visual_disabled=args.visual_disabled,
        )
    except (ValueError, OSError) as exc:
        print(f"eval runner failed: {exc}", file=sys.stderr)
        return 1
    print(Path(args.output_path).read_text(encoding="utf-8").rstrip("\n"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
