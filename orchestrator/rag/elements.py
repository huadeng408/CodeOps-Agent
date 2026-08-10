from __future__ import annotations

import hashlib
import json
import math
from pathlib import Path
from typing import Any, Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator

ElementType = Literal[
    "heading",
    "text",
    "table",
    "image",
    "equation",
    "code",
    "list",
    "footnote",
    "page_break",
]


class Element(BaseModel):
    model_config = ConfigDict(extra="ignore")

    document_id: str
    element_id: str
    parent_id: str | None = None
    reading_order: int = 0
    type: ElementType
    sub_type: str = ""
    heading_path: list[str] = Field(default_factory=list)
    page_index: int = 0
    bbox: list[float] = Field(default_factory=lambda: [0.0, 0.0, 0.0, 0.0])
    coordinate_system: str = "page_1000"
    text: str = ""
    html: str = ""
    latex: str = ""
    code_language: str = ""
    image_path: str = ""
    caption: str = ""
    footnote: str = ""
    caption_of: str | None = None
    footnote_of: str | None = None
    continuation_of: str | None = None
    parser_name: str = "mineru"
    parser_version: str = ""
    backend: str = ""
    source_payload_ref: str = ""
    source_sha256: str = ""
    source_url: str = ""

    @field_validator("bbox")
    @classmethod
    def validate_bbox(cls, value: list[float]) -> list[float]:
        if len(value) != 4 or any(not math.isfinite(float(item)) for item in value):
            raise ValueError("bbox must contain four finite numbers")
        return [float(item) for item in value]


def map_mineru_output(content_list_path: Path, middle_path: Path, *, document_id: str) -> list[Element]:
    """Map stable MinerU JSON outputs into the internal element contract."""
    content_payload = _read_json(content_list_path)
    middle_payload = _read_json(middle_path)
    items = content_payload if isinstance(content_payload, list) else content_payload.get("content", [])
    if not isinstance(items, list):
        raise ValueError("MinerU content_list.json must contain a list")

    parser_version = _parser_version(middle_payload)
    backend = _backend(middle_payload)
    source_ref = str(content_list_path)
    source_sha256 = hashlib.sha256(content_list_path.read_bytes()).hexdigest()
    elements: list[Element] = []
    heading_path: list[str] = []
    parent_id: str | None = None

    for reading_order, raw in enumerate(items):
        if not isinstance(raw, dict):
            continue
        element_type = _element_type(raw)
        text = _text_value(raw)
        if not text and element_type == "text":
            continue
        if element_type == "heading":
            level = max(1, int(raw.get("text_level", 1) or 1))
            heading_path = heading_path[: level - 1] + [text]
        element_id = f"{document_id}:p{int(raw.get('page_idx', 0) or 0)}:e{reading_order}"
        element = Element(
            document_id=document_id,
            element_id=element_id,
            parent_id=parent_id,
            reading_order=reading_order,
            type=element_type,
            sub_type=str(raw.get("sub_type", "") or ""),
            heading_path=list(heading_path),
            page_index=int(raw.get("page_idx", 0) or 0),
            bbox=_bbox(raw.get("bbox")),
            text=text,
            html=_html_value(raw),
            latex=str(raw.get("latex", raw.get("equation", "")) or "") if element_type == "equation" else "",
            code_language=str(raw.get("language", raw.get("code_language", "")) or ""),
            image_path=str(raw.get("img_path", raw.get("image_path", "")) or ""),
            caption=_caption_value(raw),
            footnote=_footnote_value(raw),
            parser_version=parser_version,
            backend=backend,
            source_payload_ref=source_ref,
            source_sha256=source_sha256,
        )
        elements.append(element)
        if element_type == "heading":
            parent_id = element_id
    return elements


def _read_json(path: Path) -> Any:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError as exc:
        raise ValueError(f"MinerU payload missing: {path}") from exc


def _parser_version(payload: Any) -> str:
    # MinerU 3.4.4 writes `_version_name`; older/other shapes use `version`.
    if isinstance(payload, dict):
        for key in ("version", "mineru_version", "parser_version", "_version_name"):
            if payload.get(key):
                return str(payload[key])
        model = payload.get("model")
        if isinstance(model, dict) and model.get("version"):
            return str(model["version"])
    return ""


def _backend(payload: Any) -> str:
    # MinerU 3.4.4 writes `_backend`; older/other shapes use `backend`/`method`.
    if isinstance(payload, dict):
        for key in ("backend", "backend_name", "method", "_backend"):
            if payload.get(key):
                return str(payload[key])
    return ""


def _element_type(raw: dict[str, Any]) -> ElementType:
    raw_type = str(raw.get("type", "text") or "text").lower()
    if raw_type in {"table", "image", "equation", "code", "list", "footnote", "page_break"}:
        return raw_type  # type: ignore[return-value]
    if raw_type in {"title", "heading", "section_header"} or int(raw.get("text_level", 0) or 0) > 0:
        return "heading"
    return "text"


def _text_value(raw: dict[str, Any]) -> str:
    value = raw.get("text", raw.get("content", ""))
    return str(value or "").strip()


def _html_value(raw: dict[str, Any]) -> str:
    value = raw.get("table_body", raw.get("html", ""))
    if isinstance(value, list):
        value = "".join(str(item) for item in value)
    return str(value or "")


def _caption_value(raw: dict[str, Any]) -> str:
    value = raw.get("caption", raw.get("image_caption", ""))
    if isinstance(value, list):
        return " ".join(str(item) for item in value)
    return str(value or "")


def _footnote_value(raw: dict[str, Any]) -> str:
    value = raw.get("footnote", raw.get("image_footnote", ""))
    if isinstance(value, list):
        return " ".join(str(item) for item in value)
    return str(value or "")


def _bbox(value: Any) -> list[float]:
    if value is None:
        return [0.0, 0.0, 0.0, 0.0]
    if not isinstance(value, list) or len(value) != 4:
        raise ValueError("MinerU bbox must contain four numbers")
    return [float(item) for item in value]

