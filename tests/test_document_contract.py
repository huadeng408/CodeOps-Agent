"""Frozen Document/Element/Chunk contract tests (design spec §3).

The JSON field names and validation rules must match the Go side
(internal/model/document_contract.go, structured_chunk.go) exactly —
tests/fixtures/document_contract.json is the shared golden fixture.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from pydantic import ValidationError

from orchestrator.rag.chunking import chunk_elements
from orchestrator.rag.elements import Element
from orchestrator.rag.schemas.document_contract import DocumentContract

FIXTURE = Path(__file__).parent / "fixtures" / "document_contract.json"

EXPECTED_GO_FIELDS = {
    "document_id",
    "source_id",
    "source_uri",
    "source_sha256",
    "mime",
    "parser_name",
    "parser_version",
    "parser_backend",
    "license_id",
    "corpus_generation",
}


def _valid_document() -> dict:
    return json.loads(FIXTURE.read_text(encoding="utf-8"))


def test_document_contract_rejects_missing_source_sha256() -> None:
    payload = _valid_document()
    del payload["source_sha256"]
    with pytest.raises(ValidationError) as exc:
        DocumentContract.model_validate(payload)
    assert "source_sha256" in str(exc.value)


def test_document_contract_rejects_blank_parser_version() -> None:
    payload = _valid_document()
    payload["parser_version"] = "   "
    with pytest.raises(ValidationError) as exc:
        DocumentContract.model_validate(payload)
    assert "parser_version" in str(exc.value)


def test_document_contract_round_trips_all_fields() -> None:
    doc = DocumentContract.model_validate(_valid_document())
    dumped = doc.model_dump()
    assert set(dumped.keys()) == EXPECTED_GO_FIELDS
    assert dumped["source_sha256"] == _valid_document()["source_sha256"]
    assert dumped["corpus_generation"] == "techdocs-2026-07-30-v1"


def test_document_contract_matches_go_golden_json() -> None:
    """The golden fixture must parse identically in Python and Go."""
    doc = DocumentContract.model_validate(_valid_document())
    assert set(doc.model_dump().keys()) == EXPECTED_GO_FIELDS
    # JSON serialization must keep the exact Go json tags.
    serialized = json.loads(doc.model_dump_json())
    assert set(serialized.keys()) == EXPECTED_GO_FIELDS


def _sample_element() -> Element:
    return Element(
        document_id="doc-1",
        element_id="doc-1:e1",
        reading_order=1,
        type="text",
        page_index=3,
        text="Hello world",
        parser_name="mineru",
        parser_version="3.4.4",
        source_sha256="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    )


def test_structured_chunk_inherits_source_sha256() -> None:
    chunks = chunk_elements([_sample_element()], child_tokens=6, parent_tokens=20)
    assert len(chunks) == 1
    assert chunks[0].source_sha256 == _sample_element().source_sha256
