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
from typing import TYPE_CHECKING, Any

from eval.retrieval.metrics import RetrievalHit, RetrievalQrel

if TYPE_CHECKING:  # import-time only — avoids circular imports with eval.adapter
    from eval.adapter import AgentAdapter, EvalInstance, EvalResult


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

    def require_pinned(self, dataset_cache: Path | None = None) -> dict[str, Any]:
        from eval.datasets.loader import (
            dataset_pin_payload,
            load_dataset_manifest,
            validate_dataset_records,
            verify_dataset_artifacts,
        )

        records = load_dataset_manifest()
        by_name = {record["name"]: record for record in records}
        record = by_name.get(self.name)
        if record is None:
            raise ValueError(f"dataset {self.name} not present in eval/datasets/manifest.yaml")
        issues = validate_dataset_records([record])
        if not issues and dataset_cache is not None:
            issues.extend(verify_dataset_artifacts(record, dataset_cache))
        if issues:
            raise ValueError(f"dataset {self.name} not pinned: {'; '.join(issues)}")
        return dataset_pin_payload(record)


def cache_root() -> Path:
    return Path(os.environ.get("CODE_AGENT_EVAL_CACHE", "eval/cache"))


# ---------------------------------------------------------------------------
# Agent benchmarks (unified harness lifecycle: prepare -> solve -> score)
# ---------------------------------------------------------------------------


class AgentBenchmark(ABC):
    """Base class for agent-based benchmarks (SWE-bench, terminal-bench, ...).

    The unified lifecycle per instance is:

        1. ``prepare(instance, workspace)``         - set up the workspace
           (clone the repo, install deps, ...).
        2. ``solve(instance, workspace, adapter)``   - run the agent and
           produce an :class:`EvalResult`; never returns ``None``.
        3. ``score(result, instance, workspace)``    - invoke the OFFICIAL
           scorer and return the metrics dict merged into the prediction
           artifact.

    Concrete subclasses must override :attr:`pins` so every run is pinned to
    an exact dataset revision and scorer.
    """

    name: str = ""

    @abstractmethod
    def prepare(self, instance: "EvalInstance", workspace: Path) -> None:
        """Prepare the workspace for one instance (clone repo, checkout, ...)."""

    @abstractmethod
    def solve(
        self,
        instance: "EvalInstance",
        workspace: Path,
        adapter: "AgentAdapter",
    ) -> "EvalResult":
        """Run the agent on the prepared workspace and return the result."""

    @abstractmethod
    def score(
        self,
        result: "EvalResult",
        instance: "EvalInstance",
        workspace: Path,
    ) -> dict[str, Any]:
        """Invoke the official scorer; returns the metrics dict for the artifact."""

    @property
    def pins(self) -> dict[str, str]:
        """Immutable benchmark pins for reproducibility.

        Subclasses override with the exact ``dataset_name``,
        ``dataset_revision`` and ``scorer_name`` used by the run.
        """
        return {
            "benchmark": self.name,
            "dataset_name": "",
            "dataset_revision": "",
            "scorer_name": "",
        }

    def validate_pins(self) -> list[str]:
        """Return the list of required pin keys that are missing or empty.

        An empty list means the pins are complete.
        """
        missing: list[str] = []
        for key in ("benchmark", "dataset_name", "dataset_revision", "scorer_name"):
            if not (self.pins.get(key) or "").strip():
                missing.append(key)
        return missing
