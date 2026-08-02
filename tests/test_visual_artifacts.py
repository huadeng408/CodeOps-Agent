"""Visual artifact tests (design spec §4.2, plan Task 5.1).

The visual pilot consumes only MinerU-rendered pages/assets — it must never
re-open the source PDF. Artifacts carry source hash + page/element/bbox +
model/revision so they are traceable and indexable in a physically separate
ES index.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path

import pytest

from orchestrator.rag.elements import Element
from orchestrator.rag.visual.artifacts import (
    CROP_SCALE,
    page_artifact,
    page_crop_artifacts,
)
from orchestrator.rag.visual.encoder import VisualEncoder, encoder_status


def _element(page_index: int, etype: str = "image", bbox: list[float] | None = None) -> Element:
    return Element(
        document_id="doc-1",
        element_id=f"doc-1:p{page_index}:e1",
        reading_order=1,
        type=etype,  # type: ignore[arg-type]
        page_index=page_index,
        bbox=bbox or [0.0, 0.0, 100.0, 100.0],
        image_path=f"images/p{page_index}.png",
        source_sha256="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        parser_version="3.4.4",
    )


def test_page_artifact_has_stable_reference() -> None:
    artifact = page_artifact("doc-1", 3, "images/p3.png", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
    assert artifact["document_id"] == "doc-1"
    assert artifact["page_id"] == "doc-1:p3"
    assert artifact["page_ref"] == "images/p3.png"
    assert artifact["source_sha256"] == "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
    assert artifact["asset_sha256"] == ""
    assert artifact["model_revision"] == ""


def test_page_artifact_hashes_asset_bytes() -> None:
    data = b"png-bytes"
    artifact = page_artifact("doc-1", 1, "images/p1.png", "s" * 64, asset_bytes=data)
    expected = hashlib.sha256(data).hexdigest()
    assert artifact["asset_sha256"] == expected


def test_page_crop_artifacts_scale_bbox() -> None:
    elements = [_element(0, "image", [0.0, 0.0, 50.0, 50.0]), _element(0, "text", [10.0, 10.0, 20.0, 20.0])]
    crops = page_crop_artifacts("doc-1", 0, "images/p0.png", elements)
    # Only the image element produces a crop; text elements do not.
    assert len(crops) == 1
    crop = crops[0]
    assert crop["page_id"] == "doc-1:p0"
    assert crop["element_id"] == "doc-1:p0:e1"
    assert crop["bbox"] == [0.0, 0.0, 50.0, 50.0]
    # Scaled bbox must be derived, not original.
    scaled = crop["bbox_scaled"]
    assert len(scaled) == 4
    assert scaled[0] == 0.0 and scaled[1] == 0.0
    assert scaled[2] == 50.0 * CROP_SCALE and scaled[3] == 50.0 * CROP_SCALE


def test_page_crop_artifacts_require_asset_ref() -> None:
    elements = [_element(0, "image", [0.0, 0.0, 50.0, 50.0])]
    elements[0].image_path = ""  # no asset
    crops = page_crop_artifacts("doc-1", 0, "images/p0.png", elements)
    assert crops == []


def test_encoder_status_disabled_without_gpu() -> None:
    # No GPU / no model: the encoder must report disabled, never crash.
    status, reason = encoder_status(device=None, model=None)
    assert status == "disabled"
    assert "gpu" in reason.lower() or "model" in reason.lower()


def test_encoder_encode_requires_enabled() -> None:
    encoder = VisualEncoder(device=None, model=None)
    with pytest.raises(RuntimeError) as exc:
        encoder.encode_page(b"png-bytes")
    assert "disabled" in str(exc.value)


def test_artifacts_serialize_to_json_for_es() -> None:
    artifact = page_artifact("doc-1", 2, "images/p2.png", "s" * 64)
    # Must serialize to the ES doc shape without custom encoders.
    blob = json.dumps(artifact)
    assert '"page_id"' in blob and '"document_id"' in blob
