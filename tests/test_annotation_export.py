"""Reviewed qrels annotation worksheet tests (plan Task 2).

The exporter turns a locked qrels JSONL file into a blind annotation
worksheet: a seeded (seed=0) deterministic shuffle of qrels rows, each tagged
with a reviewer and a redacted reviewer hash (sha256 of instance_id +
reviewer, 16 hex chars). Gold relevance (the answer) is never exported — a
reviewer must judge each row without seeing it.
"""

from __future__ import annotations

import hashlib
import json
import re
from pathlib import Path

from orchestrator.eval.annotation_export import (
    count_reviewed,
    export_worksheet,
    instance_id,
    validate_counts,
)

# The six locked corpus sources (corpus/sources.yaml, spec §3).
SEED_SOURCES = ["go", "python", "git", "docker", "kubernetes", "postgresql"]

REVIEWER = "reviewer-1"
PLACEHOLDER_COMMIT = "0" * 40

# The seed qrels shipped with the workflow (data/eval/techdocs/).
QRELS_SEED = (
    Path(__file__).resolve().parents[1] / "data" / "eval" / "techdocs" / "qrels.text.jsonl"
)


def _identity(row: dict) -> tuple[str, str, tuple[str, ...]]:
    """Stable identity tuple of one qrel row (query, doc, section path)."""
    return (row["query_id"], row["document_id"], tuple(row.get("section_path") or []))


def _sample_rows() -> list[dict]:
    """One seed-shaped placeholder row per source; half are zh queries."""
    rows = []
    for i, source in enumerate(SEED_SOURCES):
        rows.append(
            {
                "query_id": f"{source}-0001",
                "source_id": source,
                "source_commit": PLACEHOLDER_COMMIT,
                "document_id": f"{source}/{PLACEHOLDER_COMMIT}/doc/example.md",
                "section_path": ["Guide", "Examples"],
                "relevance": 1,
                "language": "zh" if i % 2 == 0 else "en",
                "query_type": "concept",
                "evidence_type": "text",
                "reviewer_hash": "",
            }
        )
    return rows


def _write_qrels(path: Path, rows: list[dict]) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in rows),
        encoding="utf-8",
    )
    return path


def _read_rows(path: Path) -> list[dict]:
    return [json.loads(line) for line in path.read_text(encoding="utf-8").splitlines()]


def test_export_worksheet_is_deterministic(tmp_path: Path) -> None:
    qrels = _write_qrels(tmp_path / "qrels.jsonl", _sample_rows())
    first = export_worksheet(qrels, tmp_path / "worksheet-1.jsonl", reviewer=REVIEWER)
    second = export_worksheet(qrels, tmp_path / "worksheet-2.jsonl", reviewer=REVIEWER)
    assert first.read_bytes() == second.read_bytes()


def test_worksheet_rows_carry_reviewer_and_redacted_hash(tmp_path: Path) -> None:
    qrels = _write_qrels(tmp_path / "qrels.jsonl", _sample_rows())
    out = export_worksheet(qrels, tmp_path / "worksheet.jsonl", reviewer=REVIEWER)
    rows = _read_rows(out)
    assert len(rows) == len(_sample_rows())
    for row in rows:
        # Reviewer attribution is plaintext per row.
        assert row["reviewer"] == REVIEWER
        # Reviewer hash is a redacted 16-hex digest of instance_id + reviewer.
        assert re.fullmatch(r"[0-9a-f]{16}", row["reviewer_hash"])
        expected = hashlib.sha256(
            (instance_id(row) + REVIEWER).encode("utf-8")
        ).hexdigest()[:16]
        assert row["reviewer_hash"] == expected


def test_worksheet_never_exports_answers_or_new_plaintext_ids(tmp_path: Path) -> None:
    qrels = _write_qrels(tmp_path / "qrels.jsonl", _sample_rows())
    src = _read_rows(qrels)
    out = export_worksheet(qrels, tmp_path / "worksheet.jsonl", reviewer=REVIEWER)
    rows = _read_rows(out)
    # The shuffle is a permutation of the qrels, not a resample.
    assert sorted(r["query_id"] for r in rows) == sorted(r["query_id"] for r in src)
    by_id = {_identity(r): r for r in src}
    assert len(by_id) == len(src)
    for row in rows:
        # Gold relevance (the answer) is never exported.
        assert "relevance" not in row
        # No plaintext fields beyond reviewer/reviewer_hash are introduced;
        # reviewer_hash already exists (empty) in the qrel and is overwritten.
        assert set(row) - set(by_id[_identity(row)]) <= {"reviewer", "reviewer_hash"}
        assert {"reviewer", "reviewer_hash"} <= set(row)
        # The redacted hash covers every plaintext field the row carries.
        covered = instance_id(row) + row["reviewer"]
        assert row["reviewer_hash"] == hashlib.sha256(
            covered.encode("utf-8")
        ).hexdigest()[:16]


def test_validate_counts_reports_total_per_source_and_zh(tmp_path: Path) -> None:
    qrels = _write_qrels(tmp_path / "qrels.jsonl", _sample_rows())
    counts = validate_counts(qrels)
    assert counts["total"] == 6
    assert counts["per_source"] == {s: 1 for s in SEED_SOURCES}
    # Half the placeholder rows are Chinese queries over English documents.
    assert counts["zh_count"] == 3


def test_validate_counts_aggregates_multiple_rows_per_source(tmp_path: Path) -> None:
    rows = _sample_rows() + [dict(_sample_rows()[0], query_id="go-0002")]
    counts = validate_counts(_write_qrels(tmp_path / "qrels.jsonl", rows))
    assert counts["total"] == 7
    assert counts["per_source"]["go"] == 2
    assert counts["per_source"]["python"] == 1
    assert counts["zh_count"] == 4  # go-0002 is also a zh placeholder row


def test_count_reviewed_counts_only_rows_with_hashes(tmp_path: Path) -> None:
    rows = _sample_rows()
    rows[0]["reviewer_hash"] = "a" * 16
    rows[2]["reviewer_hash"] = "b" * 16
    qrels = _write_qrels(tmp_path / "qrels.jsonl", rows)
    assert count_reviewed(qrels) == 2
    # Unreviewed seed rows (empty hash) do not count.
    assert count_reviewed(_write_qrels(tmp_path / "seed.jsonl", _sample_rows())) == 0


def test_seed_qrels_file_shape() -> None:
    """The shipped seed file is exactly one placeholder row per source."""
    rows = _read_rows(QRELS_SEED)
    assert len(rows) == 6
    assert sorted(r["source_id"] for r in rows) == sorted(SEED_SOURCES)
    assert all(r["relevance"] == 1 for r in rows)
    assert all(r["reviewer_hash"] == "" for r in rows)
    assert all(r["evidence_type"] == "text" for r in rows)
    # Stable IDs: source_id + pinned commit + source path + section path.
    for row in rows:
        assert row["source_commit"] == PLACEHOLDER_COMMIT
        assert row["document_id"].startswith(f"{row['source_id']}/{PLACEHOLDER_COMMIT}/")
        assert isinstance(row["section_path"], list) and row["section_path"]


def test_seed_qrels_file_validates() -> None:
    counts = validate_counts(QRELS_SEED)
    assert counts["total"] == 6
    assert counts["per_source"] == {s: 1 for s in SEED_SOURCES}
    assert counts["zh_count"] == 3  # half Chinese queries over English docs
    assert count_reviewed(QRELS_SEED) == 0  # seed rows await human review
