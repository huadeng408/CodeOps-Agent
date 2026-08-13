from __future__ import annotations

import json

import pytest

from eval.scripts.run_docvqa_colsmol_late_pilot import (
    ADAPTER_MODEL_ID,
    ADAPTER_REVISION,
    BASE_MODEL_ID,
    BASE_REVISION,
    select_text_candidates,
    validate_source_receipt,
)


def test_colsmol_late_runner_pins_adapter_and_base_commits() -> None:
    assert ADAPTER_MODEL_ID == "vidore/colSmol-256M"
    assert ADAPTER_REVISION == "a59110fdf114638b8018e6c9a018907e12f14855"
    assert BASE_MODEL_ID == "vidore/ColSmolVLM-Instruct-256M-base"
    assert BASE_REVISION == "99ca96f1f6b95b3a69e6abef74a2416cb738fed0"


def test_validate_source_receipt_requires_frozen_public_visual_receipt(tmp_path) -> None:
    receipt = tmp_path / "receipt"
    receipt.mkdir()
    (receipt / "manifest.json").write_text(json.dumps({"kind": "other"}), encoding="utf-8")

    with pytest.raises(ValueError, match="public_visual_pilot"):
        validate_source_receipt(receipt)


def test_validate_source_receipt_requires_qrels_and_pages(tmp_path) -> None:
    receipt = tmp_path / "receipt"
    receipt.mkdir()
    (receipt / "manifest.json").write_text(json.dumps({"kind": "public_visual_pilot"}), encoding="utf-8")

    with pytest.raises(ValueError, match="qrels.public.jsonl"):
        validate_source_receipt(receipt)


def test_select_text_candidates_is_query_local_stable_and_top_n_bounded() -> None:
    ranked = [
        {"query_id": "q1", "page_id": "p3", "score": 0.2},
        {"query_id": "q1", "page_id": "p2", "score": 0.8},
        {"query_id": "q1", "page_id": "p1", "score": 0.8},
        {"query_id": "q2", "page_id": "p4", "score": 1.0},
    ]

    selected = select_text_candidates(ranked, top_n=2)

    assert selected == {"q1": ["p1", "p2"], "q2": ["p4"]}
