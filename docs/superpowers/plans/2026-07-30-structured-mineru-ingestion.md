# Structured MinerU Ingestion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 MinerU OCR 的稳定 JSON 产物转换为带页面、元素、bbox、资产和父子关系的 Element Schema 与结构化 chunk。

**Architecture:** Python orchestrator 负责解析 MinerU content_list.json/middle.json 和 token-aware chunking；Go ingestion client、pipeline 和 MySQL/ES DTO 只传输与持久化结构化结果。旧字符串接口保留兼容读取，但 corpus_generation 非空时禁止降级到旧 splitter。

**Tech Stack:** Python Pydantic, MinerU CLI, langchain text utilities only after typed elements, Go HTTP client, MinIO/MySQL/ES。

---

### Task 1: Define Element Schema and fixtures

**Files:**
- Create: orchestrator/rag/elements.py
- Create: tests/fixtures/mineru/content_list.json
- Create: tests/fixtures/mineru/middle.json
- Create: tests/test_mineru_elements.py

- [ ] Step 1: Write failing mapping tests

~~~python
def test_content_list_maps_table_image_formula_and_bbox():
    elements = map_mineru_output(
        Path("tests/fixtures/mineru/content_list.json"),
        Path("tests/fixtures/mineru/middle.json"),
        document_id="doc-1",
    )
    assert [item.type for item in elements] == ["heading", "text", "table", "image", "equation"]
    assert elements[2].html.startswith("<table")
    assert elements[3].bbox == [10.0, 20.0, 110.0, 220.0]
    assert elements[4].latex == "E=mc^2"
    assert elements[2].source_payload_ref.endswith("content_list.json")
~~~

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_mineru_elements.py -q

Expected: FAIL because the mapper and fixture contract do not exist.

- [ ] Step 3: Add typed Pydantic models

Element fields are document_id, element_id, parent_id, reading_order, type, sub_type, heading_path, page_index, bbox, coordinate_system, text, html, latex, code_language, image_path, caption, footnote, caption_of, footnote_of, continuation_of, parser_name, parser_version, backend, source_payload_ref, and source_sha256. Bounding boxes must be finite four-number lists; type is limited to heading, text, table, image, equation, code, list, footnote, and page_break.

- [ ] Step 4: Run the test

Run: python -m pytest tests/test_mineru_elements.py -q

Expected: PASS with no external command.

- [ ] Step 5: Commit

~~~powershell
git add orchestrator/rag/elements.py tests/fixtures/mineru tests/test_mineru_elements.py
git commit -m "feat(rag): add MinerU element schema mapper"
~~~

### Task 2: Make PDF parse return a structured artifact

**Files:**
- Modify: orchestrator/rag/models.py
- Modify: orchestrator/rag/ingestion.py
- Modify: pkg/orchestrator/ingestion_client.go
- Modify: internal/pipeline/processor.go
- Create: tests/test_structured_ingestion_contract.py
- Modify: tests/test_rag_ingestion_pdf.py

- [ ] Step 1: Add a failing response contract test

~~~python
def test_pdf_parse_response_contains_elements_and_mineru_provenance():
    response = ParseResponsePayload.model_validate({
        "parsedText": "visible text",
        "documentId": "doc-1",
        "parserName": "mineru",
        "parserVersion": "3.4.4",
        "elements": [{"document_id": "doc-1", "element_id": "e1", "type": "text", "text": "visible text"}],
    })
    assert response.elements[0].parser_name == "mineru"
~~~

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_structured_ingestion_contract.py -q

Expected: FAIL because ParseResponsePayload only exposes parsedText.

- [ ] Step 3: Implement the explicit PDF path

The parse response adds documentId, parserName, parserVersion, sourceSha256, elements, assets, and renderedPages. For a PDF, ingestion.py must run MinerU with -m ocr and the configured backend, read content_list.json and middle.json, and raise an error if either the source bytes or typed elements are missing. It must never call the Tika URL for a PDF. DOCX/PPTX/XLSX keep the existing Tika path.

Change the Go client to return a ParsedArtifact value rather than a string. The external parse processor stores JSON under parsed/<file-md5>.json and passes the artifact to chunk. A corpus generation with no elements, missing parser metadata, or a legacy 1,000-rune fallback returns an error before persistence.

- [ ] Step 4: Run PDF routing tests

Run: python -m pytest tests/test_rag_ingestion_pdf.py tests/test_structured_ingestion_contract.py -q

Expected: PASS; assertions prove PDF invokes MinerU with -m ocr and non-PDF invokes Tika once.

- [ ] Step 5: Commit

~~~powershell
git add orchestrator/rag/models.py orchestrator/rag/ingestion.py pkg/orchestrator/ingestion_client.go internal/pipeline/processor.go tests/test_structured_ingestion_contract.py tests/test_rag_ingestion_pdf.py
git commit -m "feat(rag): return structured MinerU PDF artifacts"
~~~

### Task 3: Implement hierarchy-aware parent and child chunks

**Files:**
- Create: orchestrator/rag/chunking.py
- Modify: orchestrator/rag/ingestion.py
- Modify: orchestrator/rag/models.py
- Create: tests/test_hierarchy_chunking.py
- Modify: internal/pipeline/processor.go

- [ ] Step 1: Write failing boundary tests

~~~python
def test_chunking_preserves_table_header_and_hard_boundaries():
    chunks = chunk_elements(sample_elements(), child_tokens=256, parent_tokens=1000)
    table_chunks = [item for item in chunks if item.element_types == ["table"]]
    assert len(table_chunks) == 2
    assert table_chunks[0].text.startswith("Header A | Header B")
    assert table_chunks[0].parent_chunk_id == table_chunks[1].parent_chunk_id
    assert all(item.overlap_tokens == 0 for item in chunks if item.element_types != ["table"])
~~~

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_hierarchy_chunking.py -q

Expected: FAIL because no typed chunker exists.

- [ ] Step 3: Add the deterministic chunker

Use one heading_path as the normal merge boundary. Create a parent section at 1,000-2,000 tokens and child chunks at 256-512 tokens. Tables split only on complete row groups and repeat the original header; code/log chunks split on complete lines; equations remain atomic; images create a parent with caption/OCR/analysis text and asset_refs. Apply overlap only while splitting one oversized element. Serialize embedding_text with document title, ancestor headings and required caption, while text remains the exact source text.

- [ ] Step 4: Verify and commit

Run: python -m pytest tests/test_hierarchy_chunking.py tests/test_mineru_elements.py -q and go test ./internal/pipeline ./pkg/orchestrator -count=1.

Expected: PASS; the Go JSON decoder rejects a structured chunk with missing document_id, page_id, or element_ids.

~~~powershell
git add orchestrator/rag/chunking.py orchestrator/rag/ingestion.py orchestrator/rag/models.py internal/pipeline/processor.go tests/test_hierarchy_chunking.py
git commit -m "feat(rag): add hierarchy-aware typed chunking"
~~~

### Task 4: Enforce the PDF and legacy fallback guard

**Files:**
- Modify: orchestrator/rag/ingestion.py
- Modify: pkg/mineru/client.go
- Create: tests/test_no_pdf_tika.py
- Create: pkg/mineru/client_ocr_test.go

- [ ] Step 1: Write guard tests

Test a PDF with a fake Tika server and assert zero Tika requests. Test an enabled corpus task with missing elements and assert a non-zero error. Test the MinerU command arguments contain -m ocr and never contain -m auto.

- [ ] Step 2: Run the guard tests

Run: python -m pytest tests/test_no_pdf_tika.py -q and go test ./pkg/mineru -run OCR -count=1.

Expected: FAIL until the explicit guards are in place.

- [ ] Step 3: Implement and verify

The Go MinerU client keeps its current OCR route for agent Read; the structured orchestrator route is the only v2 corpus route. Tika remains registered only in documentparser for non-PDF extensions. A v2 task fails closed when orchestrator is disabled, structured metadata is absent, or chunking returns a plain string list.

- [ ] Step 4: Commit

~~~powershell
git add orchestrator/rag/ingestion.py pkg/mineru/client.go tests/test_no_pdf_tika.py pkg/mineru/client_ocr_test.go
git commit -m "test(rag): enforce MinerU-only PDF corpus ingestion"
~~~

## Rollback Boundary

Revert the structured response/client commits together if the Go and Python contracts diverge. Keep the existing PDF OCR fixtures and old parsed text objects; do not delete them or route PDF bytes through Tika.
