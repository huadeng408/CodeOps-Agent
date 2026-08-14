from __future__ import annotations

from io import BytesIO
from types import SimpleNamespace

from openpyxl import Workbook
import pytest

from orchestrator.rag.ingestion import IngestionService
from orchestrator.rag.models import ParseRequestPayload
from orchestrator.rag.spreadsheet import parse_xlsx_bytes


def test_xlsx_parser_preserves_sheet_table_and_formula_coordinates() -> None:
    workbook = Workbook()
    sheet = workbook.active
    sheet.title = "Budget"
    sheet.append(["Item", "Amount", "Double"])
    sheet.append(["Servers", 10, "=B2*2"])
    sheet.append(["Storage", 20, "=SUM(B2:B3)"])
    sheet.merge_cells("A5:C5")
    sheet["A5"] = "Quarterly plan"
    payload = BytesIO()
    workbook.save(payload)

    artifact = parse_xlsx_bytes(payload.getvalue(), document_id="budget.xlsx")

    assert artifact.parser_name == "openpyxl"
    assert artifact.source_sha256
    assert [element.type for element in artifact.elements] == ["heading", "table", "equation", "equation"]
    table = artifact.elements[1]
    assert "Sheet: Budget" in table.text
    assert "Range: A1:C5" in table.text
    assert "Item | Amount | Double" in table.text
    assert table.parser_version == artifact.parser_version
    assert table.source_sha256 == artifact.source_sha256
    assert artifact.elements[2].latex == "=B2*2"
    assert artifact.elements[2].text == "Budget!C2 =B2*2"
    assert artifact.elements[3].latex == "=SUM(B2:B3)"


class _Response:
    def __init__(self, content: bytes) -> None:
        self.content = content
        self.text = ""

    def raise_for_status(self) -> None:
        return None


class _XlsxHTTP:
    def __init__(self, content: bytes) -> None:
        self.content = content
        self.tika_calls = 0

    async def get(self, _url: str) -> _Response:
        return _Response(self.content)

    async def put(self, *_args: object, **_kwargs: object) -> _Response:
        self.tika_calls += 1
        raise AssertionError("XLSX must use native openpyxl extraction")


@pytest.mark.asyncio
async def test_xlsx_parse_uses_native_extractor_and_never_tika() -> None:
    workbook = Workbook()
    workbook.active.title = "Budget"
    workbook.active.append(["Amount"])
    workbook.active.append(["=1+1"])
    payload = BytesIO()
    workbook.save(payload)
    http = _XlsxHTTP(payload.getvalue())
    service = IngestionService.__new__(IngestionService)
    service._http = http
    service._settings = SimpleNamespace(tika_url="http://tika.invalid")

    response = await service.parse(
        ParseRequestPayload.model_validate(
            {
                "task": {"file_md5": "budget", "file_name": "budget.xlsx", "user_id": 1, "stage": "parse"},
                "objectUrl": "http://source.invalid/budget.xlsx",
            }
        )
    )

    assert http.tika_calls == 0
    assert response.parserName == "openpyxl"
    assert [element.type for element in response.elements] == ["heading", "table", "equation"]
