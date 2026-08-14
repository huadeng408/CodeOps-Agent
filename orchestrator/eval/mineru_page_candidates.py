"""Materialize non-gold page evidence candidates from explicit MinerU OCR.

This converts the existing MinerU element contract into immutable review
candidates.  It is deliberately one-way: the output is ``AI_CANDIDATE`` only;
human review and qrels release remain separate gates.
"""

from __future__ import annotations

import hashlib
import json
import math
import re
import struct
from collections.abc import Mapping
from pathlib import Path
from typing import Any

from orchestrator.eval.multimodal_candidate import CandidateContractError, validate_candidate
from orchestrator.rag.elements import map_mineru_output


class MinerUPageCandidateError(ValueError):
    """Raised when page evidence cannot be bound to explicit MinerU OCR."""


_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")


def materialize_page_candidates(
    content_list_path: Path | str,
    middle_path: Path | str,
    *,
    page_images: Mapping[int, Path | str],
    page_dimensions: Mapping[int, tuple[int, int]],
    document_id: str,
    source: Mapping[str, str],
    ocr_mode: str,
    out_path: Path | str,
    ocr_receipt_path: Path | str | None = None,
    input_pdf_sha256: str = "",
) -> Path:
    """Write validated pre-review candidates from OCR elements and page assets.

    MinerU ``content_list.json`` bboxes use the project-wide
    ``page_1000_xyxy`` coordinate system already, so this importer preserves
    them exactly. Rendering happens upstream; accepting rendered page files
    here binds asset identity without opening the original PDF a second time.
    """
    if ocr_mode != "explicit":
        raise MinerUPageCandidateError("MINERU_OCR_MODE_REQUIRED")
    content_file = Path(content_list_path)
    middle_file = Path(middle_path)
    content_sha256 = _sha256(content_file, "MINERU_CONTENT_MISSING")
    middle_sha256 = _sha256(middle_file, "MINERU_OUTPUT_INVALID")
    try:
        middle_metadata = json.loads(middle_file.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise MinerUPageCandidateError("MINERU_OCR_RECEIPT_INVALID") from exc
    if not isinstance(middle_metadata, dict):
        raise MinerUPageCandidateError("MINERU_OCR_RECEIPT_INVALID")
    middle_mode = middle_metadata.get("ocr_mode")
    if middle_mode not in (None, "explicit"):
        raise MinerUPageCandidateError("MINERU_OCR_RECEIPT_INVALID")
    receipt_sha256 = ""
    if middle_mode != "explicit":
        if ocr_receipt_path is None:
            raise MinerUPageCandidateError("MINERU_OCR_RECEIPT_INVALID")
        receipt_sha256 = _verify_explicit_ocr_receipt(
            Path(ocr_receipt_path),
            input_pdf_sha256=input_pdf_sha256,
            content_sha256=content_sha256,
            middle_sha256=middle_sha256,
        )
    try:
        elements = map_mineru_output(
            content_file,
            middle_file,
            document_id=document_id,
            element_namespace=document_id,
        )
    except (OSError, ValueError) as exc:
        raise MinerUPageCandidateError(f"MINERU_OUTPUT_INVALID: {exc}") from exc
    if not elements:
        raise MinerUPageCandidateError("MINERU_ELEMENTS_EMPTY")

    rows: list[dict[str, Any]] = []
    for element in elements:
        if element.type == "page_break" or not _has_positive_area(element.bbox):
            continue
        page_index = element.page_index
        image_value = page_images.get(page_index)
        dimensions = page_dimensions.get(page_index)
        if image_value is None or dimensions is None:
            raise MinerUPageCandidateError("PAGE_IMAGE_MISSING")
        image_path = Path(image_value)
        image_sha256 = _sha256(image_path, "PAGE_IMAGE_MISSING")
        if _image_dimensions(image_path) != dimensions:
            raise MinerUPageCandidateError("PAGE_DIMENSIONS_MISMATCH")
        # Element coordinates are already the stable page_1000 contract.
        bbox = element.bbox
        candidate = {
            "schema_version": "multimodal-evidence-candidate/v1",
            "candidate_status": "AI_CANDIDATE",
            "candidate_id": _candidate_id(document_id, element.element_id, content_sha256),
            "document_id": document_id,
            "page_id": f"{document_id}:p{page_index}",
            "element_id": element.element_id,
            "bbox": bbox,
            "coordinate_system": "page_1000_xyxy",
            "page_image_sha256": image_sha256,
            "source": dict(source),
            "mineru": {
                "version": element.parser_version,
                "ocr_mode": "explicit",
                "content_sha256": content_sha256,
                "middle_sha256": middle_sha256,
                **({"receipt_sha256": receipt_sha256} if receipt_sha256 else {}),
            },
        }
        try:
            validate_candidate(candidate)
        except CandidateContractError as exc:
            raise MinerUPageCandidateError(f"CANDIDATE_INVALID: {exc}") from exc
        rows.append(candidate)
    if not rows:
        raise MinerUPageCandidateError("MINERU_NO_PAGE_EVIDENCE")

    destination = Path(out_path)
    if destination.exists():
        raise MinerUPageCandidateError("CANDIDATE_OUTPUT_EXISTS")
    destination.parent.mkdir(parents=True, exist_ok=True)
    destination.write_text(
        "".join(json.dumps(row, ensure_ascii=False, separators=(",", ":")) + "\n" for row in rows),
        encoding="utf-8",
    )
    return destination


def _candidate_id(document_id: str, element_id: str, content_sha256: str) -> str:
    payload = f"{document_id}|{element_id}|{content_sha256}".encode("utf-8")
    return "mineru-" + hashlib.sha256(payload).hexdigest()[:24]


def _has_positive_area(bbox: list[float]) -> bool:
    if len(bbox) != 4:
        return False
    try:
        x1, y1, x2, y2 = (float(value) for value in bbox)
    except (TypeError, ValueError):
        return False
    return all(math.isfinite(value) for value in (x1, y1, x2, y2)) and x2 > x1 and y2 > y1


def _verify_explicit_ocr_receipt(
    receipt_path: Path,
    *,
    input_pdf_sha256: str,
    content_sha256: str,
    middle_sha256: str,
) -> str:
    try:
        receipt = json.loads(receipt_path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise MinerUPageCandidateError("MINERU_OCR_RECEIPT_INVALID") from exc
    if not isinstance(receipt, dict):
        raise MinerUPageCandidateError("MINERU_OCR_RECEIPT_INVALID")
    if (
        receipt.get("schema_version") != "mineru-explicit-ocr-receipt/v1"
        or receipt.get("ocr_mode") != "explicit"
        or receipt.get("exit_code") != 0
        or not _SHA256_RE.fullmatch(input_pdf_sha256)
        or receipt.get("input_pdf_sha256") != input_pdf_sha256
        or receipt.get("content_sha256") != content_sha256
        or receipt.get("middle_sha256") != middle_sha256
    ):
        raise MinerUPageCandidateError("MINERU_OCR_RECEIPT_INVALID")
    return _sha256(receipt_path, "MINERU_OCR_RECEIPT_INVALID")


def _sha256(path: Path, error: str) -> str:
    try:
        return hashlib.sha256(path.read_bytes()).hexdigest()
    except OSError as exc:
        raise MinerUPageCandidateError(error) from exc


def _image_dimensions(path: Path) -> tuple[int, int]:
    """Read PNG/JPEG dimensions without adding a decoder dependency."""
    try:
        data = path.read_bytes()
    except OSError as exc:
        raise MinerUPageCandidateError("PAGE_IMAGE_MISSING") from exc
    if len(data) >= 24 and data[:8] == b"\x89PNG\r\n\x1a\n" and data[12:16] == b"IHDR":
        return struct.unpack(">II", data[16:24])
    if data[:2] == b"\xff\xd8":
        offset = 2
        while offset + 9 <= len(data):
            if data[offset] != 0xFF:
                break
            marker = data[offset + 1]
            offset += 2
            if marker in {0xD8, 0xD9}:
                continue
            length = int.from_bytes(data[offset:offset + 2], "big")
            if length < 2 or offset + length > len(data):
                break
            if marker in {0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF}:
                return (int.from_bytes(data[offset + 5:offset + 7], "big"), int.from_bytes(data[offset + 3:offset + 5], "big"))
            offset += length
    raise MinerUPageCandidateError("PAGE_IMAGE_INVALID")
