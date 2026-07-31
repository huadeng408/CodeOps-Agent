from orchestrator.rag.chunking import chunk_elements
from orchestrator.rag.elements import Element


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
