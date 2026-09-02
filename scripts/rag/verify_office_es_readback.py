"""Index real Office chunks into an isolated Elasticsearch pilot index.

This verifies persistence and retrieval metadata only. Fixture-derived queries
remain contract evidence and are not a production quality evaluation.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Mapping
from pathlib import Path
from typing import Any

ROOT = Path(__file__).parents[2]
sys.path.insert(0, str(ROOT))

from orchestrator.rag.chunking import StructuredChunk, chunk_elements_for_modality
from orchestrator.rag.native_documents import parse_docx_bytes, parse_pptx_bytes
from orchestrator.rag.spreadsheet import parse_xlsx_bytes

FIXTURES = ROOT / "tests" / "fixtures" / "multimodal"
DEFAULT_INDEX = "knowledge_office_multimodal_pilot_20260828"
DEFAULT_OUTPUT = ROOT / "eval_results" / "multimodal-rag" / "current-head-20260828-office-es-readback"


def chunk_to_index_document(chunk: StructuredChunk, index: str) -> dict[str, Any]:
    """Project a structured chunk into the text-index document contract."""
    if not chunk.document_id or not chunk.chunk_id or not chunk.element_ids:
        raise ValueError("Office chunk requires document_id, chunk_id and element_ids")
    if not chunk.source_sha256 or len(chunk.source_sha256) != 64:
        raise ValueError("Office chunk requires a SHA-256 source identity")
    source = {
        "document_id": chunk.document_id,
        "source_sha256": chunk.source_sha256,
        "chunk_id": chunk.chunk_id,
        "parent_chunk_id": chunk.parent_chunk_id,
        "text_content": chunk.text,
        "embedding_text": chunk.embedding_text,
        "section_path": chunk.section_path,
        "page_id": chunk.page_id,
        "page_span": chunk.page_span,
        "element_ids": chunk.element_ids,
        "element_types": chunk.element_types,
        "bbox_refs": chunk.bbox_refs,
        "asset_refs": chunk.asset_refs,
        "sheet_name": chunk.sheet_name,
        "cell_range": chunk.cell_range,
        "token_count": chunk.token_count,
        "tokenizer_id": chunk.tokenizer_id,
        "parser_name": chunk.parser_name,
        "parser_version": chunk.parser_version,
        "corpus_generation": chunk.corpus_generation,
        "target_index": index,
    }
    return {"_id": chunk.chunk_id, "_source": source}


def text_index_mapping() -> dict[str, Any]:
    keyword = (
        "document_id", "source_sha256", "chunk_id", "parent_chunk_id", "page_id",
        "element_ids", "element_types", "bbox_refs", "asset_refs", "sheet_name",
        "cell_range", "tokenizer_id", "parser_name", "parser_version",
        "corpus_generation", "target_index",
    )
    properties: dict[str, Any] = {
        "text_content": {"type": "text"},
        "embedding_text": {"type": "text"},
        "section_path": {"type": "keyword"},
        "page_span": {"type": "integer"},
        "token_count": {"type": "integer"},
    }
    properties.update({field: {"type": "keyword"} for field in keyword})
    return {"mappings": {"properties": properties}}


def readback_gate_status(
    *,
    document_count: Any,
    expected_document_count: int,
    readback_chunk_count: int,
    expected_coordinate_rows: int,
    coordinate_rows_checked: int,
    query_checks: list[Mapping[str, Any]],
    bulk_errors: bool,
) -> str:
    """Return a truthful status from every readback gate.

    A receipt is ``VERIFIED_READBACK`` only when the indexed document count,
    per-chunk readback, spreadsheet coordinates, query expectations, and bulk
    response all agree.  Partial observations are useful diagnostics but must
    never be promoted to a verified claim.
    """
    if (
        bulk_errors
        or not isinstance(document_count, int)
        or document_count != expected_document_count
        or readback_chunk_count != expected_document_count
        or coordinate_rows_checked != expected_coordinate_rows
        or not query_checks
        or not all(item.get("expected_document_returned") is True for item in query_checks)
    ):
        return "OFFICE_INTEGRATION_PARTIAL"
    return "VERIFIED_READBACK"


def _current_git_sha() -> str:
    result = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=ROOT,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=False,
    )
    return result.stdout.strip() if result.returncode == 0 else "unknown"


def _request(base_url: str, method: str, path: str, payload: Any | None = None) -> dict[str, Any]:
    data = json.dumps(payload, ensure_ascii=False).encode("utf-8") if payload is not None else None
    request = urllib.request.Request(base_url.rstrip("/") + path, data=data, method=method)
    if payload is not None:
        request.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            return json.loads(response.read().decode("utf-8") or "{}")
    except urllib.error.HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")[:1000]
        raise RuntimeError(f"Elasticsearch {method} {path} failed: HTTP {exc.code} {detail}") from exc


def _load_chunks() -> tuple[list[StructuredChunk], dict[str, Any]]:
    specs = (
        ("office_chunk_fixture.docx", parse_docx_bytes, "office_document"),
        ("office_chunk_fixture.pptx", parse_pptx_bytes, "slide"),
        ("office_chunk_fixture.xlsx", parse_xlsx_bytes, "spreadsheet"),
    )
    chunks: list[StructuredChunk] = []
    fixtures: dict[str, Any] = {}
    for name, parser, modality in specs:
        source = FIXTURES / name
        artifact = parser(source.read_bytes(), document_id=name)
        parsed = chunk_elements_for_modality(artifact.elements, modality=modality)
        chunks.extend(parsed)
        fixtures[name] = {
            "sha256": hashlib.sha256(source.read_bytes()).hexdigest(),
            "parser": artifact.parser_name,
            "parser_version": artifact.parser_version,
            "elements": len(artifact.elements),
            "chunks": len(parsed),
        }
    return chunks, fixtures


def run(es_url: str, output_dir: Path, index: str = DEFAULT_INDEX) -> dict[str, Any]:
    if output_dir.exists():
        raise RuntimeError(f"output directory already exists: {output_dir}")
    chunks, fixtures = _load_chunks()
    _request(es_url, "PUT", f"/{index}", text_index_mapping())
    bulk_lines: list[str] = []
    for chunk in chunks:
        document = chunk_to_index_document(chunk, index)
        bulk_lines.extend([json.dumps({"index": {"_index": index, "_id": document["_id"]}}), json.dumps(document["_source"], ensure_ascii=False)])
    bulk_request = urllib.request.Request(
        es_url.rstrip("/") + "/_bulk",
        data=("\n".join(bulk_lines) + "\n").encode("utf-8"),
        method="POST",
    )
    bulk_request.add_header("Content-Type", "application/x-ndjson")
    with urllib.request.urlopen(bulk_request, timeout=60) as response:
        bulk = json.loads(response.read().decode("utf-8"))
    if bulk.get("errors"):
        raise RuntimeError("Office bulk indexing returned errors")
    _request(es_url, "POST", f"/{index}/_refresh")

    readback_ids: list[str] = []
    coordinate_rows = 0
    for chunk in chunks:
        payload = _request(es_url, "GET", f"/{index}/_doc/{urllib.parse.quote(chunk.chunk_id, safe='')}")
        source = payload.get("_source", {})
        if payload.get("found") is not True or source.get("chunk_id") != chunk.chunk_id:
            raise RuntimeError(f"chunk readback mismatch: {chunk.chunk_id}")
        if source.get("document_id") != chunk.document_id or source.get("element_ids") != chunk.element_ids:
            raise RuntimeError(f"provenance readback mismatch: {chunk.chunk_id}")
        if chunk.sheet_name and source.get("sheet_name") == chunk.sheet_name and source.get("cell_range") == chunk.cell_range:
            coordinate_rows += 1
        readback_ids.append(str(source["chunk_id"]))

    query_specs = (
        ("q-docx-deploy", "release package health traffic", "office_chunk_fixture.docx"),
        ("q-pptx-notes", "speaker note rollback", "office_chunk_fixture.pptx"),
        ("q-pptx-table", "table stage owner", "office_chunk_fixture.pptx"),
        ("q-xlsx-region", "Metrics A1 C40", "office_chunk_fixture.xlsx"),
        ("q-xlsx-formula", "formula B2 times 2", "office_chunk_fixture.xlsx"),
    )
    query_checks: list[dict[str, Any]] = []
    for query_id, query, expected_document in query_specs:
        result = _request(es_url, "POST", f"/{index}/_search", {"query": {"match": {"text_content": query}}, "size": 10, "_source": ["document_id", "chunk_id", "element_ids", "sheet_name", "cell_range"]})
        hits = result.get("hits", {}).get("hits", [])
        returned = [hit.get("_source", {}) for hit in hits]
        query_checks.append({"query_id": query_id, "expected_document": expected_document, "hit_count": len(hits), "expected_document_returned": any(item.get("document_id") == expected_document for item in returned)})

    document_count = _request(es_url, "GET", f"/{index}/_count").get("count", 0)
    mapping = _request(es_url, "GET", f"/{index}/_mapping")[index]["mappings"]
    mapping_sha = hashlib.sha256(json.dumps(mapping, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    expected_coordinate_rows = sum(1 for chunk in chunks if chunk.sheet_name)
    status = readback_gate_status(
        document_count=document_count,
        expected_document_count=len(chunks),
        readback_chunk_count=len(readback_ids),
        expected_coordinate_rows=expected_coordinate_rows,
        coordinate_rows_checked=coordinate_rows,
        query_checks=query_checks,
        bulk_errors=bool(bulk.get("errors")),
    )
    receipt = {
        "status": status,
        "scope": "real-office-fixtures-isolated-elasticsearch-text-index",
        "git_sha": _current_git_sha(),
        "index": index,
        "document_count": document_count,
        "expected_document_count": len(chunks),
        "bulk_errors": bool(bulk.get("errors")),
        "readback_chunk_count": len(readback_ids),
        "coordinate_rows_checked": coordinate_rows,
        "expected_coordinate_rows": expected_coordinate_rows,
        "xlsx_coordinate_preservation": coordinate_rows == expected_coordinate_rows,
        "query_checks": query_checks,
        "all_expected_documents_returned": all(item["expected_document_returned"] for item in query_checks),
        "mapping_sha256": mapping_sha,
        "fixtures": fixtures,
        "physical_index_isolation": True,
        "alias_created": False,
        "alias_switched": False,
        "not_claimed": ["production_recall", "human_reviewed_qrels", "visual_quality", "formula_recalculation"],
    }
    output_dir.mkdir(parents=True)
    receipt_path = output_dir / "receipt.json"
    receipt_path.write_text(json.dumps(receipt, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    checksum_paths = (receipt_path,)
    (output_dir / "checksums.sha256").write_text(
        "".join(
            f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.name}\n"
            for path in checksum_paths
        ),
        encoding="utf-8",
    )
    return receipt


def main() -> int:
    parser = argparse.ArgumentParser(description="Verify Office chunks through isolated Elasticsearch readback")
    parser.add_argument("--es-url", default="http://127.0.0.1:9200")
    parser.add_argument("--index", default=DEFAULT_INDEX)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    receipt = run(args.es_url, args.out, args.index)
    print(json.dumps(receipt, ensure_ascii=False, indent=2))
    return 0 if receipt["status"] == "VERIFIED_READBACK" else 2


if __name__ == "__main__":
    raise SystemExit(main())
