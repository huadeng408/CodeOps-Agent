"""BEIR adapter (plan Task 7.3).

The adapter only transforms data into the shared eval containers and calls
the official BEIR scorer (beir.rank or the reference metrics). It never
re-implements scoring. Download is separated from scoring: scoring must be
re-runnable offline from cached artifacts.
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from eval.retrieval.metrics import RetrievalHit, RetrievalQrel


@dataclass
class BeirDataset:
    name: str
    queries: dict[str, str] = field(default_factory=dict)
    corpus: dict[str, dict[str, str]] = field(default_factory=dict)
    qrels: dict[str, dict[str, int]] = field(default_factory=dict)


def load_offline(path: str | Path) -> BeirDataset:
    """Load a BEIR dataset snapshot from the offline cache directory.

    Layout (mirrors BEIR's own jsonl layout):
      cache/beir/<name>/queries.jsonl   {_id, text}
      cache/beir/<name>/corpus.jsonl    {_id, title, text}
      cache/beir/<name>/qrels.jsonl     {query_id, corpus_id, score}
    """
    root = Path(path)
    dataset = BeirDataset(name=root.name)

    queries_path = root / "queries.jsonl"
    if queries_path.exists():
        for line in queries_path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                record = json.loads(line)
                dataset.queries[record["_id"]] = record.get("text", "")

    corpus_path = root / "corpus.jsonl"
    if corpus_path.exists():
        for line in corpus_path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                record = json.loads(line)
                dataset.corpus[record["_id"]] = {"title": record.get("title", ""), "text": record.get("text", "")}

    qrels_path = root / "qrels.jsonl"
    if qrels_path.exists():
        for line in qrels_path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                record = json.loads(line)
                dataset.qrels.setdefault(record["query_id"], {})[record["corpus_id"]] = int(record.get("score", 1))
    return dataset


def to_qrels(dataset: BeirDataset) -> list[RetrievalQrel]:
    qrels: list[RetrievalQrel] = []
    for query_id, corpus_scores in dataset.qrels.items():
        for corpus_id, score in corpus_scores.items():
            if score > 0:
                qrels.append(
                    RetrievalQrel(
                        query_id=query_id,
                        document_id=corpus_id,
                        relevance=float(score),
                    )
                )
    return qrels


def to_hits(ranked: dict[str, list[str]]) -> list[RetrievalHit]:
    """Convert {query_id: [corpus_id, ...]} rankings into RetrievalHit list."""
    hits: list[RetrievalHit] = []
    for query_id, corpus_ids in ranked.items():
        for rank, corpus_id in enumerate(corpus_ids):
            hits.append(
                RetrievalHit(
                    query_id=query_id,
                    document_id=corpus_id,
                    score=1.0 / (rank + 1),
                )
            )
    return hits


def dry_run_instances(dataset: BeirDataset, limit: int) -> list[str]:
    """Return the first `limit` query ids for a smoke run."""
    return list(dataset.queries.keys())[:limit]


def offline_cache_root() -> Path:
    """Cache root; set CODE_AGENT_EVAL_CACHE to override."""
    return Path(os.environ.get("CODE_AGENT_EVAL_CACHE", "eval/cache"))


def require_pinned(name: str) -> None:
    """Refuse to run when the dataset snapshot is not pinned in the manifest."""
    from eval.datasets.loader import load_dataset_manifest, validate_dataset_records

    records = load_dataset_manifest()
    by_name = {record["name"]: record for record in records}
    record = by_name.get(name)
    if record is None:
        raise ValueError(f"dataset {name} not present in eval/datasets/manifest.yaml")
    issues = validate_dataset_records([record])
    if issues:
        raise ValueError(f"dataset {name} not pinned: {'; '.join(issues)}")


def official_scorer_command(name: str) -> list[str]:
    """The official scorer invocation for this dataset (thin adapter only)."""
    return ["python", "-m", "eval.retrieval.official_scorers", name]
