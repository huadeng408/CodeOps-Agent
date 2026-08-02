"""BRIGHT adapter (plan Task 7.3) — reasoning-intensive retrieval.

Mirrors the BEIR base contract. BRIGHT's official scoring must run against
the official repo's evaluation script; the adapter only converts formats.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

from eval.benchmarks.base import RetrievalBenchmark
from eval.benchmarks.beir import load_offline as _load_beir_offline
from eval.retrieval.metrics import RetrievalQrel


class BrightBenchmark(RetrievalBenchmark):
    def __init__(self) -> None:
        super().__init__(name="bright")
        self._data: Any = None

    def load_offline(self, cache_root: Path) -> None:
        self._data = _load_beir_offline(cache_root / self.name)

    def qrels(self) -> list[RetrievalQrel]:
        qrels: list[RetrievalQrel] = []
        for query_id, corpus_scores in self._data.qrels.items():
            for corpus_id, score in corpus_scores.items():
                if score > 0:
                    qrels.append(RetrievalQrel(query_id=query_id, document_id=corpus_id, relevance=float(score)))
        return qrels

    def queries(self) -> list[str]:
        return list(self._data.queries.keys())

    def score_predictions(self, ranked: dict[str, list[str]], output_dir: Path) -> dict[str, Any]:
        path = self.write_predictions(ranked, output_dir)
        return {
            "dataset": self.name,
            "predictions": str(path),
            "scorer": "official-bright",
            "note": "run official BRIGHT evaluator on the predictions file",
        }
