"""The pinned public DocVQA page associations must retain their limits."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path


DATASET_DIR = (
    Path(__file__).resolve().parents[2]
    / "data"
    / "eval"
    / "multimodal"
    / "docvqa-public-dev-page-qrels-v1"
)


def test_public_dev_page_qrels_are_hash_bound_and_never_represented_as_gold() -> None:
    manifest = json.loads((DATASET_DIR / "manifest.json").read_text(encoding="utf-8"))
    qrels_path = DATASET_DIR / "qrels.jsonl"
    rows = [json.loads(line) for line in qrels_path.read_text(encoding="utf-8").splitlines()]

    assert manifest["status"] == "PUBLIC_DEV_PAGE_QRELS"
    assert manifest["dataset"]["id"] == "vidore/docvqa_test_subsampled"
    assert manifest["dataset"]["revision"] == "49bf8f13e13c41dd8cdb0cae5314e31c1da1e0d6"
    assert manifest["dataset"]["license_spdx"] == "MIT"
    assert manifest["limitations"] == {
        "bbox_available": False,
        "human_reviewed_by_project": False,
        "hidden_holdout": False,
        "scoreable_for_page_retrieval_only": True,
    }
    assert manifest["qrels_count"] == 120 == len(rows)
    assert manifest["qrels_sha256"] == hashlib.sha256(qrels_path.read_bytes()).hexdigest()
    assert all(set(row) == {"query_id", "query", "document_id", "page_id", "source"} for row in rows)
    assert {row["source"] for row in rows} == {"vidore/docvqa_test_subsampled"}
    assert len({row["query_id"] for row in rows}) == 120
