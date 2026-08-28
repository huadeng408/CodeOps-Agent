"""Run a deterministic, service-free retrieval check over real Office fixtures.

This is deliberately a contract smoke test, not a production quality claim:
the qrels are derived from the checked-in fixture and still require independent
human review before they can gate a release.
"""

from __future__ import annotations

import hashlib
import json
import re
import sys
from pathlib import Path
from typing import Any

ROOT = Path(__file__).parents[2]
sys.path.insert(0, str(ROOT))

from orchestrator.eval.metrics import score_run
from orchestrator.rag.chunking import StructuredChunk, chunk_elements_for_modality
from orchestrator.rag.native_documents import parse_docx_bytes, parse_pptx_bytes
from orchestrator.rag.spreadsheet import parse_xlsx_bytes


FIXTURES = ROOT / "tests" / "fixtures" / "multimodal"
DEFAULT_RECEIPT = ROOT / "eval_results" / "multimodal-rag" / "current-head-20260828-office-retrieval" / "receipt.json"


def _sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _terms(value: str) -> set[str]:
    return {item for item in re.findall(r"[a-z0-9]+", value.lower()) if len(item) > 1}


def _load_chunks() -> tuple[list[StructuredChunk], dict[str, dict[str, Any]]]:
    specs = (
        ("office_chunk_fixture.docx", parse_docx_bytes, "office_document"),
        ("office_chunk_fixture.pptx", parse_pptx_bytes, "slide"),
        ("office_chunk_fixture.xlsx", parse_xlsx_bytes, "spreadsheet"),
    )
    chunks: list[StructuredChunk] = []
    metadata: dict[str, dict[str, Any]] = {}
    for name, parser, modality in specs:
        source = FIXTURES / name
        artifact = parser(source.read_bytes(), document_id=name)
        parsed_chunks = chunk_elements_for_modality(artifact.elements, modality=modality)
        chunks.extend(parsed_chunks)
        metadata[name] = {
            "sha256": _sha(source),
            "bytes": source.stat().st_size,
            "parser": artifact.parser_name,
            "version": artifact.parser_version,
            "elements": len(artifact.elements),
            "chunks": len(parsed_chunks),
        }
        if name.endswith(".xlsx"):
            metadata[name].update(
                {
                    "named_ranges": len(artifact.named_ranges),
                    "regions": len(artifact.table_regions),
                    "formula_cells": len(artifact.formula_dependencies),
                }
            )
    return chunks, metadata


def _retrieve(query: str, chunks: list[StructuredChunk]) -> list[dict[str, Any]]:
    query_terms = _terms(query)
    ranked: list[tuple[int, str, StructuredChunk]] = []
    for chunk in chunks:
        text_terms = _terms(f"{chunk.text} {chunk.embedding_text}")
        ranked.append((len(query_terms & text_terms), chunk.chunk_id, chunk))
    ranked.sort(key=lambda item: (-item[0], item[1]))
    return [
        {
            "query_id": query,
            "document_id": chunk.document_id,
            "section_path": list(chunk.section_path),
            "rank": rank,
            "score": float(score),
        }
        for rank, (score, _chunk_id, chunk) in enumerate(ranked, start=1)
    ]


def run_office_fixture_retrieval(receipt_path: str | Path = DEFAULT_RECEIPT) -> dict[str, Any]:
    chunks, fixtures = _load_chunks()
    query_specs = (
        ("q-docx-deploy", "release package health traffic", "office_chunk_fixture.docx", ["Operations"]),
        ("q-pptx-notes", "speaker note rollback", "office_chunk_fixture.pptx", ["Release plan"]),
        ("q-pptx-table", "table stage owner", "office_chunk_fixture.pptx", ["Release plan"]),
        ("q-xlsx-region", "Metrics A1 C40", "office_chunk_fixture.xlsx", ["Metrics"]),
        ("q-xlsx-formula", "formula B2 times 2", "office_chunk_fixture.xlsx", ["Metrics"]),
    )
    qrels: list[dict[str, Any]] = []
    run: list[dict[str, Any]] = []
    query_hashes: list[dict[str, str]] = []
    for query_id, query, document_id, section_path in query_specs:
        qrels.append(
            {
                "query_id": query_id,
                "document_id": document_id,
                "section_path": section_path,
                "relevance": 2,
                "source_id": "checked-in-office-fixture",
                "language": "en",
                "query_type": "fixture-contract",
            }
        )
        hits = _retrieve(query, chunks)
        for hit in hits:
            hit["query_id"] = query_id
        run.extend(hits)
        query_hashes.append({"query_id": query_id, "query_sha256": hashlib.sha256(query.encode()).hexdigest()})

    xlsx_chunks = [chunk for chunk in chunks if chunk.document_id.endswith(".xlsx")]
    coordinate_checks = {
        "xlsx_ranges": len({chunk.cell_range for chunk in xlsx_chunks if chunk.cell_range.startswith("Metrics!") and ":" in chunk.cell_range}),
        "xlsx_chunks_checked": len(xlsx_chunks),
        "all_preserved": bool(xlsx_chunks) and all(chunk.sheet_name == "Metrics" and chunk.cell_range for chunk in xlsx_chunks),
    }
    metrics = score_run(qrels, run, ks=(5, 10))
    receipt = {
        "status": "FIXTURE_CONTRACT_ONLY",
        "scope": "real-office-fixture-lexical-retrieval-and-coordinate-preservation",
        "fixtures": fixtures,
        "query_count": len(query_specs),
        "qrels_count": len(qrels),
        "prediction_count": len(run),
        "query_hashes": query_hashes,
        "metrics": metrics,
        "coordinate_checks": coordinate_checks,
        "qrels_source": "fixture-derived; requires independent human review before quality use",
        "not_claimed": ["production-recall", "human-reviewed-qrels", "es-readback", "visual-bakeoff"],
    }
    output = Path(receipt_path)
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(receipt, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    output.with_name("qrels.jsonl").write_text("".join(json.dumps(row, ensure_ascii=False) + "\n" for row in qrels), encoding="utf-8")
    output.with_name("predictions.jsonl").write_text("".join(json.dumps(row, ensure_ascii=False) + "\n" for row in run), encoding="utf-8")
    return receipt


def main() -> int:
    receipt = run_office_fixture_retrieval()
    print(json.dumps({"status": receipt["status"], "receipt": str(DEFAULT_RECEIPT), "metrics": receipt["metrics"]}, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
