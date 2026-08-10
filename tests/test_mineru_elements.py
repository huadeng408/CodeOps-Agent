from pathlib import Path

from orchestrator.rag.elements import Element, map_mineru_output


FIXTURES = Path(__file__).parent / "fixtures" / "mineru"


def test_content_list_maps_table_image_formula_and_bbox() -> None:
    elements = map_mineru_output(
        FIXTURES / "content_list.json",
        FIXTURES / "middle.json",
        document_id="doc-1",
    )
    assert [item.type for item in elements] == ["heading", "text", "table", "image", "equation"]
    assert elements[2].html.startswith("<table")
    assert elements[3].bbox == [10.0, 20.0, 110.0, 220.0]
    assert elements[4].latex == "E=mc^2"
    assert elements[2].source_payload_ref.endswith("content_list.json")
    assert elements[2].parser_version == "3.4.4"


def test_real_mineru_3_4_4_middle_json_exposes_version_and_backend() -> None:
    """MinerU 3.4.4 writes `_version_name`/`_backend`, not `version`/`backend`.

    The fixture is a trimmed copy of a real `*_middle.json` emitted by
    `mineru -m ocr -b pipeline` (3.4.4). Missing provenance must fail closed in
    ingestion, so the mapper has to read the keys MinerU actually produces.
    """
    elements = map_mineru_output(
        FIXTURES / "content_list.json",
        FIXTURES / "middle_real_3_4_4.json",
        document_id="doc-real",
    )
    assert elements, "real MinerU output must map to elements"
    assert {item.parser_version for item in elements} == {"3.4.4"}
    assert {item.backend for item in elements} == {"pipeline"}


def test_element_rejects_non_finite_bbox() -> None:
    try:
        Element(document_id="d", element_id="e", type="text", bbox=[0, 1, float("inf"), 3])
    except ValueError as exc:
        assert "bbox" in str(exc)
    else:
        raise AssertionError("non-finite bbox must be rejected")

