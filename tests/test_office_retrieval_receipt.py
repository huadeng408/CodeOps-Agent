from __future__ import annotations

from pathlib import Path


def test_office_fixture_retrieval_receipt_is_real_and_coordinate_safe(tmp_path: Path) -> None:
    from scripts.rag.verify_office_retrieval import run_office_fixture_retrieval

    receipt = run_office_fixture_retrieval(tmp_path / "receipt.json")

    assert receipt["status"] == "FIXTURE_CONTRACT_ONLY"
    assert receipt["query_count"] == 5
    assert receipt["metrics"]["recall@5"] == 1.0
    assert receipt["metrics"]["mrr@10"] == 1.0
    assert receipt["coordinate_checks"]["xlsx_ranges"] == 2
    assert receipt["coordinate_checks"]["all_preserved"] is True
    assert receipt["qrels_source"] == "fixture-derived; requires independent human review before quality use"
