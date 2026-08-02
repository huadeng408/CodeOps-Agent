"""ViDoRe adapter and bake-off comparison tests (plan Task 5.2)."""

from __future__ import annotations

import json
import tempfile
from pathlib import Path

from eval.benchmarks.vidore import (
    ViDoReManifest,
    compare_paths,
    load_qrels,
    score_path,
)
from eval.retrieval.multimodal_metrics import Hit, Qrel


def test_manifest_validation() -> None:
    good = ViDoReManifest(
        name="vidore-benchmark",
        source_url="https://huggingface.co/datasets/illuin-tech/vidore-benchmark-500k",
        revision="abcdef1234567890",
        license_spdx="CC-BY-4.0",
    )
    assert good.validate() == []
    bad = ViDoReManifest(name="x", source_url="ftp://insecure", revision="main", license_spdx="")
    issues = bad.validate()
    assert len(issues) == 3


def test_load_qrels_json_lines(tmp_path: Path) -> None:
    path = tmp_path / "qrels.jsonl"
    path.write_text(
        json.dumps({"query_id": "q1", "document_id": "doc-1", "page_id": "doc-1:p3", "bbox": [0, 0, 10, 10]}) + "\n"
        + json.dumps({"query_id": "q2", "document_id": "doc-2", "page_id": "doc-2:p1"}) + "\n",
        encoding="utf-8",
    )
    qrels = load_qrels(path)
    assert len(qrels) == 2
    assert qrels[0].page_id == "doc-1:p3"
    assert qrels[0].bbox == [0, 0, 10, 10]
    assert qrels[1].bbox is None


def test_score_path_aggregates_per_query() -> None:
    ranked = {
        "q1": [Hit(document_id="doc-1", page_id="doc-1:p1")],
    }
    qrels = [Qrel(query_id="q1", document_id="doc-1", page_id="doc-1:p1")]
    result = score_path(ranked, {"q1": qrels}, "text-only")
    assert result.path == "text-only"
    assert result.ndcg10 == 1.0
    assert result.recall5 == 1.0
    assert result.mrr10 == 1.0
    assert "q1" in result.per_query


def test_compare_paths_three_way() -> None:
    qrels = [Qrel(query_id="q1", document_id="doc-1", page_id="doc-1:p1")]
    text = {"q1": [Hit(document_id="doc-1", page_id="doc-1:p1")]}
    visual = {"q1": [Hit(document_id="doc-1", page_id="doc-1:p1")]}
    late = {}  # disabled path
    results = compare_paths(text, visual, late, qrels)
    assert [r.path for r in results] == ["text-only", "page-visual", "late-interaction"]
    assert results[0].ndcg10 == 1.0
    assert results[1].ndcg10 == 1.0
    # A path with no ranking data scores 0 (reported, not fabricated).
    assert results[2].ndcg10 == 0.0
