"""Create a metadata-only receipt for the checked-in Office chunk fixtures."""

from __future__ import annotations

import hashlib
import json
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).parents[2]
sys.path.insert(0, str(ROOT))

from orchestrator.rag.chunking import chunk_elements_for_modality
from orchestrator.rag.evidence import EvidenceUnit
from orchestrator.rag.native_documents import parse_docx_bytes, parse_pptx_bytes
from orchestrator.rag.spreadsheet import parse_xlsx_bytes


FIXTURES = ROOT / "tests" / "fixtures" / "multimodal"
RECEIPT = ROOT / "eval_results" / "multimodal-rag" / "current-head-20260827-office-chunking" / "receipt.json"


def _sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _git_head() -> str:
    return subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()


def main() -> None:
    docx_path = FIXTURES / "office_chunk_fixture.docx"
    pptx_path = FIXTURES / "office_chunk_fixture.pptx"
    xlsx_path = FIXTURES / "office_chunk_fixture.xlsx"
    docx = parse_docx_bytes(docx_path.read_bytes(), document_id="office_chunk_fixture.docx")
    pptx = parse_pptx_bytes(pptx_path.read_bytes(), document_id="office_chunk_fixture.pptx")
    xlsx = parse_xlsx_bytes(xlsx_path.read_bytes(), document_id="office_chunk_fixture.xlsx")
    docx_chunks = chunk_elements_for_modality(docx.elements, modality="office_document")
    pptx_chunks = chunk_elements_for_modality(pptx.elements, modality="slide")
    xlsx_chunks = chunk_elements_for_modality(xlsx.elements, modality="spreadsheet")
    evidence = [
        EvidenceUnit.from_structured_chunk(chunk)
        for chunk in [*docx_chunks, *pptx_chunks, *xlsx_chunks]
    ]
    payload = {
        "status": "VERIFIED",
        "scope": "native-office-element-and-chunk-contract",
        "git_head": _git_head(),
        "python": "C:/Python312/python.exe",
        "fixtures": {
            "docx": {"sha256": _sha(docx_path), "bytes": docx_path.stat().st_size, "parser": docx.parser_name, "version": docx.parser_version, "elements": len(docx.elements), "chunks": len(docx_chunks)},
            "pptx": {"sha256": _sha(pptx_path), "bytes": pptx_path.stat().st_size, "parser": pptx.parser_name, "version": pptx.parser_version, "elements": len(pptx.elements), "chunks": len(pptx_chunks)},
            "xlsx": {"sha256": _sha(xlsx_path), "bytes": xlsx_path.stat().st_size, "parser": xlsx.parser_name, "version": xlsx.parser_version, "elements": len(xlsx.elements), "chunks": len(xlsx_chunks), "named_ranges": len(xlsx.named_ranges), "regions": len(xlsx.table_regions), "formula_cells": len(xlsx.formula_dependencies), "formula_cache_present": sum(xlsx.formula_cache_present.values())},
        },
        "evidence_units": len(evidence),
        "pdf_tika_policy": "preserved-and-covered-by-focused-tests",
        "test_command": "C:/Python312/python.exe -m pytest tests/test_native_office_documents.py tests/test_spreadsheet_rag.py tests/test_hierarchy_chunking.py tests/test_evidence_unit.py tests/test_rag_ingestion_pdf.py tests/test_no_pdf_tika.py -q",
        "not_claimed": ["visual-retrieval-quality", "qrels", "formula-recalculation", "audio-WER", "production-release"],
    }
    RECEIPT.parent.mkdir(parents=True, exist_ok=True)
    RECEIPT.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"status": payload["status"], "receipt": str(RECEIPT), "evidence_units": len(evidence)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
