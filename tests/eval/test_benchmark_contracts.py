"""Retrieval benchmark adapter contract tests (plan Task 7.3)."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.benchmarks.base import RetrievalBenchmark
from eval.benchmarks.beir import (
    BeirDataset,
    dry_run_instances,
    load_offline,
    to_hits,
    to_qrels,
)
from eval.benchmarks.bright import BrightBenchmark
from eval.benchmarks.miracl import MiraclBenchmark
from eval.retrieval.metrics import RetrievalQrel


def _write_beir_snapshot(root: Path) -> Path:
    root.mkdir(parents=True, exist_ok=True)
    (root / "queries.jsonl").write_text(
        json.dumps({"_id": "q1", "text": "query one"}) + "\n"
        + json.dumps({"_id": "q2", "text": "query two"}) + "\n",
        encoding="utf-8",
    )
    (root / "corpus.jsonl").write_text(
        json.dumps({"_id": "c1", "title": "T1", "text": "corpus one"}) + "\n"
        + json.dumps({"_id": "c2", "title": "T2", "text": "corpus two"}) + "\n",
        encoding="utf-8",
    )
    (root / "qrels.jsonl").write_text(
        json.dumps({"query_id": "q1", "corpus_id": "c1", "score": 1}) + "\n"
        + json.dumps({"query_id": "q2", "corpus_id": "c2", "score": 1}) + "\n",
        encoding="utf-8",
    )
    return root


def test_beir_offline_load(tmp_path: Path) -> None:
    root = _write_beir_snapshot(tmp_path / "beir-nfcorpus")
    dataset = load_offline(root)
    assert dataset.name == "beir-nfcorpus"
    assert set(dataset.queries) == {"q1", "q2"}
    assert dataset.corpus["c1"]["title"] == "T1"
    assert dataset.qrels["q1"] == {"c1": 1}


def test_beir_to_qrels_filters_zero_scores() -> None:
    dataset = BeirDataset(name="x")
    dataset.qrels = {"q1": {"c1": 1, "c2": 0}}
    qrels = to_qrels(dataset)
    assert len(qrels) == 1
    assert qrels[0].document_id == "c1"


def test_beir_to_hits_rank_scoring() -> None:
    hits = to_hits({"q1": ["c1", "c2"]})
    assert hits[0].score == 1.0
    assert hits[1].score == 0.5


def test_beir_dry_run_instances() -> None:
    dataset = BeirDataset(name="x")
    dataset.queries = {"q1": "", "q2": "", "q3": ""}
    assert dry_run_instances(dataset, 2) == ["q1", "q2"]


def test_miracl_and_bright_share_uniform_contract() -> None:
    mi = MiraclBenchmark(language="zh")
    br = BrightBenchmark()
    assert mi.name == "miracl-zh"
    assert br.name == "bright"
    # Both must be RetrievalBenchmark instances with the abstract methods.
    for bench in (mi, br):
        assert isinstance(bench, RetrievalBenchmark)
        assert callable(bench.load_offline)
        assert callable(bench.qrels)
        assert callable(bench.score_predictions)


def test_benchmark_require_pinned_blocks_placeholder(tmp_path: Path) -> None:
    # The shipped dataset manifest uses all-zero placeholders, so every
    # adapter must refuse to run until the datasets are pinned.
    bench = MiraclBenchmark(language="zh")
    with pytest.raises(ValueError) as exc:
        bench.require_pinned()
    assert "not pinned" in str(exc.value)


def test_benchmark_write_predictions(tmp_path: Path) -> None:
    bench = BrightBenchmark()
    path = bench.write_predictions({"q1": ["c1", "c2"]}, tmp_path)
    lines = path.read_text(encoding="utf-8").splitlines()
    assert len(lines) == 2
    assert json.loads(lines[0]) == {"query_id": "q1", "corpus_id": "c1", "rank": 1}


def test_benchmark_smoke_instances_deterministic() -> None:
    bench = BrightBenchmark()
    ids = ["q1", "q2", "q3", "q4"]
    assert bench.smoke_instances(ids, 2) == ["q1", "q2"]
    assert bench.smoke_instances(ids, 10) == ids


def test_qrels_are_retrieval_qrel_instances() -> None:
    dataset = BeirDataset(name="x")
    dataset.qrels = {"q1": {"c1": 1}}
    for qrel in to_qrels(dataset):
        assert isinstance(qrel, RetrievalQrel)
