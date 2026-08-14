from __future__ import annotations

import hashlib
from dataclasses import dataclass
from io import BytesIO

import openpyxl

from .elements import Element


@dataclass(frozen=True)
class SpreadsheetArtifact:
    parser_name: str
    parser_version: str
    source_sha256: str
    elements: list[Element]


def parse_xlsx_bytes(content: bytes, *, document_id: str, source_url: str = "") -> SpreadsheetArtifact:
    """Extract worksheet regions and formula coordinates without recalculating cells."""

    source_sha256 = hashlib.sha256(content).hexdigest()
    parser_version = openpyxl.__version__
    workbook = openpyxl.load_workbook(BytesIO(content), read_only=True, data_only=False)
    elements: list[Element] = []
    order = 0
    for sheet in workbook.worksheets:
        heading_path = [sheet.title]
        elements.append(
            _element(
                document_id, order, "heading", sheet.title, heading_path, source_sha256, parser_version, source_url
            )
        )
        order += 1
        rows = list(sheet.iter_rows())
        if rows:
            region = "\n".join(" | ".join(_cell_text(cell.value) for cell in row) for row in rows)
            dimension = sheet.calculate_dimension()
            elements.append(
                _element(
                    document_id,
                    order,
                    "table",
                    f"Sheet: {sheet.title}\nRange: {dimension}\n{region}",
                    heading_path,
                    source_sha256,
                    parser_version,
                    source_url,
                )
            )
            order += 1
        for row in rows:
            for cell in row:
                if not isinstance(cell.value, str) or not cell.value.startswith("="):
                    continue
                elements.append(
                    _element(
                        document_id,
                        order,
                        "equation",
                        f"{sheet.title}!{cell.coordinate} {cell.value}",
                        heading_path,
                        source_sha256,
                        parser_version,
                        source_url,
                        latex=cell.value,
                    )
                )
                order += 1
    return SpreadsheetArtifact("openpyxl", parser_version, source_sha256, elements)


def _element(
    document_id: str,
    order: int,
    element_type: str,
    text: str,
    heading_path: list[str],
    source_sha256: str,
    parser_version: str,
    source_url: str,
    *,
    latex: str = "",
) -> Element:
    return Element(
        document_id=document_id,
        element_id=f"{document_id}:sheet:{heading_path[0]}:e{order}",
        reading_order=order,
        type=element_type,  # type: ignore[arg-type]
        heading_path=heading_path,
        text=text,
        latex=latex,
        parser_name="openpyxl",
        parser_version=parser_version,
        source_sha256=source_sha256,
        source_url=source_url,
    )


def _cell_text(value: object) -> str:
    return "" if value is None else str(value)
