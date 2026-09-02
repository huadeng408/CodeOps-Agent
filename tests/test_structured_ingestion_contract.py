import pytest

from orchestrator.rag.ingestion import IngestionService
from orchestrator.rag.models import ChunkRequestPayload, ParseResponsePayload


def test_pdf_parse_response_contains_elements_and_mineru_provenance() -> None:
    response = ParseResponsePayload.model_validate(
        {
            "parsedText": "visible text",
            "documentId": "doc-1",
            "parserName": "mineru",
            "parserVersion": "3.4.4",
            "elements": [
                {
                    "document_id": "doc-1",
                    "element_id": "e1",
                    "type": "text",
                    "text": "visible text",
                    "page_index": 0,
                    "bbox": [0, 0, 10, 10],
                    "parser_name": "mineru",
                    "parser_version": "3.4.4",
                }
            ],
        }
    )
    assert response.elements[0].parser_name == "mineru"
    assert response.sourceSha256 == ""


@pytest.mark.asyncio
async def test_chunk_endpoint_returns_typed_provenance_from_elements() -> None:
    payload = ChunkRequestPayload.model_validate(
        {
            "task": {"file_md5": "doc-1", "file_name": "guide.pdf", "user_id": 1, "stage": "chunk"},
            "text": "alpha beta gamma",
            "documentId": "doc-1",
            "parserName": "mineru",
            "parserVersion": "3.4.4",
            "sourceSha256": "b" * 64,
            "chunkSize": 8,
            "chunkOverlap": 1,
            "elements": [
                {
                    "document_id": "doc-1", "element_id": "e1", "type": "text", "text": "alpha beta",
                    "heading_path": ["Guide"], "page_index": 0, "bbox": [0, 0, 10, 10],
                    "parser_name": "mineru", "parser_version": "3.4.4",
                    "source_sha256": "a" * 64,
                },
                {
                    "document_id": "doc-1", "element_id": "e2", "type": "text", "text": "gamma",
                    "heading_path": ["Guide"], "page_index": 0, "bbox": [0, 10, 10, 20],
                    "parser_name": "mineru", "parser_version": "3.4.4",
                    "source_sha256": "a" * 64,
                },
            ],
        }
    )
    response = await IngestionService.__new__(IngestionService).chunk(payload)
    assert len(response.structuredChunks) == 1
    assert response.structuredChunks[0]["document_id"] == "doc-1"
    assert response.structuredChunks[0]["element_ids"] == ["e1", "e2"]
    assert response.structuredChunks[0]["page_id"] == "doc-1:p0"


@pytest.mark.asyncio
async def test_chunk_endpoint_honors_requested_overlap_for_oversized_text() -> None:
    payload = ChunkRequestPayload.model_validate(
        {
            "task": {"file_md5": "doc-1", "file_name": "guide.pdf", "user_id": 1, "stage": "chunk"},
            "text": "one two three four five six",
            "documentId": "doc-1",
            "parserName": "mineru",
            "parserVersion": "3.4.4",
            "sourceSha256": "b" * 64,
            "chunkSize": 4,
            "chunkOverlap": 1,
            "elements": [
                {
                    "document_id": "doc-1", "element_id": "e1", "type": "text",
                    "text": "one two three four five six", "page_index": 0, "bbox": [0, 0, 10, 10],
                    "parser_name": "mineru", "parser_version": "3.4.4",
                    "source_sha256": "a" * 64,
                }
            ],
        }
    )
    response = await IngestionService.__new__(IngestionService).chunk(payload)
    assert [item["text"] for item in response.structuredChunks] == ["one two three four", "four five six"]
    assert [item["overlap_tokens"] for item in response.structuredChunks] == [0, 1]


@pytest.mark.asyncio
async def test_chunk_endpoint_uses_parser_identity_when_pdf_extension_is_wrong() -> None:
    payload = ChunkRequestPayload.model_validate(
        {
            "task": {"file_md5": "renamed", "file_name": "scan.docx", "user_id": 1, "stage": "chunk"},
            "text": "page evidence",
            "documentId": "renamed",
            "parserName": "mineru",
            "parserVersion": "3.4.4",
            "sourceSha256": "b" * 64,
            "elements": [
                {
                    "document_id": "renamed",
                    "element_id": "p1-e1",
                    "type": "text",
                    "text": "page evidence",
                    "heading_path": ["Page 2"],
                    "page_index": 1,
                    "bbox": [10, 20, 100, 80],
                    "coordinate_system": "page_1000",
                    "parser_name": "mineru",
                    "parser_version": "3.4.4",
                    "source_sha256": "a" * 64,
                }
            ],
        }
    )

    response = await IngestionService.__new__(IngestionService).chunk(payload)

    assert response.structuredChunks[0]["page_id"] == "renamed:p1"


@pytest.mark.asyncio
async def test_chunk_endpoint_rejects_parser_identity_mismatch() -> None:
    payload = ChunkRequestPayload.model_validate(
        {
            "task": {
                "file_md5": "mismatch",
                "file_name": "scan.docx",
                "user_id": 1,
                "stage": "chunk",
            },
            "text": "document evidence",
            "parserName": "mineru",
            "elements": [
                {
                    "document_id": "mismatch",
                    "element_id": "e1",
                    "type": "text",
                    "text": "document evidence",
                    "parser_name": "python-docx",
                    "parser_version": "1.1.2",
                }
            ],
        }
    )

    with pytest.raises(ValueError, match="parser provenance"):
        await IngestionService.__new__(IngestionService).chunk(payload)


@pytest.mark.asyncio
async def test_chunk_endpoint_rejects_non_mineru_pdf_elements_without_declared_parser() -> None:
    payload = ChunkRequestPayload.model_validate(
        {
            "task": {
                "file_md5": "pdf-bypass",
                "file_name": "scan.pdf",
                "user_id": 1,
                "stage": "chunk",
            },
            "text": "untrusted parser output",
            "elements": [
                {
                    "document_id": "pdf-bypass",
                    "element_id": "e1",
                    "type": "text",
                    "text": "untrusted parser output",
                    "parser_name": "tika",
                    "parser_version": "2.9.0",
                }
            ],
        }
    )

    with pytest.raises(ValueError, match="PDF structured chunks require MinerU provenance"):
        await IngestionService.__new__(IngestionService).chunk(payload)


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "element_provenance",
    [
        {},
        {"parser_name": "mineru"},
        {"parser_name": "mineru", "parser_version": "3.4.4"},
    ],
)
async def test_chunk_endpoint_rejects_incomplete_mineru_pdf_provenance(
    element_provenance: dict[str, str],
) -> None:
    payload = ChunkRequestPayload.model_validate(
        {
            "task": {
                "file_md5": "untrusted-pdf",
                "file_name": "untrusted.pdf",
                "user_id": 1,
                "stage": "chunk",
            },
            "text": "untrusted parser output",
            "elements": [
                {
                    "document_id": "untrusted-pdf",
                    "element_id": "e1",
                    "type": "text",
                    "text": "untrusted parser output",
                    **element_provenance,
                }
            ],
        }
    )

    with pytest.raises(ValueError, match="PDF structured chunks require MinerU provenance"):
        await IngestionService.__new__(IngestionService).chunk(payload)


@pytest.mark.asyncio
@pytest.mark.parametrize("file_name", ["scan.pdf", "renamed.docx"])
async def test_chunk_endpoint_rejects_pdf_text_without_element_provenance(
    file_name: str,
) -> None:
    payload = ChunkRequestPayload.model_validate(
        {
            "task": {
                "file_md5": "pdf-text-bypass",
                "file_name": file_name,
                "user_id": 1,
                "stage": "chunk",
            },
            "text": "unbound PDF text",
            "parserName": "mineru",
            "elements": [],
        }
    )

    with pytest.raises(ValueError, match="PDF chunks require MinerU element provenance"):
        await IngestionService.__new__(IngestionService).chunk(payload)


def _valid_mineru_chunk_payload() -> dict[str, object]:
    return {
        "task": {
            "file_md5": "doc-1",
            "file_name": "scan.pdf",
            "user_id": 1,
            "stage": "chunk",
        },
        "text": "first second",
        "documentId": "doc-1",
        "parserName": "mineru",
        "parserVersion": "3.4.4",
        "sourceSha256": "a" * 64,
        "elements": [
            {
                "document_id": "doc-1",
                "element_id": "e1",
                "type": "text",
                "text": "first",
                "parser_name": "mineru",
                "parser_version": "3.4.4",
                "source_sha256": "b" * 64,
            },
            {
                "document_id": "doc-1",
                "element_id": "e2",
                "type": "text",
                "text": "second",
                "parser_name": "mineru",
                "parser_version": "3.4.4",
                "source_sha256": "b" * 64,
            },
        ],
    }


def _valid_native_chunk_payload() -> dict[str, object]:
    return {
        "task": {
            "file_md5": "doc-1",
            "file_name": "metrics.xlsx",
            "user_id": 1,
            "stage": "chunk",
        },
        "text": "metric value",
        "documentId": "doc-1",
        "parserName": "openpyxl",
        "parserVersion": "3.1.5",
        "sourceSha256": "a" * 64,
        "elements": [
            {
                "document_id": "doc-1",
                "element_id": "doc-1:sheet:Metrics:e0",
                "type": "table",
                "text": "metric value",
                "parser_name": "openpyxl",
                "parser_version": "3.1.5",
                "source_sha256": "a" * 64,
                "sheet_name": "Metrics",
                "cell_range": "Metrics!A1:B2",
            }
        ],
    }


@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("mutation", "message"),
    [
        (lambda raw: raw.update(documentId="other-doc"), "document"),
        (lambda raw: raw.update(parserVersion=""), "version"),
        (lambda raw: raw.update(sourceSha256=""), "hash"),
        (
            lambda raw: raw["elements"][0].update(source_sha256="b" * 64),
            "hash",
        ),
    ],
)
async def test_chunk_endpoint_rejects_unbound_native_provenance(
    mutation: object, message: str
) -> None:
    raw = _valid_native_chunk_payload()
    mutation(raw)  # type: ignore[operator]
    payload = ChunkRequestPayload.model_validate(raw)

    with pytest.raises(ValueError, match=message):
        await IngestionService.__new__(IngestionService).chunk(payload)


@pytest.mark.asyncio
async def test_chunk_endpoint_preserves_legacy_text_without_structured_metadata() -> None:
    payload = ChunkRequestPayload.model_validate(
        {
            "task": {
                "file_md5": "legacy-doc",
                "file_name": "notes.txt",
                "user_id": 1,
                "stage": "chunk",
            },
            "text": "legacy text remains supported",
        }
    )

    response = await IngestionService.__new__(IngestionService).chunk(payload)

    assert response.chunks == ["legacy text remains supported"]
    assert response.structuredChunks[0]["document_id"] == "legacy-doc"


@pytest.mark.asyncio
async def test_chunk_endpoint_rejects_foreign_pdf_document_id() -> None:
    raw = _valid_mineru_chunk_payload()
    raw["documentId"] = "other-doc"
    payload = ChunkRequestPayload.model_validate(raw)

    with pytest.raises(ValueError, match="document"):
        await IngestionService.__new__(IngestionService).chunk(payload)


@pytest.mark.asyncio
async def test_chunk_endpoint_rejects_prefixed_mineru_identity() -> None:
    raw = _valid_mineru_chunk_payload()
    raw["parserName"] = "mineru-evil"
    for element in raw["elements"]:  # type: ignore[union-attr]
        element["parser_name"] = "mineru-evil"
    payload = ChunkRequestPayload.model_validate(raw)

    with pytest.raises(ValueError, match="MinerU"):
        await IngestionService.__new__(IngestionService).chunk(payload)


@pytest.mark.asyncio
async def test_chunk_endpoint_rejects_element_parser_version_mismatch() -> None:
    raw = _valid_mineru_chunk_payload()
    raw["elements"][1]["parser_version"] = "3.4.5"  # type: ignore[index]
    payload = ChunkRequestPayload.model_validate(raw)

    with pytest.raises(ValueError, match="version"):
        await IngestionService.__new__(IngestionService).chunk(payload)


@pytest.mark.asyncio
async def test_chunk_endpoint_rejects_inconsistent_element_artifact_hash() -> None:
    raw = _valid_mineru_chunk_payload()
    raw["elements"][1]["source_sha256"] = "c" * 64  # type: ignore[index]
    payload = ChunkRequestPayload.model_validate(raw)

    with pytest.raises(ValueError, match="hash"):
        await IngestionService.__new__(IngestionService).chunk(payload)


@pytest.mark.asyncio
async def test_chunk_endpoint_keeps_raw_and_mineru_artifact_hashes_distinct() -> None:
    payload = ChunkRequestPayload.model_validate(_valid_mineru_chunk_payload())

    response = await IngestionService.__new__(IngestionService).chunk(payload)

    assert response.structuredChunks[0]["source_sha256"] == "b" * 64


@pytest.mark.asyncio
async def test_chunk_endpoint_preserves_stable_corpus_document_id_separate_from_file_md5() -> None:
    raw = _valid_native_chunk_payload()
    stable_document_id = "go@0123456789abcdef0123456789abcdef01234567:docs/guide.xlsx"
    raw["task"]["file_md5"] = "file-md5-not-document-id"  # type: ignore[index]
    raw["task"]["document_id"] = stable_document_id  # type: ignore[index]
    raw["documentId"] = stable_document_id
    for element in raw["elements"]:  # type: ignore[union-attr]
        element["document_id"] = stable_document_id
        element["element_id"] = stable_document_id + ":sheet:Metrics:e0"

    payload = ChunkRequestPayload.model_validate(raw)
    response = await IngestionService.__new__(IngestionService).chunk(payload)

    assert response.structuredChunks[0]["document_id"] == stable_document_id


@pytest.mark.asyncio
async def test_text_chunk_preserves_stable_corpus_document_id() -> None:
    document_id = "python@0123456789abcdef0123456789abcdef01234567:Doc/guide.rst"
    payload = ChunkRequestPayload.model_validate(
        {
            "task": {
                "file_md5": "file-md5-not-document-id",
                "document_id": document_id,
                "file_name": "guide.rst",
                "user_id": 1,
                "stage": "chunk",
            },
            "text": "Stable corpus identity remains attached to text chunks.",
        }
    )

    response = await IngestionService.__new__(IngestionService).chunk(payload)

    assert response.structuredChunks[0]["document_id"] == document_id
