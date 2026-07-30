from orchestrator.rag.models import ParseResponsePayload


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

