import pytest

from orchestrator.rag.chunking import chunk_elements, chunk_elements_for_modality
from orchestrator.rag.elements import Element
from orchestrator.rag.evidence import EvidenceUnit


def sample_elements() -> list[Element]:
    return [
        Element(
            document_id="doc-1", element_id="heading-1", type="heading", text="Chapter 1",
            heading_path=["Chapter 1"], page_index=0, bbox=[0, 0, 10, 10],
        ),
        Element(
            document_id="doc-1", element_id="text-1", type="text", text="alpha beta gamma",
            heading_path=["Chapter 1"], page_index=0, bbox=[0, 10, 10, 20],
        ),
        Element(
            document_id="doc-1", element_id="table-1", type="table",
            text="Header A | Header B\nrow 1 | value 1\nrow 2 | value 2",
            heading_path=["Chapter 1"], page_index=0, bbox=[0, 20, 10, 40],
        ),
    ]


def test_chunking_preserves_table_header_and_hard_boundaries() -> None:
    chunks = chunk_elements(sample_elements(), child_tokens=6, parent_tokens=20)
    table_chunks = [item for item in chunks if item.element_types == ["table"]]
    assert len(table_chunks) == 2
    assert table_chunks[0].text.startswith("Header A | Header B")
    assert table_chunks[0].parent_chunk_id == table_chunks[1].parent_chunk_id
    assert all(item.overlap_tokens == 0 for item in chunks if item.element_types != ["table"])


def test_chunk_ids_are_unique_across_heading_sections() -> None:
    elements = sample_elements() + [
        Element(
            document_id="doc-1", element_id="heading-2", type="heading", text="Chapter 2",
            heading_path=["Chapter 2"], page_index=1, bbox=[0, 0, 10, 10],
        ),
        Element(
            document_id="doc-1", element_id="text-2", type="text", text="delta epsilon",
            heading_path=["Chapter 2"], page_index=1, bbox=[0, 10, 10, 20],
        ),
    ]
    chunks = chunk_elements(elements, child_tokens=6, parent_tokens=20)
    ids = [item.chunk_id for item in chunks]
    assert len(ids) == len(set(ids))


def test_small_text_elements_merge_with_provenance() -> None:
    elements = [
        Element(
            document_id="doc-1", element_id="text-1", type="text", text="alpha beta",
            heading_path=["Guide"], page_index=0, bbox=[0, 0, 10, 10],
        ),
        Element(
            document_id="doc-1", element_id="text-2", type="text", text="gamma delta",
            heading_path=["Guide"], page_index=0, bbox=[0, 10, 10, 20],
        ),
    ]
    chunks = chunk_elements(elements, child_tokens=8, parent_tokens=20)
    assert len(chunks) == 1
    assert chunks[0].text == "alpha beta\n\ngamma delta"
    assert chunks[0].element_ids == ["text-1", "text-2"]


def test_overlap_applies_only_to_one_oversized_text_element() -> None:
    elements = [
        Element(
            document_id="doc-1", element_id="long", type="text",
            text="one two three four five six seven eight",
            heading_path=["Guide"], page_index=0, bbox=[0, 0, 10, 10],
        ),
        Element(
            document_id="doc-1", element_id="equation", type="equation", latex="E=mc^2",
            heading_path=["Guide"], page_index=0, bbox=[0, 10, 10, 20],
        ),
    ]
    chunks = chunk_elements(elements, child_tokens=4, parent_tokens=20, overlap_tokens=1)
    text_chunks = [item for item in chunks if item.element_types == ["text"]]
    equation = next(item for item in chunks if item.element_types == ["equation"])
    assert [item.text for item in text_chunks] == ["one two three four", "four five six seven", "seven eight"]
    assert [item.overlap_tokens for item in text_chunks] == [0, 1, 1]
    assert equation.text == "E=mc^2"
    assert equation.overlap_tokens == 0


def test_code_splits_on_complete_lines_and_image_caption_enters_embedding_text() -> None:
    elements = [
        Element(
            document_id="doc-1", element_id="code", type="code",
            text="alpha = 1\nbeta = 2\ngamma = 3",
            heading_path=["Guide"], page_index=0, bbox=[0, 0, 10, 20],
        ),
        Element(
            document_id="doc-1", element_id="image", type="image", text="OCR labels",
            caption="Architecture diagram", image_path="images/diagram.png",
            heading_path=["Guide"], page_index=1, bbox=[0, 0, 20, 20],
        ),
    ]
    chunks = chunk_elements(elements, child_tokens=4, parent_tokens=20)
    code_chunks = [item for item in chunks if item.element_types == ["code"]]
    image = next(item for item in chunks if item.element_types == ["image"])
    assert [item.text for item in code_chunks] == ["alpha = 1", "beta = 2", "gamma = 3"]
    assert image.text == "OCR labels"
    assert "Guide" in image.embedding_text
    assert "Architecture diagram" in image.embedding_text
    assert "OCR labels" in image.embedding_text
    assert image.asset_refs == ["images/diagram.png"]


def test_python_function_is_a_hard_code_chunk_boundary() -> None:
    source = "\n".join(
        [
            "def first():",
            "    alpha = 1",
            "    return alpha",
            "",
            "def second():",
            "    beta = 2",
            "    return beta",
        ]
    )
    elements = [
        Element(
            document_id="doc-1", element_id="code", type="code", text=source,
            heading_path=["Guide"], page_index=0, bbox=[0, 0, 10, 20],
        )
    ]

    chunks = chunk_elements(elements, child_tokens=4, parent_tokens=20)

    assert [item.text for item in chunks] == [
        "def first():\n    alpha = 1\n    return alpha",
        "def second():\n    beta = 2\n    return beta",
    ]


def test_office_document_policy_keeps_table_and_image_typed_boundaries() -> None:
    source_sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    elements = [
        Element(document_id="doc-office", element_id="heading", type="heading", text="Runbook", heading_path=["Runbook"], source_sha256=source_sha, parser_version="fixture"),
        Element(document_id="doc-office", element_id="paragraph", type="text", text="Deploy the service", heading_path=["Runbook"], source_sha256=source_sha, parser_version="fixture"),
        Element(document_id="doc-office", element_id="table", type="table", text="Name | Owner\nAPI | SRE", heading_path=["Runbook"], source_sha256=source_sha, parser_version="fixture"),
        Element(document_id="doc-office", element_id="image", type="image", text="", caption="Topology", image_path="embedded://sha256:abc", heading_path=["Runbook"], source_sha256=source_sha, parser_version="fixture"),
    ]

    chunks = chunk_elements_for_modality(elements, modality="office_document", child_tokens=20, parent_tokens=40)

    assert [item.element_types for item in chunks] == [["text"], ["table"], ["image"]]
    assert chunks[1].text.startswith("Name | Owner")
    assert chunks[2].asset_refs == ["embedded://sha256:abc"]
    assert all(EvidenceUnit.from_structured_chunk(item).coordinates.element_ids for item in chunks)


def test_slide_policy_keeps_slide_shapes_in_reading_order() -> None:
    elements = [
        Element(document_id="deck", element_id="slide", type="heading", text="Overview", page_index=0, heading_path=["Overview"]),
        Element(document_id="deck", element_id="shape-a", type="text", text="First", page_index=0, heading_path=["Overview"], bbox=[1, 1, 10, 10]),
        Element(document_id="deck", element_id="shape-b", type="table", text="A | B\n1 | 2", page_index=0, heading_path=["Overview"], bbox=[10, 10, 30, 30]),
        Element(document_id="deck", element_id="notes", type="text", text="Speaker note", sub_type="speaker_notes", page_index=0, heading_path=["Overview"]),
    ]

    chunks = chunk_elements_for_modality(elements, modality="slide", child_tokens=100, parent_tokens=200)

    assert [item.element_ids for item in chunks] == [["shape-a"], ["shape-b"], ["notes"]]
    assert [item.element_types for item in chunks] == [["text"], ["table"], ["text"]]


def test_modality_policy_rejects_unknown_values() -> None:
    with pytest.raises(ValueError, match="unsupported modality"):
        chunk_elements_for_modality([], modality="unknown")  # type: ignore[arg-type]
