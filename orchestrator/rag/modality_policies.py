"""Explicit chunk and provenance policies for each supported input modality."""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class ModalityChunkPolicy:
    """Stable contract consumed by chunkers, indexers, and evaluators."""

    name: str
    chunk_unit: str
    target_tokens: int
    overlap_tokens: int = 0
    overlap_ms: int = 0
    hard_boundaries: tuple[str, ...] = ()
    metadata_fields: tuple[str, ...] = ()
    index_kind: str = "text"
    physical_index: str = "knowledge_base_current"
    ingestion_route: str = "native"
    tika_allowed: bool = False
    quality_gate: str = "human_qrels"
    # ``None`` means this policy is not handled by the structured chunker yet.
    chunker_modality: str | None = None


_POLICIES: dict[str, ModalityChunkPolicy] = {
    "text": ModalityChunkPolicy(
        "text", "semantic_section", 512, 64,
        hard_boundaries=("table", "equation", "code", "image"),
        metadata_fields=("section_path", "page_id", "element_ids", "bbox_refs"),
        chunker_modality="text",
    ),
    "word": ModalityChunkPolicy(
        "word", "semantic_block", 512, 64,
        hard_boundaries=("table", "image", "caption"),
        metadata_fields=("element_id", "heading_path", "asset_refs", "source_sha256"),
        ingestion_route="native_office", tika_allowed=True,
        chunker_modality="office_document",
    ),
    "ppt": ModalityChunkPolicy(
        "ppt", "slide", 768,
        hard_boundaries=("shape", "table", "image", "speaker_notes"),
        metadata_fields=("slide_index", "element_id", "bbox", "reading_order", "asset_refs", "speaker_notes"),
        ingestion_route="native_office", tika_allowed=True,
        chunker_modality="slide",
    ),
    "excel": ModalityChunkPolicy(
        "excel", "worksheet_region", 768,
        hard_boundaries=("worksheet", "formula", "named_range"),
        metadata_fields=("sheet_name", "cell_range", "formula_dependencies", "named_ranges", "formula_cache_present"),
        ingestion_route="native_office", tika_allowed=True,
        quality_gate="deterministic_formula_validation_and_human_qrels",
        chunker_modality="spreadsheet",
    ),
    "table": ModalityChunkPolicy(
        "table", "header_repeated_row_group", 768,
        hard_boundaries=("header", "row_group", "merged_cell"),
        metadata_fields=("header", "row_range", "column_range", "caption", "source_sha256"),
        chunker_modality="table",
    ),
    "formula": ModalityChunkPolicy(
        "formula", "dependency_subgraph", 384,
        hard_boundaries=("formula", "dependency_edge", "named_range"),
        metadata_fields=("formula_dependencies", "cell_range", "cached_value", "calculation_state"),
        quality_gate="deterministic_recalculation_and_human_qrels",
        chunker_modality="formula",
    ),
    "pdf": ModalityChunkPolicy(
        "pdf", "page_element", 512, 64,
        hard_boundaries=("page", "table", "equation", "image"),
        metadata_fields=("page_id", "element_id", "bbox", "asset_refs", "source_sha256"),
        ingestion_route="mineru_ocr", tika_allowed=False,
        chunker_modality="pdf",
    ),
    "image": ModalityChunkPolicy(
        "image", "visual_page", 0,
        hard_boundaries=("page", "crop", "bbox"),
        metadata_fields=("page_id", "element_id", "bbox", "asset_sha256", "model_revision"),
        index_kind="visual", physical_index="knowledge_page_visual_current",
        ingestion_route="rendered_asset", quality_gate="page_and_bbox_qrels",
        chunker_modality="visual_page",
    ),
    "audio": ModalityChunkPolicy(
        "audio", "timestamp_window", 384, overlap_ms=1500,
        hard_boundaries=("speaker_turn", "timestamp"),
        metadata_fields=("audio_id", "start_ms", "end_ms", "speaker_id", "source_sha256"),
        quality_gate="independent_reference_wer",
    ),
    "video": ModalityChunkPolicy(
        "video", "scene_window", 512,
        hard_boundaries=("scene", "speaker_turn", "keyframe"),
        metadata_fields=("video_id", "start_ms", "end_ms", "frame_refs", "bbox_refs"),
        quality_gate="multimodal_time_range_qrels",
    ),
    "structured": ModalityChunkPolicy(
        "structured", "schema_aware_record_group", 512,
        hard_boundaries=("schema", "record", "primary_key"),
        metadata_fields=("schema_id", "record_ids", "field_names", "source_sha256"),
        quality_gate="deterministic_query_validation",
    ),
    "graph": ModalityChunkPolicy(
        "graph", "entity_relation_neighborhood", 512,
        hard_boundaries=("entity", "relation", "connected_component"),
        metadata_fields=("entity_ids", "relation_ids", "graph_version", "source_sha256"),
        quality_gate="path_and_relation_qrels",
    ),
}

_ALIASES = {
    "md": "text", "txt": "text", "html": "text", "code": "text",
    "docx": "word", "word": "word",
    "pptx": "ppt", "powerpoint": "ppt",
    "xlsx": "excel", "xls": "excel", "spreadsheet": "excel",
    "table": "table", "formula": "formula", "equation": "formula",
    "pdf_page": "pdf", "application/pdf": "pdf",
    "page": "image", "visual_page": "image", "png": "image", "jpg": "image",
    "wav": "audio", "mp3": "audio",
    "mp4": "video",
    "json": "structured", "csv": "structured", "structured": "structured",
    "knowledge_graph": "graph", "graph": "graph",
}


def supported_modalities() -> tuple[str, ...]:
    return tuple(_POLICIES)


def get_modality_chunk_policy(modality: str) -> ModalityChunkPolicy:
    normalized = modality.strip().lower()
    canonical = _ALIASES.get(normalized, normalized if normalized in _POLICIES else None)
    if canonical is None:
        raise ValueError(f"unsupported modality: {modality}")
    return _POLICIES[canonical]


def get_chunker_modality(modality: str) -> str | None:
    """Return the implementation route for a canonical or aliased modality."""
    return get_modality_chunk_policy(modality).chunker_modality
