"""Shared retrieval-benchmark base (plan Task 7.3).

BEIR, MIRACL and BRIGHT all produce per-query ranked corpus ids scored by
their official scorers. The base class enforces the uniform contract: data
conversion and scoring are separated, scoring is offline-runnable from the
cache, and every adapter has a --dry-run and small-sample path. Adapters
never fake the official scorer.
"""

from __future__ import annotations

import json
import os
from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from eval.retrieval.metrics import RetrievalHit, RetrievalQrel


@dataclass
class RetrievalBenchmark(ABC):
    name: str

    # -- Data loading (offline from cache) ---------------------------------

    @abstractmethod
    def load_offline(self, cache_root: Path) -> None:
        """Load the snapshot from the offline cache; must not hit the network."""

    @abstractmethod
    def qrels(self) -> list[RetrievalQrel]:
        """Return the qrels for scoring."""

    @abstractmethod
    def queries(self) -> list[str]:
        """Return query ids in deterministic order."""

    # -- Scoring (official scorer, offline) --------------------------------

    @abstractmethod
    def score_predictions(self, ranked: dict[str, list[str]], output_dir: Path) -> dict[str, Any]:
        """Run the OFFICIAL scorer on predictions; returns metrics dict."""

    # -- Shared helpers -----------------------------------------------------

    def to_hits(self, ranked: dict[str, list[str]]) -> list[RetrievalHit]:
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

    def smoke_instances(self, query_ids: list[str], limit: int) -> list[str]:
        """Small deterministic sample for pipeline validation."""
        return query_ids[:limit]

    def write_predictions(self, ranked: dict[str, list[str]], output_dir: Path) -> Path:
        output_dir.mkdir(parents=True, exist_ok=True)
        path = output_dir / f"{self.name}-predictions.jsonl"
        with path.open("w", encoding="utf-8") as handle:
            for query_id, corpus_ids in ranked.items():
                for rank, corpus_id in enumerate(corpus_ids):
                    handle.write(
                        json.dumps(
                            {
                                "query_id": query_id,
                                "corpus_id": corpus_id,
                                "rank": rank + 1,
                            },
                            ensure_ascii=False,
                        )
                        + "\n"
                    )
        return path

    def require_pinned(self) -> None:
        from eval.datasets.loader import load_dataset_manifest, validate_dataset_records

        records = load_dataset_manifest()
        by_name = {record["name"]: record for record in records}
        record = by_name.get(self.name)
        if record is None:
            raise ValueError(f"dataset {self.name} not present in eval/datasets/manifest.yaml")
        issues = validate_dataset_records([record])
        if issues:
            raise ValueError(f"dataset {self.name} not pinned: {'; '.join(issues)}")


def cache_root() -> Path:
    return Path(os.environ.get("CODE_AGENT_EVAL_CACHE", "eval/cache"))
