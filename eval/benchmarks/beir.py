"""BEIR adapter (plan Task 7.3).

The adapter only transforms data into the shared eval containers and calls
the official BEIR scorer (beir.rank or the reference metrics). It never
re-implements scoring. Download is separated from scoring: scoring must be
re-runnable offline from cached artifacts.
"""

from __future__ import annotations

import json
import os
import csv
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from eval.retrieval.metrics import RetrievalHit, RetrievalQrel

#: Trace/pin capabilities this benchmark exercises.  BEIR *is* retrieval, so the
#: RAG spans and the corpus/qrels/index pins are all genuinely required: a
#: retrieval score that does not pin the gold set and the physical index it
#: searched is not reproducible and not comparable to anything.
TRACE_CAPABILITIES: tuple[str, ...] = ("rag",)


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
    data_root = root
    if not (data_root / "queries.jsonl").exists():
        candidates = [child for child in root.iterdir() if child.is_dir()] if root.is_dir() else []
        if len(candidates) == 1 and (candidates[0] / "queries.jsonl").exists():
            data_root = candidates[0]

    queries_path = data_root / "queries.jsonl"
    if queries_path.exists():
        for line in queries_path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                record = json.loads(line)
                dataset.queries[record["_id"]] = record.get("text", "")

    corpus_path = data_root / "corpus.jsonl"
    if corpus_path.exists():
        for line in corpus_path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                record = json.loads(line)
                dataset.corpus[record["_id"]] = {"title": record.get("title", ""), "text": record.get("text", "")}

    qrels_path = data_root / "qrels.jsonl"
    if qrels_path.exists():
        for line in qrels_path.read_text(encoding="utf-8").splitlines():
            if line.strip():
                record = json.loads(line)
                dataset.qrels.setdefault(record["query_id"], {})[record["corpus_id"]] = int(record.get("score", 1))
    else:
        official_qrels = data_root / "qrels" / "test.tsv"
        if official_qrels.exists():
            with official_qrels.open(encoding="utf-8", newline="") as handle:
                for record in csv.DictReader(handle, delimiter="\t"):
                    dataset.qrels.setdefault(record["query-id"], {})[record["corpus-id"]] = int(record["score"])
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


def require_pinned(name: str, dataset_cache: str | Path | None = None) -> dict[str, Any]:
    """Refuse to run when the dataset snapshot is not pinned in the manifest."""
    from eval.datasets.loader import (
        dataset_pin_payload,
        load_dataset_manifest,
        validate_dataset_records,
        verify_dataset_artifacts,
    )

    records = load_dataset_manifest()
    by_name = {record["name"]: record for record in records}
    record = by_name.get(name)
    if record is None:
        raise ValueError(f"dataset {name} not present in eval/datasets/manifest.yaml")
    issues = validate_dataset_records([record])
    if not issues and dataset_cache is not None:
        issues.extend(verify_dataset_artifacts(record, dataset_cache))
    if issues:
        raise ValueError(f"dataset {name} not pinned: {'; '.join(issues)}")
    return dataset_pin_payload(record)


def official_scorer_command(name: str) -> list[str]:
    """The official scorer invocation for this dataset (thin adapter only)."""
    return ["python", "-m", "eval.retrieval.official_scorers", name]
