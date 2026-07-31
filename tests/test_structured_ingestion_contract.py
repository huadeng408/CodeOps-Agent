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
            "chunkSize": 8,
            "chunkOverlap": 1,
            "elements": [
                {
                    "document_id": "doc-1", "element_id": "e1", "type": "text", "text": "alpha beta",
                    "heading_path": ["Guide"], "page_index": 0, "bbox": [0, 0, 10, 10],
                    "parser_name": "mineru", "parser_version": "3.4.4",
                },
                {
                    "document_id": "doc-1", "element_id": "e2", "type": "text", "text": "gamma",
                    "heading_path": ["Guide"], "page_index": 0, "bbox": [0, 10, 10, 20],
                    "parser_name": "mineru", "parser_version": "3.4.4",
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
            "chunkSize": 4,
            "chunkOverlap": 1,
            "elements": [
                {
                    "document_id": "doc-1", "element_id": "e1", "type": "text",
                    "text": "one two three four five six", "page_index": 0, "bbox": [0, 0, 10, 10],
                    "parser_name": "mineru", "parser_version": "3.4.4",
                }
            ],
        }
    )
    response = await IngestionService.__new__(IngestionService).chunk(payload)
    assert [item["text"] for item in response.structuredChunks] == ["one two three four", "four five six"]
    assert [item["overlap_tokens"] for item in response.structuredChunks] == [0, 1]
