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
    page_artifact_to_evidence,
    page_artifact_to_index_document,
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


def test_visual_package_reexports_artifact_builders() -> None:
    # The package root (__init__.py) re-exports the artifact builders; it must
    # import and behave identically to artifacts.py. Regression: the root used
    # to call Element.asset_ref(), which does not exist.
    from orchestrator.rag.visual import page_crop_artifacts

    crops = page_crop_artifacts("doc-1", 0, "images/p0.png", [_element(0, "image")])
    assert len(crops) == 1
    assert crops[0]["asset_ref"] == "images/p0.png"

    no_asset = _element(1, "image")
    no_asset.image_path = ""  # no asset: skipped via image_path, not asset_ref()
    assert page_crop_artifacts("doc-1", 1, "images/p1.png", [no_asset]) == []


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


def test_clip_encoder_requires_immutable_revision() -> None:
    from orchestrator.rag.visual.encoder import CLIPVisualEncoder

    with pytest.raises(ValueError, match="immutable"):
        CLIPVisualEncoder(model_id="openai/clip-vit-base-patch32", revision="main", device="cpu")


def test_clip_encoder_validates_image_payload_before_model_load() -> None:
    from orchestrator.rag.visual.encoder import CLIPVisualEncoder

    encoder = CLIPVisualEncoder(
        model_id="openai/clip-vit-base-patch32",
        revision="3d74acf9a28c67741b2f4f2ea7635f0aaf6f0268",
        device="cpu",
    )
    with pytest.raises(ValueError, match="valid image"):
        encoder.encode_page(b"not-an-image")


def test_clip_encoder_rejects_empty_query_before_model_load() -> None:
    from orchestrator.rag.visual.encoder import CLIPVisualEncoder

    encoder = CLIPVisualEncoder(
        model_id="openai/clip-vit-base-patch32",
        revision="3d74acf9a28c67741b2f4f2ea7635f0aaf6f0268",
        device="cpu",
    )
    with pytest.raises(ValueError, match="query must not be empty"):
        encoder.encode_query("   ")


def test_clip_encoder_rejects_empty_batch_before_model_load() -> None:
    from orchestrator.rag.visual.encoder import CLIPVisualEncoder

    encoder = CLIPVisualEncoder(
        model_id="openai/clip-vit-base-patch32",
        revision="3d74acf9a28c67741b2f4f2ea7635f0aaf6f0268",
        device="cpu",
    )
    with pytest.raises(ValueError, match="must not be empty"):
        encoder.encode_queries(["valid", " "])


def test_artifacts_serialize_to_json_for_es() -> None:
    artifact = page_artifact("doc-1", 2, "images/p2.png", "s" * 64)
    # Must serialize to the ES doc shape without custom encoders.
    blob = json.dumps(artifact)
    assert '"page_id"' in blob and '"document_id"' in blob


def test_page_artifact_projects_to_visual_evidence_with_page_coordinates() -> None:
    source_sha = "a" * 64
    asset_sha = "b" * 64
    artifact = page_artifact("doc-visual", 4, "pages/p4.png", source_sha, asset_bytes=b"asset")
    assert artifact["asset_sha256"] == hashlib.sha256(b"asset").hexdigest()
    evidence = page_artifact_to_evidence(artifact, parser_version="3.4.4")

    assert evidence.document_id == "doc-visual"
    assert evidence.child_id == "doc-visual:p4:visual-page"
    assert evidence.parent_id == "doc-visual:visual-document"
    assert evidence.modality == "visual"
    assert evidence.coordinates.page_id == "doc-visual:p4"
    assert evidence.coordinates.asset_refs == ("pages/p4.png",)
    assert evidence.coordinates.element_ids == ()
    assert evidence.source_sha256 == source_sha


def test_page_artifact_index_projection_is_visual_only_and_fail_closed() -> None:
    artifact = page_artifact("doc-visual", 0, "pages/p0.png", "c" * 64, asset_bytes=b"asset")
    document = page_artifact_to_index_document(artifact, visual_vector=[0.1, 0.2])

    assert document["document_id"] == "doc-visual"
    assert document["page_id"] == "doc-visual:p0"
    assert document["asset_sha256"] == hashlib.sha256(b"asset").hexdigest()
    assert document["visual_vector"] == [0.1, 0.2]
    assert "text_content" not in document
    assert "embedding_text" not in document

    missing = dict(artifact)
    missing["source_sha256"] = ""
    with pytest.raises(ValueError, match="source_sha256"):
        page_artifact_to_index_document(missing)
