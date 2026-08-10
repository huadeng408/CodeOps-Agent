"""MIRACL adapter (plan Task 7.3) — multilingual retrieval, zh split focus.

Mirrors the BEIR base contract; the official MIRACL scorer (miracl official
evaluation script) is the only scoring authority.
"""

from __future__ import annotations

from pathlib import Path
from typing import Any

from eval.benchmarks.base import RetrievalBenchmark
from eval.retrieval.metrics import RetrievalQrel
from eval.benchmarks.beir import load_offline as _load_beir_offline

#: MIRACL is multilingual retrieval: RAG spans and corpus/qrels/index pins are
#: required for the same reason as BEIR.
TRACE_CAPABILITIES: tuple[str, ...] = ("rag",)


class MiraclBenchmark(RetrievalBenchmark):
    def __init__(self, language: str = "zh") -> None:
        super().__init__(name=f"miracl-{language}")
        self.language = language
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
        # Thin adapter: the official MIRACL evaluator consumes the
        # predictions file. Implementation is wired when the dataset is
        # pinned and cached; the contract stays uniform.
        path = self.write_predictions(ranked, output_dir)
        return {
            "dataset": self.name,
            "predictions": str(path),
            "scorer": "official-miracl",
            "note": "run official MIRACL evaluator on the predictions file",
        }
