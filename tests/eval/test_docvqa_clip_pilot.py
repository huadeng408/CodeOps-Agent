from __future__ import annotations

from eval.scripts.run_docvqa_clip_pilot import (
    build_qrels,
    rank_ocr_text,
    visual_index_mapping,
    visual_pilot_index_name,
    select_public_subset,
)


def test_select_public_subset_is_stable_and_does_not_use_dataset_order() -> None:
    rows = [
        {"questionId": "z", "query": "z query", "docId": 3, "page": "p3"},
        {"questionId": "a", "query": "a query", "docId": 1, "page": "p1"},
        {"questionId": "m", "query": "m query", "docId": 2, "page": "p2"},
    ]
    selected = select_public_subset(rows, 2)
    assert [item["questionId"] for item in selected] == ["a", "m"]


def test_build_qrels_uses_official_docvqa_identifiers() -> None:
    rows = [{"questionId": "q-1", "query": "find total", "docId": 42, "page": "page-0042.png"}]
    qrels = build_qrels(rows)
    assert qrels == [{
        "query_id": "docvqa:q-1",
        "query": "find total",
        "document_id": "docvqa:42",
        "page_id": "docvqa:42:page-0042.png",
        "source": "vidore/docvqa_test_subsampled",
    }]


def test_visual_pilot_index_is_separate_and_commit_named() -> None:
    index = visual_pilot_index_name("20260814")
    assert index == "knowledge_page_visual_pilot_clip_3d74acf9_20260814"
    mapping = visual_index_mapping()
    props = mapping["mappings"]["properties"]
    assert props["visual_vector"]["dims"] == 512
    assert "text_content" not in props


def test_visual_pilot_index_accepts_explicit_receipt_suffix() -> None:
    assert visual_pilot_index_name("20260814", "public120") == "knowledge_page_visual_pilot_clip_3d74acf9_20260814_public120"


def test_rank_ocr_text_returns_stable_bm25_ranking() -> None:
    qrels = [{"query_id": "q1", "query": "purchase order number"}]
    pages = [
        {"document_id": "d2", "page_id": "p2", "text": "purchase order number 123"},
        {"document_id": "d1", "page_id": "p1", "text": "purchase order number 456"},
        {"document_id": "d3", "page_id": "p3", "text": "unrelated content"},
    ]

    ranked = rank_ocr_text(qrels, pages)

    assert [hit["page_id"] for hit in ranked] == ["p1", "p2", "p3"]
    assert ranked[0]["score"] == ranked[1]["score"]
    assert ranked[-1]["score"] == 0.0
