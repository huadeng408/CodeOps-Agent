from __future__ import annotations

from typing import TYPE_CHECKING, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator

if TYPE_CHECKING:
    from .chunking import StructuredChunk


Modality = Literal["text", "visual", "table", "formula", "audio"]


class EvidenceCoordinates(BaseModel):
    """Source coordinates required to turn a retrieval result into a citation."""

    model_config = ConfigDict(extra="forbid", frozen=True)

    page_id: str = ""
    page_span: tuple[int, ...] = ()
    element_ids: tuple[str, ...] = ()
    bbox_refs: tuple[str, ...] = ()
    asset_refs: tuple[str, ...] = ()
    sheet_name: str = ""
    cell_range: str = ""
    start_ms: int | None = None
    end_ms: int | None = None

    @field_validator("page_span")
    @classmethod
    def page_span_is_empty_or_a_two_page_range(cls, value: tuple[int, ...]) -> tuple[int, ...]:
        if value and len(value) != 2:
            raise ValueError("page_span must be empty or contain exactly two page numbers")
        return value


class EvidenceUnit(BaseModel):
    """Immutable, modality-neutral provenance for a retrievable source unit."""

    model_config = ConfigDict(extra="forbid", frozen=True)

    document_id: str = Field(min_length=1)
    child_id: str = Field(min_length=1)
    parent_id: str = Field(min_length=1)
    source_sha256: str = Field(pattern=r"^[0-9a-f]{64}$")
    parser_name: str = Field(min_length=1)
    parser_version: str = Field(min_length=1)
    modality: Modality
    coordinates: EvidenceCoordinates
    model_id: str = ""
    model_version: str = ""

    @classmethod
    def from_structured_chunk(cls, chunk: StructuredChunk) -> EvidenceUnit:
        return cls(
            document_id=chunk.document_id,
            child_id=chunk.chunk_id,
            parent_id=chunk.parent_chunk_id,
            source_sha256=chunk.source_sha256,
            parser_name=chunk.parser_name,
            parser_version=chunk.parser_version,
            modality=_modality_for(chunk.element_types),
            coordinates=EvidenceCoordinates(
                page_id=chunk.page_id,
                page_span=tuple(chunk.page_span),
                element_ids=tuple(chunk.element_ids),
                bbox_refs=tuple(chunk.bbox_refs),
                asset_refs=tuple(chunk.asset_refs),
                sheet_name=chunk.sheet_name,
                cell_range=chunk.cell_range,
            ),
        )


def _modality_for(element_types: list[str]) -> Modality:
    known = set(element_types)
    if "table" in known:
        return "table"
    if "equation" in known:
        return "formula"
    if "image" in known:
        return "visual"
    return "text"
