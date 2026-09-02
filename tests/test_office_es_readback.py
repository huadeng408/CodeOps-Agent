from __future__ import annotations

import json

from orchestrator.rag.chunking import StructuredChunk
from scripts.rag.verify_office_es_readback import (
    chunk_to_index_document,
    readback_gate_status,
)


def test_chunk_projection_preserves_provenance_and_spreadsheet_coordinates() -> None:
    chunk = StructuredChunk(
        document_id="metrics.xlsx",
        chunk_id="metrics.xlsx:chunk:1",
        parent_chunk_id="metrics.xlsx:parent:1",
        text="Sheet: Metrics\nRange: A1:C40\nRevenue | 42",
        embedding_text="Metrics\nRevenue | 42",
        source_sha256="a" * 64,
        section_path=["Metrics"],
        page_id="metrics.xlsx:p0",
        page_span=[0, 0],
        element_ids=["metrics.xlsx:sheet:Metrics:e1"],
        element_types=["table"],
        token_count=7,
        parser_name="openpyxl",
        parser_version="3.1.5",
        corpus_generation="office-pilot-v1",
        sheet_name="Metrics",
        cell_range="Metrics!A1:C40",
    )

    document = chunk_to_index_document(chunk, "office-pilot-index")

    assert document["_id"] == "metrics.xlsx:chunk:1"
    assert document["_source"]["document_id"] == "metrics.xlsx"
    assert document["_source"]["element_ids"] == ["metrics.xlsx:sheet:Metrics:e1"]
    assert document["_source"]["sheet_name"] == "Metrics"
    assert document["_source"]["cell_range"] == "Metrics!A1:C40"
    assert "visual_vector" not in document["_source"]


def test_run_quotes_chunk_id_for_elasticsearch_readback(monkeypatch, tmp_path) -> None:
    import scripts.rag.verify_office_es_readback as readback

    chunk = StructuredChunk(
        document_id="office fixture.docx",
        chunk_id="office fixture.docx:chunk/1",
        parent_chunk_id="office fixture.docx:parent:1",
        text="release package health traffic",
        embedding_text="release package health traffic",
        source_sha256="b" * 64,
        element_ids=["office fixture.docx:e1"],
        element_types=["paragraph"],
        parser_name="python-docx",
        parser_version="1.1.2",
    )
    monkeypatch.setattr(
        readback,
        "_load_chunks",
        lambda: ([chunk], {"office fixture.docx": {"chunks": 1}}),
    )

    request_paths: list[str] = []

    def fake_request(base_url, method, path, payload=None):
        request_paths.append(path)
        if method == "GET" and path.endswith("/_doc/office%20fixture.docx%3Achunk%2F1"):
            return {"found": True, "_source": chunk_to_index_document(chunk, "office-index")["_source"]}
        if method == "GET" and path.endswith("/_count"):
            return {"count": 1}
        if method == "GET" and path.endswith("/_mapping"):
            return {"office-index": {"mappings": {"properties": {}}}}
        if method == "POST" and path.endswith("/_search"):
            return {"hits": {"hits": [{"_source": {"document_id": chunk.document_id}}]}}
        return {}

    monkeypatch.setattr(readback, "_request", fake_request)

    class BulkResponse:
        def __enter__(self):
            return self

        def __exit__(self, exc_type, exc_value, traceback):
            return False

        def read(self):
            return json.dumps({"errors": False}).encode("utf-8")

    monkeypatch.setattr(readback.urllib.request, "urlopen", lambda request, timeout=60: BulkResponse())

    receipt = readback.run("http://127.0.0.1:9200", tmp_path / "receipt", "office-index")

    assert receipt["readback_chunk_count"] == 1
    assert "/office-index/_doc/office%20fixture.docx%3Achunk%2F1" in request_paths


def test_readback_gate_does_not_promote_incomplete_queries_or_counts() -> None:
    assert (
        readback_gate_status(
            document_count=2,
            expected_document_count=2,
            readback_chunk_count=2,
            expected_coordinate_rows=1,
            coordinate_rows_checked=1,
            query_checks=[{"expected_document_returned": True}],
            bulk_errors=False,
        )
        == "VERIFIED_READBACK"
    )
    assert (
        readback_gate_status(
            document_count=1,
            expected_document_count=2,
            readback_chunk_count=2,
            expected_coordinate_rows=1,
            coordinate_rows_checked=1,
            query_checks=[{"expected_document_returned": True}],
            bulk_errors=False,
        )
        == "OFFICE_INTEGRATION_PARTIAL"
    )
    assert (
        readback_gate_status(
            document_count=2,
            expected_document_count=2,
            readback_chunk_count=2,
            expected_coordinate_rows=1,
            coordinate_rows_checked=1,
            query_checks=[{"expected_document_returned": False}],
            bulk_errors=False,
        )
        == "OFFICE_INTEGRATION_PARTIAL"
    )
