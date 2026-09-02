from __future__ import annotations

import pytest

from orchestrator.rag.modality_policies import (
    get_chunker_modality,
    get_modality_chunk_policy,
    supported_modalities,
)


def test_registry_covers_the_requested_document_modalities() -> None:
    assert {"word", "ppt", "excel", "pdf"}.issubset(set(supported_modalities()))


@pytest.mark.parametrize(
    ("alias", "canonical"),
    [("docx", "word"), ("pptx", "ppt"), ("xlsx", "excel"), ("pdf_page", "pdf")],
)
def test_file_aliases_resolve_to_one_canonical_policy(alias: str, canonical: str) -> None:
    assert get_modality_chunk_policy(alias) == get_modality_chunk_policy(canonical)


def test_canonical_office_policies_expose_the_actual_chunker_route() -> None:
    assert get_chunker_modality("word") == "office_document"
    assert get_chunker_modality("ppt") == "slide"
    assert get_chunker_modality("excel") == "spreadsheet"


def test_office_policies_preserve_structure_and_use_text_index() -> None:
    word = get_modality_chunk_policy("word")
    ppt = get_modality_chunk_policy("ppt")
    excel = get_modality_chunk_policy("excel")

    assert word.chunk_unit == "semantic_block"
    assert ppt.chunk_unit == "slide"
    assert excel.chunk_unit == "worksheet_region"
    assert {word.index_kind, ppt.index_kind, excel.index_kind} == {"text"}
    assert "table" in word.hard_boundaries
    assert "speaker_notes" in ppt.metadata_fields
    assert "formula_dependencies" in excel.metadata_fields


def test_pdf_and_visual_policies_are_physically_separate_from_text() -> None:
    pdf = get_modality_chunk_policy("pdf")
    image = get_modality_chunk_policy("image")

    assert pdf.ingestion_route == "mineru_ocr"
    assert pdf.tika_allowed is False
    assert pdf.chunk_unit == "page_element"
    assert image.index_kind == "visual"
    assert image.physical_index == "knowledge_page_visual_current"


def test_audio_and_video_policies_require_playback_provenance() -> None:
    audio = get_modality_chunk_policy("audio")
    video = get_modality_chunk_policy("video")

    assert audio.chunk_unit == "timestamp_window"
    assert audio.overlap_ms == 1500
    assert "start_ms" in audio.metadata_fields
    assert "end_ms" in audio.metadata_fields
    assert video.chunk_unit == "scene_window"
    assert "frame_refs" in video.metadata_fields
    assert audio.quality_gate == "independent_reference_wer"


def test_embedded_table_formula_and_structured_lanes_have_deterministic_units() -> None:
    table = get_modality_chunk_policy("table")
    formula = get_modality_chunk_policy("formula")
    structured = get_modality_chunk_policy("structured")
    graph = get_modality_chunk_policy("graph")

    assert table.chunk_unit == "header_repeated_row_group"
    assert table.index_kind == "text"
    assert formula.chunk_unit == "dependency_subgraph"
    assert "formula_dependencies" in formula.metadata_fields
    assert structured.chunk_unit == "schema_aware_record_group"
    assert graph.chunk_unit == "entity_relation_neighborhood"
    assert "entity_ids" in graph.metadata_fields


def test_unknown_modality_fails_closed() -> None:
    with pytest.raises(ValueError, match="unsupported modality"):
        get_modality_chunk_policy("archive")
