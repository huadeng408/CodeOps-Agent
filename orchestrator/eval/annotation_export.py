"""Reviewed qrels annotation worksheet export (plan Task 2).

Text/multimodal qrels are produced by human reviewers from a locked source
corpus. This module turns a qrels JSONL file into a blind annotation
worksheet: a seeded (seed=0, deterministic) shuffle of qrels rows, each
tagged with the reviewer and a redacted reviewer hash. Gold relevance (the
answer) is never exported — a reviewer must judge each row without seeing
it, so answers cannot leak into the annotation round.

Review workflow:

1. export_worksheet()  -> blind worksheet JSONL for the reviewer.
2. Human review:       -> reviewer records relevance per row.
3. count_reviewed()    -> rows with a non-empty reviewer_hash.
4. validate_counts()   -> total / per-source / zh breakdown.

Everything is deterministic given seed=0: the same qrels file always
produces the same worksheet, independent of environment or dict ordering.
"""

from __future__ import annotations

import hashlib
import json
import random
from pathlib import Path
from typing import Any

# Fixed seed makes the annotation shuffle reproducible across runs.
ANNOTATION_SEED = 0
# Fields that carry the gold answer; never exported into a worksheet.
ANSWER_FIELDS = frozenset({"relevance"})
# Length of the redacted reviewer hash in hex characters.
HASH_LENGTH = 16


def instance_id(row: dict[str, Any]) -> str:
    """Canonical stable identity of one qrel row.

    Built exclusively from fields that already live inside the qrel row
    (query_id, document_id, section_path), so the redacted reviewer hash
    never leaks plaintext identifiers beyond the qrel itself.
    """
    section = ">".join(row.get("section_path") or [])
    return "|".join([str(row["query_id"]), str(row["document_id"]), section])


def _redact_hash(row: dict[str, Any], reviewer: str) -> str:
    """sha256(instance_id + reviewer) truncated to 16 hex chars."""
    digest = hashlib.sha256((instance_id(row) + reviewer).encode("utf-8")).hexdigest()
    return digest[:HASH_LENGTH]


def export_worksheet(
    qrels_path: str | Path,
    out_path: str | Path,
    reviewer: str = "reviewer-1",
) -> Path:
    """Write a blind annotation worksheet and return its path.

    The worksheet is a seeded (seed=0) shuffle of the qrels rows. Every row
    keeps its stable identity and metadata fields (query_id, document_id,
    section_path, source_id, source_commit, language, query_type,
    evidence_type), is tagged with the reviewer and a redacted reviewer
    hash, and never carries the gold relevance.
    """
    rows = _load_rows(qrels_path)
    rng = random.Random(ANNOTATION_SEED)
    rng.shuffle(rows)
    worksheet = []
    for row in rows:
        out_row = {key: value for key, value in row.items() if key not in ANSWER_FIELDS}
        out_row["reviewer"] = reviewer
        out_row["reviewer_hash"] = _redact_hash(row, reviewer)
        worksheet.append(out_row)
    out = Path(out_path)
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(
        "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in worksheet),
        encoding="utf-8",
    )
    return out


def validate_counts(qrels_path: str | Path) -> dict[str, Any]:
    """Report {"total", "per_source", "zh_count"} over a qrels JSONL file.

    A row without a source_id is a hard error: it cannot be attributed to a
    source and must be fixed before review.
    """
    rows = _load_rows(qrels_path)
    per_source: dict[str, int] = {}
    zh_count = 0
    for row in rows:
        source = row.get("source_id")
        if not isinstance(source, str) or not source:
            raise ValueError(
                f"qrel row missing source_id: {row.get('query_id', '<unknown>')}"
            )
        per_source[source] = per_source.get(source, 0) + 1
        if row.get("language") == "zh":
            zh_count += 1
    return {"total": len(rows), "per_source": per_source, "zh_count": zh_count}


def count_reviewed(qrels_path: str | Path) -> int:
    """Number of qrel rows that carry a non-empty reviewer_hash."""
    return sum(1 for row in _load_rows(qrels_path) if row.get("reviewer_hash"))


def _load_rows(path: str | Path) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        row = json.loads(line)
        if not isinstance(row, dict):
            raise ValueError(f"qrels row is not an object: {line[:80]}")
        rows.append(row)
    return rows
