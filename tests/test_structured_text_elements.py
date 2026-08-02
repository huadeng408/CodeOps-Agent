"""Structured chunking for non-PDF documents (plan: native parsers for md/rst).

Non-PDF documents (Markdown/reST from official repos) must also produce
structured chunks with corpus provenance so they land in the v2 text index,
not the legacy one.
"""

from __future__ import annotations

from orchestrator.rag.elements import Element
from orchestrator.rag.ingestion import _text_to_elements


def test_text_to_elements_builds_headings_and_paragraphs() -> None:
    text = (
        "# Chapter One\n\n"
        "Intro paragraph text.\n\n"
        "## Section A\n\n"
        "Section body content."
    )
    elements = _text_to_elements(text, document_id="doc-1")
    assert len(elements) == 4
    assert elements[0].type == "heading"
    assert elements[0].text == "Chapter One"
    assert elements[0].heading_path == ["Chapter One"]
    assert elements[1].type == "text"
    assert elements[1].text == "Intro paragraph text."
    assert elements[2].type == "heading"
    assert elements[2].heading_path == ["Chapter One", "Section A"]
    assert elements[3].type == "text"
    assert elements[3].heading_path == ["Chapter One", "Section A"]


def test_text_to_elements_skips_blank_and_navigation() -> None:
    text = "\n\n# Title\n\n\n## Sub\n\nBody\n"
    elements = _text_to_elements(text, document_id="doc-1")
    # No empty-text elements.
    assert all(e.text.strip() for e in elements)
    assert elements[0].type == "heading"


def test_text_to_elements_sets_page_and_source() -> None:
    elements = _text_to_elements("# T\n\nBody", document_id="doc-1")
    for element in elements:
        assert element.page_index == 0
        assert element.parser_name == "native"
        assert element.source_sha256 == ""
        assert element.document_id == "doc-1"


def test_text_to_elements_handles_code_blocks_as_text() -> None:
    text = "# T\n\n```go\nfunc main() {}\n```\n"
    elements = _text_to_elements(text, document_id="doc-1")
    texts = [e.text for e in elements]
    assert any("func main()" in t for t in texts)


def test_text_to_elements_detects_heading_depth() -> None:
    text = "# H1\n\n## H2\n\n### H3\n\nBody\n"
    elements = _text_to_elements(text, document_id="doc-1")
    headings = [e for e in elements if e.type == "heading"]
    assert [h.text for h in headings] == ["H1", "H2", "H3"]
    assert headings[2].heading_path == ["H1", "H2", "H3"]


def test_text_to_elements_is_element_instance() -> None:
    elements = _text_to_elements("# T\n\nBody", document_id="doc-1")
    assert all(isinstance(e, Element) for e in elements)
