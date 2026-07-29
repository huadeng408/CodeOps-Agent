from __future__ import annotations

from pathlib import Path
from types import SimpleNamespace

import pytest

from orchestrator.rag.ingestion import IngestionService
from orchestrator.rag.models import ParseRequestPayload


class _Response:
    def __init__(self, content: bytes = b"", text: str = "") -> None:
        self.content = content
        self.text = text

    def raise_for_status(self) -> None:
        return None


class _SourceOnlyHTTP:
    def __init__(self, source: bytes) -> None:
        self._source = source
        self.put_calls = 0

    async def get(self, _url: str) -> _Response:
        return _Response(content=self._source)

    async def put(self, *_args, **_kwargs) -> _Response:
        self.put_calls += 1
        raise AssertionError("PDF content must never be sent to Tika")


class _TikaHTTP(_SourceOnlyHTTP):
    async def put(self, *_args, **_kwargs) -> _Response:
        self.put_calls += 1
        return _Response(text="Text extracted by Tika")


@pytest.mark.asyncio
async def test_pdf_parse_uses_mineru_ocr_and_never_tika(monkeypatch: pytest.MonkeyPatch) -> None:
    http = _SourceOnlyHTTP(b"%PDF-1.7\nscanned-image-only")
    service = IngestionService.__new__(IngestionService)
    service._http = http
    service._settings = SimpleNamespace(
        tika_url="http://tika.invalid",
        mineru_command="mineru-test",
        mineru_backend="pipeline",
        mineru_timeout_seconds=30,
    )
    calls: list[tuple[str, ...]] = []

    async def fake_run(command: str, *args: str, **_kwargs) -> tuple[bytes, bytes]:
        calls.append((command, *args))
        output_dir = Path(args[args.index("-o") + 1])
        markdown_dir = output_dir / "document" / "ocr"
        markdown_dir.mkdir(parents=True)
        (markdown_dir / "document.md").write_text(
            "MINERU_REAL_OCR_20260729\n所有 PDF 入口统一使用 MinerU OCR",
            encoding="utf-8",
        )
        return b"ok", b""

    monkeypatch.setattr("orchestrator.rag.ingestion._run_mineru", fake_run, raising=False)

    payload = ParseRequestPayload.model_validate(
        {
            "task": {
                "file_md5": "abc123",
                "file_name": "scan.pdf",
                "user_id": 1,
                "org_tag": "",
                "is_public": False,
                "stage": "parse",
            },
            "objectUrl": "http://objects.invalid/scan.pdf",
        }
    )
    result = await service.parse(payload)

    assert "MINERU_REAL_OCR_20260729" in result.parsedText
    assert "所有 PDF 入口统一使用 MinerU OCR" in result.parsedText
    assert http.put_calls == 0
    assert len(calls) == 1
    assert calls[0][0] == "mineru-test"
    assert calls[0][calls[0].index("-m") + 1] == "ocr"
    assert calls[0][calls[0].index("-b") + 1] == "pipeline"


@pytest.mark.asyncio
async def test_non_pdf_parse_keeps_tika_and_never_runs_mineru(monkeypatch: pytest.MonkeyPatch) -> None:
    http = _TikaHTTP(b"docx-content")
    service = IngestionService.__new__(IngestionService)
    service._http = http
    service._settings = SimpleNamespace(tika_url="http://tika.invalid")

    async def fail_mineru(*_args, **_kwargs) -> tuple[bytes, bytes]:
        raise AssertionError("non-PDF content must not invoke MinerU")

    monkeypatch.setattr("orchestrator.rag.ingestion._run_mineru", fail_mineru)
    payload = ParseRequestPayload.model_validate(
        {
            "task": {
                "file_md5": "docx123",
                "file_name": "notes.docx",
                "user_id": 1,
                "stage": "parse",
            },
            "objectUrl": "http://objects.invalid/notes.docx",
        }
    )

    result = await service.parse(payload)

    assert result.parsedText == "Text extracted by Tika"
    assert http.put_calls == 1
