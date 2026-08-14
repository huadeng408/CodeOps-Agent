"""Page evidence candidates must be traceable to explicit MinerU OCR output."""

from __future__ import annotations

import hashlib
import json
import base64
from pathlib import Path

import pytest

from orchestrator.eval.mineru_page_candidates import (
    MinerUPageCandidateError,
    materialize_page_candidates,
)


def _inputs(tmp_path: Path) -> tuple[Path, Path, Path]:
    content = tmp_path / "content_list.json"
    content.write_text(
        json.dumps(
            [
                {"type": "text", "page_idx": 0, "bbox": [0, 0, 1, 2], "text": "A visible fact."},
                {"type": "table", "page_idx": 0, "bbox": [1, 0, 2, 1], "table_body": "<table/>"},
            ]
        ),
        encoding="utf-8",
    )
    middle = tmp_path / "middle.json"
    middle.write_text(json.dumps({"_version_name": "3.4.4", "_backend": "pipeline", "ocr_mode": "explicit"}), encoding="utf-8")
    page = tmp_path / "page-0.png"
    page.write_bytes(base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAADElEQVR42mP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"))
    return content, middle, page


def _source() -> dict[str, str]:
    return {"source_id": "licensed-docs", "source_revision": "abc1234", "license_spdx": "CC-BY-4.0"}


def test_materialize_page_candidates_binds_page_image_and_normalizes_mineru_geometry(tmp_path: Path) -> None:
    content, middle, page = _inputs(tmp_path)
    out = materialize_page_candidates(
        content,
        middle,
        page_images={0: page},
        page_dimensions={0: (2, 2)},
        document_id="licensed-doc@abc1234:guide.pdf",
        source=_source(),
        ocr_mode="explicit",
        out_path=tmp_path / "candidates.jsonl",
    )

    rows = [json.loads(line) for line in out.read_text(encoding="utf-8").splitlines()]
    assert len(rows) == 2
    assert rows[0]["candidate_status"] == "AI_CANDIDATE"
    assert rows[0]["page_id"] == "licensed-doc@abc1234:guide.pdf:p0"
    assert rows[0]["bbox"] == [0.0, 0.0, 500.0, 1000.0]
    assert rows[0]["page_image_sha256"] == hashlib.sha256(page.read_bytes()).hexdigest()
    assert rows[0]["mineru"] == {
        "version": "3.4.4",
        "ocr_mode": "explicit",
        "content_sha256": hashlib.sha256(content.read_bytes()).hexdigest(),
        "middle_sha256": hashlib.sha256(middle.read_bytes()).hexdigest(),
    }


def test_materialize_page_candidates_requires_explicit_ocr(tmp_path: Path) -> None:
    content, middle, page = _inputs(tmp_path)

    with pytest.raises(MinerUPageCandidateError, match="MINERU_OCR_MODE_REQUIRED"):
        materialize_page_candidates(
            content,
            middle,
            page_images={0: page},
            page_dimensions={0: (2, 2)},
            document_id="licensed-doc@abc1234:guide.pdf",
            source=_source(),
            ocr_mode="auto",
            out_path=tmp_path / "candidates.jsonl",
        )


def test_materialize_page_candidates_requires_explicit_ocr_in_mineru_metadata(tmp_path: Path) -> None:
    content, middle, page = _inputs(tmp_path)
    middle.write_text(json.dumps({"_version_name": "3.4.4", "ocr_mode": "auto"}), encoding="utf-8")

    with pytest.raises(MinerUPageCandidateError, match="MINERU_OCR_RECEIPT_INVALID"):
        materialize_page_candidates(
            content, middle, page_images={0: page}, page_dimensions={0: (2, 2)},
            document_id="licensed-doc@abc1234:guide.pdf", source=_source(), ocr_mode="explicit",
            out_path=tmp_path / "candidates.jsonl",
        )


def test_materialize_page_candidates_rejects_missing_page_image_or_outside_geometry(tmp_path: Path) -> None:
    content, middle, page = _inputs(tmp_path)

    with pytest.raises(MinerUPageCandidateError, match="PAGE_IMAGE_MISSING"):
        materialize_page_candidates(
            content,
            middle,
            page_images={},
            page_dimensions={},
            document_id="licensed-doc@abc1234:guide.pdf",
            source=_source(),
            ocr_mode="explicit",
            out_path=tmp_path / "candidates.jsonl",
        )

    content.write_text(
        json.dumps([{"type": "text", "page_idx": 0, "bbox": [0, 0, 3, 1], "text": "outside"}]),
        encoding="utf-8",
    )
    with pytest.raises(MinerUPageCandidateError, match="MINERU_BBOX_OUTSIDE_PAGE"):
        materialize_page_candidates(
            content,
            middle,
            page_images={0: page},
            page_dimensions={0: (2, 2)},
            document_id="licensed-doc@abc1234:guide.pdf",
            source=_source(),
            ocr_mode="explicit",
            out_path=tmp_path / "candidates.jsonl",
        )


def test_materialize_page_candidates_rejects_non_image_page_or_dimension_claim(tmp_path: Path) -> None:
    content, middle, page = _inputs(tmp_path)
    page.write_bytes(b"not-an-image")
    with pytest.raises(MinerUPageCandidateError, match="PAGE_IMAGE_INVALID"):
        materialize_page_candidates(
            content, middle, page_images={0: page}, page_dimensions={0: (2, 2)},
            document_id="licensed-doc@abc1234:guide.pdf", source=_source(), ocr_mode="explicit",
            out_path=tmp_path / "candidates.jsonl",
        )

    _, _, page = _inputs(tmp_path)
    with pytest.raises(MinerUPageCandidateError, match="PAGE_DIMENSIONS_MISMATCH"):
        materialize_page_candidates(
            content, middle, page_images={0: page}, page_dimensions={0: (999, 999)},
            document_id="licensed-doc@abc1234:guide.pdf", source=_source(), ocr_mode="explicit",
            out_path=tmp_path / "candidates.jsonl",
        )
