"""ViDoRe visual document retrieval adapter (plan Task 5.2).

The bake-off compares THREE retrieval paths on the same qrels:
  - text-only (existing text index)
  - page visual (visual pilot index)
  - late-interaction over text top-N pages

The adapter only transforms data and calls the deterministic metrics from
eval.retrieval.multimodal_metrics. It never downloads models at import time;
the run script decides whether a GPU encoder is available. Manifest must pin
the ViDoRe snapshot/license/scorer before any real download.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

from eval.retrieval.multimodal_metrics import Hit, Qrel, aggregate, bbox_hit_rate, mrr_at_k, ndcg_at_k, recall_at_k


@dataclass(frozen=True)
class ViDoReManifest:
    name: str
    source_url: str
    revision: str
    license_spdx: str
    scorer: str = "official-vidore"

    def validate(self) -> list[str]:
        issues: list[str] = []
        if not self.source_url.startswith("https://"):
            issues.append("source_url must be https")
        if len(self.revision) < 7 or " " in self.revision:
            issues.append("revision must be a pinned commit-ish identifier")
        if not self.license_spdx:
            issues.append("license_spdx required")
        return issues


@dataclass
class VisualPilotResult:
    path: str  # "text-only" | "page-visual" | "late-interaction"
    ndcg10: float
    recall5: float
    mrr10: float
    bbox_hit: float
    per_query: dict[str, dict[str, float]] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        return {
            "path": self.path,
            "ndcg@10": self.ndcg10,
            "recall@5": self.recall5,
            "mrr@10": self.mrr10,
            "bbox_hit": self.bbox_hit,
            "per_query": self.per_query,
        }


def load_qrels(path: str | Path) -> list[Qrel]:
    """Load qrels from JSON lines: {query_id, document_id, page_id, element_id?, bbox?, relevance?}."""
    qrels: list[Qrel] = []
    for line in Path(path).read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        record = json.loads(line)
        qrels.append(
            Qrel(
                query_id=record["query_id"],
                document_id=record["document_id"],
                page_id=record["page_id"],
                element_id=record.get("element_id", ""),
                bbox=record.get("bbox"),
                relevance=float(record.get("relevance", 1.0)),
            )
        )
    return qrels


def group_by_query(qrels: list[Qrel]) -> dict[str, list[Qrel]]:
    grouped: dict[str, list[Qrel]] = {}
    for qrel in qrels:
        grouped.setdefault(qrel.query_id, []).append(qrel)
    return grouped


def score_path(ranked_by_query: dict[str, list[Hit]], qrels_by_query: dict[str, list[Qrel]], path: str) -> VisualPilotResult:
    """Score one retrieval path: per-query metrics aggregated across queries."""
    ndcg, recall, mrr, bbox = [], [], [], []
    per_query: dict[str, dict[str, float]] = {}
    for query_id, query_qrels in qrels_by_query.items():
        ranked = ranked_by_query.get(query_id, [])
        n = ndcg_at_k(ranked, query_qrels, 10)
        r = recall_at_k(ranked, query_qrels, 5)
        m = mrr_at_k(ranked, query_qrels, 10)
        b = bbox_hit_rate(ranked, query_qrels, 10)
        ndcg.append((query_id, n))
        recall.append((query_id, r))
        mrr.append((query_id, m))
        bbox.append((query_id, b))
        per_query[query_id] = {"ndcg10": n, "recall5": r, "mrr10": m, "bbox_hit": b}
    return VisualPilotResult(
        path=path,
        ndcg10=aggregate(ndcg),
        recall5=aggregate(recall),
        mrr10=aggregate(mrr),
        bbox_hit=aggregate(bbox),
        per_query=per_query,
    )


def compare_paths(
    text_ranked: dict[str, list[Hit]],
    visual_ranked: dict[str, list[Hit]],
    late_ranked: dict[str, list[Hit]],
    qrels: list[Qrel],
) -> list[VisualPilotResult]:
    """Score the three paths; a missing path (empty dict) yields a disabled result."""
    by_query = group_by_query(qrels)
    results = [
        score_path(text_ranked, by_query, "text-only"),
        score_path(visual_ranked, by_query, "page-visual"),
        score_path(late_ranked, by_query, "late-interaction"),
    ]
    return results
