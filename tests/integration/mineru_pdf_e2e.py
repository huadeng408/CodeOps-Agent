from __future__ import annotations

import io
import os
from types import SimpleNamespace

import pytest
from PIL import Image, ImageDraw, ImageFont

from orchestrator.rag.ingestion import IngestionService
from orchestrator.rag.models import ParseRequestPayload


class _Response:
    def __init__(self, content: bytes) -> None:
        self.content = content

    def raise_for_status(self) -> None:
        return None


class _PDFSource:
    def __init__(self, content: bytes) -> None:
        self.content = content
        self.tika_calls = 0

    async def get(self, _url: str) -> _Response:
        return _Response(self.content)

    async def put(self, *_args, **_kwargs) -> _Response:
        self.tika_calls += 1
        raise AssertionError("PDF content must never be sent to Tika")


def _image_only_pdf() -> bytes:
    image = Image.new("RGB", (1600, 2200), "white")
    draw = ImageDraw.Draw(image)
    font_path = os.getenv("MINERU_E2E_FONT", r"C:\Windows\Fonts\arialbd.ttf")
    font = ImageFont.truetype(font_path, 82)
    draw.text((120, 300), "MINERU REAL OCR", fill="black", font=font)
    draw.text((120, 520), "MINERU_REAL_OCR_20260729", fill="black", font=font)
    draw.rectangle((90, 250, 1510, 720), outline="black", width=5)
    output = io.BytesIO()
    image.save(output, format="PDF", resolution=150.0)
    return output.getvalue()


@pytest.mark.integration
@pytest.mark.asyncio
async def test_real_mineru_ocr_bypasses_tika() -> None:
    if os.getenv("CODE_AGENT_RUN_MINERU_E2E") != "1":
        pytest.skip("set CODE_AGENT_RUN_MINERU_E2E=1 to run the real MinerU integration")

    command = os.getenv("CODE_AGENT_MINERU_COMMAND", "mineru")
    source = _PDFSource(_image_only_pdf())
    service = IngestionService.__new__(IngestionService)
    service._http = source
    service._settings = SimpleNamespace(
        tika_url="http://127.0.0.1:1",
        mineru_command=command,
        mineru_backend=os.getenv("CODE_AGENT_MINERU_BACKEND", "pipeline"),
        mineru_timeout_seconds=600,
    )
    payload = ParseRequestPayload.model_validate(
        {
            "task": {
                "file_md5": "real-mineru-e2e",
                "file_name": "image-only.pdf",
                "user_id": 1,
                "stage": "parse",
            },
            "objectUrl": "http://objects.invalid/image-only.pdf",
        }
    )

    result = await service.parse(payload)

    assert "MINERU_REAL_OCR_20260729" in result.parsedText.replace("\\_", "_")
    assert source.tika_calls == 0
