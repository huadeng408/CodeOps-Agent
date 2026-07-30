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


def test_element_rejects_non_finite_bbox() -> None:
    try:
        Element(document_id="d", element_id="e", type="text", bbox=[0, 1, float("inf"), 3])
    except ValueError as exc:
        assert "bbox" in str(exc)
    else:
        raise AssertionError("non-finite bbox must be rejected")

