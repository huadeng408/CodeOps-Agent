from __future__ import annotations

from pathlib import Path
from types import SimpleNamespace

import pytest

from orchestrator.rag.ingestion import IngestionService
from orchestrator.rag.models import ParseRequestPayload


class _Response:
    content = b"%PDF-1.7\nscan"

    def raise_for_status(self) -> None:
        return None


class _NoTikaHTTP:
    def __init__(self) -> None:
        self.tika_calls = 0

    async def get(self, _url: str) -> _Response:
        return _Response()

    async def put(self, *_args, **_kwargs):
        self.tika_calls += 1
        raise AssertionError("PDF must not be routed to Tika")


@pytest.mark.asyncio
async def test_pdf_missing_structured_mineru_output_fails_without_tika(monkeypatch: pytest.MonkeyPatch) -> None:
    http = _NoTikaHTTP()
    service = IngestionService.__new__(IngestionService)
    service._http = http
    service._settings = SimpleNamespace(
        tika_url="http://tika.invalid", mineru_command="mineru-test", mineru_backend="pipeline", mineru_timeout_seconds=30
    )

    async def fake_run(_command: str, *args: str, **_kwargs):
        output_dir = Path(args[args.index("-o") + 1])
        output_dir.mkdir(parents=True)
        (output_dir / "legacy.md").write_text("legacy fallback text", encoding="utf-8")
        return b"ok", b""

    monkeypatch.setattr("orchestrator.rag.ingestion._run_mineru", fake_run)
    payload = ParseRequestPayload.model_validate(
        {
            "task": {"file_md5": "doc-1", "file_name": "scan.pdf", "user_id": 1, "stage": "parse"},
            "objectUrl": "http://objects.invalid/scan.pdf",
        }
    )

    with pytest.raises(RuntimeError, match="content_list.json/middle.json"):
        await service.parse(payload)
    assert http.tika_calls == 0

