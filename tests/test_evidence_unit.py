from __future__ import annotations

import pytest
from pydantic import ValidationError

from orchestrator.rag.chunking import StructuredChunk
from orchestrator.rag.evidence import EvidenceCoordinates, EvidenceUnit


SOURCE_SHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"


def _payload() -> dict[str, object]:
    return {
        "document_id": "doc-1",
        "child_id": "doc-1:chunk:1",
        "parent_id": "doc-1:parent:1",
        "source_sha256": SOURCE_SHA256,
        "parser_name": "mineru",
        "parser_version": "3.4.4",
        "modality": "visual",
        "coordinates": {
            "page_id": "doc-1:p2",
            "page_span": [2, 2],
            "element_ids": ["doc-1:e2"],
            "bbox_refs": ["doc-1:e2:0,0,10,10"],
        },
    }


def test_evidence_unit_requires_provenance_identity_and_coordinates() -> None:
    unit = EvidenceUnit.model_validate(_payload())

    assert unit.source_sha256 == SOURCE_SHA256
    assert unit.child_id == "doc-1:chunk:1"
    assert unit.coordinates == EvidenceCoordinates(
        page_id="doc-1:p2",
        page_span=(2, 2),
        element_ids=("doc-1:e2",),
        bbox_refs=("doc-1:e2:0,0,10,10",),
    )

    missing_source = _payload()
    del missing_source["source_sha256"]
    with pytest.raises(ValidationError, match="source_sha256"):
        EvidenceUnit.model_validate(missing_source)


@pytest.mark.parametrize("forbidden_field", ["answer", "qrels", "relevance"])
def test_evidence_unit_rejects_answer_and_label_fields(forbidden_field: str) -> None:
    payload = _payload()
    payload[forbidden_field] = "must not enter retrieval evidence"

    with pytest.raises(ValidationError, match=forbidden_field):
        EvidenceUnit.model_validate(payload)


def test_evidence_unit_adapts_existing_structured_chunk_without_identity_drift() -> None:
    chunk = StructuredChunk(
        document_id="doc-1",
        chunk_id="doc-1:chunk:1",
        parent_chunk_id="doc-1:parent:1",
        text="source text",
        embedding_text="source text",
        source_sha256=SOURCE_SHA256,
        page_id="doc-1:p2",
        page_span=[2, 2],
        element_ids=["doc-1:e2"],
        element_types=["table"],
        bbox_refs=["doc-1:e2:0,0,10,10"],
        parser_name="openpyxl",
        parser_version="3.1.5",
    )

    unit = EvidenceUnit.from_structured_chunk(chunk)

    assert unit.modality == "table"
    assert unit.document_id == chunk.document_id
    assert unit.child_id == chunk.chunk_id
    assert unit.parent_id == chunk.parent_chunk_id
    assert unit.source_sha256 == chunk.source_sha256
    assert unit.coordinates.page_span == tuple(chunk.page_span)


def test_audio_evidence_coordinates_round_trip_through_json() -> None:
    unit = EvidenceUnit(
        document_id="audio-1",
        child_id="audio-1:audio:0-25000",
        parent_id="audio-1:audio:transcript",
        source_sha256=SOURCE_SHA256,
        parser_name="faster-whisper",
        parser_version="1.2.1",
        modality="audio",
        coordinates=EvidenceCoordinates(
            asset_refs=("audio-1",),
            start_ms=0,
            end_ms=25_000,
        ),
    )

    restored = EvidenceUnit.model_validate(unit.model_dump(mode="json"))

    assert restored == unit
