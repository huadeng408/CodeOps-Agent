"""Deterministic retrieval metrics for stable-ID qrels (plan Task 1).

Scores a retrieval run against graded qrels that reference stable
``document_id`` / ``section_path`` pairs instead of volatile chunk ordinals.

Matching rule: a run hit ``(document_id, section_path)`` matches a qrel when
the document ids are equal and the run's ``section_path`` is a prefix of the
qrel's ``section_path`` (so a broader-section hit satisfies deeper-section
qrels). All functions are pure and deterministic: no randomness, ties broken
by stable input ordering, and scores never depend on qrel input order.

DCG follows the same formulas as orchestrator/eval/runner.py and
eval/retrieval/metrics.py so reported numbers stay comparable:
rank 1 contributes ``relevance``, rank i contributes
``relevance / log2(i)``, and nDCG divides actual DCG by the ideal DCG of
the query's qrels sorted by relevance descending.

Companion assets: ``data/eval/techdocs/qrels.schema.json`` (record schema,
validated by :func:`validate_qrels`).
"""

from __future__ import annotations

import json
import math
import re
from collections import defaultdict
from pathlib import Path
from typing import Any

# Unstable ID shapes: qrels must never reference volatile chunk/page/element
# ordinals such as "doc-1:chunk:1", "doc-1:p0", "doc-1:p0:e5", bare integers
# or "chunk-1"-style tokens. Stable document ids look like
# "python@<commit>#<path>" and stable section paths are heading titles.
_UNSTABLE_ID_RE = re.compile(
    r"^(?:\d+|p\d+|e\d+|(?:chunk|page|element|parent)[-_]\d+)$"
    r"|:(?:chunk|parent):\d+|:p\d+(?::e\d+)?|:e\d+"
)


def _is_unstable_id(value: str) -> bool:
    """True when an identifier encodes a volatile chunk/page/element ordinal."""
    return _UNSTABLE_ID_RE.search(value) is not None


def _prefix_of(run_section: list[str], qrel_section: list[str]) -> bool:
    """True when run_section is a prefix of qrel_section (run may be equal or shallower)."""
    return len(run_section) <= len(qrel_section) and qrel_section[: len(run_section)] == run_section


def _matches(hit: dict[str, Any], qrel: dict[str, Any]) -> bool:
    """A run hit matches a qrel on document_id plus section-path prefix."""
    return hit["document_id"] == qrel["document_id"] and _prefix_of(
        hit["section_path"], qrel["section_path"]
    )


def _dcg(relevances: list[int], k: int) -> float:
    total = 0.0
    for rank, rel in enumerate(relevances[:k], start=1):
        total += rel * _rank_weight(rank)
    return total


def _within_depth(hits: list[dict[str, Any]], k: int) -> list[dict[str, Any]]:
    """Hits whose 1-indexed rank is in [1, k], in rank order (stable)."""
    return [h for h in hits if 1 <= h["rank"] <= k]


def _rank_weight(rank: int) -> float:
    """DCG weight of a rank: 1 at rank 1, 1/log2(rank) after (same as runner)."""
    return 1.0 if rank == 1 else 1.0 / math.log2(rank)


def _recall(rels: list[dict[str, Any]], hits: list[dict[str, Any]], k: int) -> float:
    if not rels or k <= 0:
        return 0.0
    found = 0
    for qrel in rels:
        if any(_matches(hit, qrel) for hit in _within_depth(hits, k)):
            found += 1
    return found / len(rels)


def _mrr(rels: list[dict[str, Any]], hits: list[dict[str, Any]], k: int) -> float:
    if not rels or k <= 0:
        return 0.0
    for hit in _within_depth(hits, k):
        if any(_matches(hit, qrel) for qrel in rels):
            return 1.0 / hit["rank"]
    return 0.0


def _ndcg(rels: list[dict[str, Any]], hits: list[dict[str, Any]], k: int) -> float:
    if not rels or k <= 0:
        return 0.0
    ideal = sorted((qrel["relevance"] for qrel in rels), reverse=True)
    ideal_dcg = _dcg(ideal, k)
    if ideal_dcg <= 0:
        return 0.0
    # Each qrel contributes its relevance exactly once, at the earliest hit
    # that matches it; the gain at a rank is the max over newly matched qrels.
    dcg = 0.0
    matched: set[int] = set()
    for hit in _within_depth(hits, k):
        gain = 0
        for j, qrel in enumerate(rels):
            if j in matched or not _matches(hit, qrel):
                continue
            matched.add(j)
            gain = max(gain, qrel["relevance"])
        dcg += gain * _rank_weight(hit["rank"])
    return dcg / ideal_dcg


def score_run(
    qrels: list[dict[str, Any]], run: list[dict[str, Any]], ks: tuple[int, ...] = (5, 10)
) -> dict[str, float]:
    """Macro-average Recall@k, MRR@K and nDCG@K over queries with qrels.

    ``qrels`` records: ``{query_id, document_id, section_path, relevance, ...}``.
    ``run`` records: ``{query_id, document_id, section_path, rank}`` (rank is
    1-indexed; ties keep input order).

    Returns one ``recall@{k}`` key per k in ``ks`` (sorted, positive, de-duped)
    plus ``mrr@{K}`` and ``ndcg@{K}`` where ``K`` is the largest k. Empty
    inputs return zeros, never errors.
    """
    keys = sorted({int(k) for k in ks if int(k) > 0})
    if not keys:
        return {}
    top_k = max(keys)

    qrels_by_query: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for record in qrels:
        qrels_by_query[record["query_id"]].append(record)
    hits_by_query: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for record in run:
        hits_by_query[record["query_id"]].append(record)

    queries = sorted(qrels_by_query)
    if not queries:
        return {f"recall@{k}": 0.0 for k in keys} | {f"mrr@{top_k}": 0.0, f"ndcg@{top_k}": 0.0}
    recall = {k: 0.0 for k in keys}
    mrr_total = 0.0
    ndcg_total = 0.0
    for query_id in queries:
        rels = sorted(
            qrels_by_query[query_id],
            key=lambda r: (r["document_id"], tuple(r["section_path"]), r["relevance"]),
        )
        hits = sorted(hits_by_query.get(query_id, []), key=lambda h: h["rank"])
        for k in keys:
            recall[k] += _recall(rels, hits, k)
        mrr_total += _mrr(rels, hits, top_k)
        ndcg_total += _ndcg(rels, hits, top_k)

    n = len(queries)
    scores = {f"recall@{k}": recall[k] / n for k in keys}
    scores[f"mrr@{top_k}"] = mrr_total / n
    scores[f"ndcg@{top_k}"] = ndcg_total / n
    return scores


def validate_qrels(
    qrels: list[dict[str, Any]] | str | Path, schema_path: str | Path | None = None
) -> list[str]:
    """Validate qrels records against the JSON schema and stable-ID shape.

    ``qrels`` may be an in-memory list of records, or a path to a JSON array
    file or a JSONL file (one record per line). An unreadable or invalid JSON
    file raises :class:`ValueError`; schema and stable-ID problems are returned
    as a sorted list of issue strings (empty for valid input). ``schema_path``
    defaults to ``data/eval/techdocs/qrels.schema.json`` next to the repo root.
    """
    if isinstance(qrels, (str, Path)):
        qrels = _load_qrels_file(Path(qrels))
    if not isinstance(qrels, list):
        return ["qrels must be a JSON array of records"]
    if not qrels:
        return []

    from jsonschema import Draft202012Validator

    schema = _load_schema(schema_path)
    validator = Draft202012Validator(schema)
    issues: list[str] = []
    for i, record in enumerate(qrels):
        if not isinstance(record, dict):
            issues.append(f"record {i}: expected an object, got {type(record).__name__}")
            continue
        for error in sorted(
            validator.iter_errors(record),
            key=lambda e: (tuple(str(x) for x in e.absolute_path), e.message),
        ):
            issues.append(f"record {i}: {error.message}")
        document_id = record.get("document_id")
        if isinstance(document_id, str) and _is_unstable_id(document_id):
            issues.append(
                f"record {i}: document_id {document_id!r} encodes a volatile chunk/page/element ordinal"
            )
        section_path = record.get("section_path")
        if isinstance(section_path, list):
            for j, element in enumerate(section_path):
                if isinstance(element, str) and _is_unstable_id(element):
                    issues.append(
                        f"record {i}: section_path[{j}] {element!r} encodes a volatile chunk/page/element ordinal"
                    )
    return sorted(issues)


def _load_qrels_file(path: Path) -> list[dict[str, Any]]:
    """Read a JSON array or JSONL file; raises ValueError on unreadable JSON."""
    try:
        text = path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        raise ValueError(f"cannot read qrels file {path}: {exc}") from exc
    try:
        data = json.loads(text)
    except json.JSONDecodeError:
        records: list[dict[str, Any]] = []
        for line_no, line in enumerate(text.splitlines(), start=1):
            line = line.strip()
            if not line:
                continue
            try:
                records.append(json.loads(line))
            except json.JSONDecodeError as exc:
                raise ValueError(f"unreadable JSON in {path} at line {line_no}: {exc}") from exc
        data = records
    if not isinstance(data, list):
        raise ValueError(f"{path}: expected a JSON array of qrel records")
    return data


def _load_schema(schema_path: str | Path | None) -> dict[str, Any]:
    if schema_path is None:
        schema_path = Path(__file__).resolve().parents[2] / "data" / "eval" / "techdocs" / "qrels.schema.json"
    try:
        return json.loads(Path(schema_path).read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f"cannot load qrels schema {schema_path}: {exc}") from exc
