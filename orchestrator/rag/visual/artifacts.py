"""Visual pilot artifacts (design spec §4.2, plan Task 5.1).

These builders consume ONLY MinerU-rendered pages/assets — never the source
PDF. Page and crop artifacts carry source hash + page/element/bbox + model
revision so every visual embedding is traceable to a text document/page and
can live in a physically separate ES index.
"""

from __future__ import annotations

import hashlib
import math
import re
from collections.abc import Mapping
from typing import Any

from orchestrator.rag.elements import Element
from orchestrator.rag.evidence import EvidenceCoordinates, EvidenceUnit

# Bbox scale applied when cropping a page for a visual encoder. The original
# bbox stays in the artifact; the scaled copy is what the encoder consumes.
CROP_SCALE = 1000
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")


def _sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def page_artifact(
    document_id: str,
    page_index: int,
    page_ref: str,
    source_sha256: str,
    asset_bytes: bytes | None = None,
    model: str = "",
    model_revision: str = "",
) -> dict[str, Any]:
    """Build the page-level visual artifact for one rendered page."""
    return {
        "document_id": document_id,
        "page_id": f"{document_id}:p{page_index}",
        "page_index": page_index,
        "page_ref": page_ref,
        "asset_sha256": _sha256(asset_bytes) if asset_bytes else "",
        "source_sha256": source_sha256,
        "model": model,
        "model_revision": model_revision,
        "kind": "page",
    }


def page_crop_artifacts(
    document_id: str,
    page_index: int,
    page_ref: str,
    elements: list[Element],
    model: str = "",
    model_revision: str = "",
) -> list[dict[str, Any]]:
    """Build crop-level artifacts for image/table/equation elements on a page.

    Only elements with an asset reference produce crops; text/heading
    elements are skipped. The original bbox is preserved and a scaled copy is
    added for the encoder.
    """
    crops: list[dict[str, Any]] = []
    for element in elements:
        if element.type not in ("image", "table", "equation"):
            continue
        if not element.image_path:
            continue
        crops.append(
            {
                "document_id": document_id,
                "page_id": f"{document_id}:p{page_index}",
                "page_index": page_index,
                "page_ref": page_ref,
                "element_id": element.element_id,
                "element_type": element.type,
                "asset_ref": element.image_path,
                "bbox": list(element.bbox),
                "bbox_scaled": [value * CROP_SCALE for value in element.bbox],
                "source_sha256": element.source_sha256,
                "model": model,
                "model_revision": model_revision,
                "kind": "crop",
            }
        )
    return crops


def page_artifact_to_evidence(
    artifact: Mapping[str, Any],
    *,
    parser_name: str = "mineru",
    parser_version: str = "",
) -> EvidenceUnit:
    """Project one rendered page into the shared citation contract."""

    values = _validated_page_artifact(artifact)
    resolved_parser_version = parser_version.strip() or str(values.get("parser_version", "")).strip()
    if not parser_name.strip() or not resolved_parser_version:
        raise ValueError("visual page evidence requires parser_name and parser_version")
    document_id = str(values["document_id"])
    page_id = str(values["page_id"])
    return EvidenceUnit(
        document_id=document_id,
        child_id=f"{page_id}:visual-page",
        parent_id=f"{document_id}:visual-document",
        source_sha256=str(values["source_sha256"]),
        parser_name=parser_name,
        parser_version=resolved_parser_version,
        modality="visual",
        coordinates=EvidenceCoordinates(
            page_id=page_id,
            page_span=(int(values["page_index"]), int(values["page_index"])),
            asset_refs=(str(values["page_ref"]),),
        ),
        model_id=str(values.get("model", "")),
        model_version=str(values.get("model_revision", "")),
    )


def page_artifact_to_index_document(
    artifact: Mapping[str, Any],
    *,
    visual_vector: list[float] | None = None,
) -> dict[str, Any]:
    """Build a visual-index document without introducing text-index fields."""

    values = _validated_page_artifact(artifact)
    document = {
        field: values.get(field, "")
        for field in (
            "document_id",
            "page_id",
            "page_index",
            "page_ref",
            "asset_sha256",
            "source_sha256",
            "model",
            "model_revision",
            "kind",
        )
    }
    if visual_vector is not None:
        if not visual_vector or any(not math.isfinite(float(value)) for value in visual_vector):
            raise ValueError("visual_vector must contain finite values")
        document["visual_vector"] = [float(value) for value in visual_vector]
    return document


def _validated_page_artifact(artifact: Mapping[str, Any]) -> Mapping[str, Any]:
    document_id = str(artifact.get("document_id", "")).strip()
    page_id = str(artifact.get("page_id", "")).strip()
    page_ref = str(artifact.get("page_ref", "")).strip()
    source_sha256 = str(artifact.get("source_sha256", "")).strip()
    asset_sha256 = str(artifact.get("asset_sha256", "")).strip()
    page_index = artifact.get("page_index")
    if not document_id:
        raise ValueError("visual page artifact requires document_id")
    if not isinstance(page_index, int) or isinstance(page_index, bool) or page_index < 0:
        raise ValueError("visual page artifact requires a non-negative page_index")
    if page_id != f"{document_id}:p{page_index}":
        raise ValueError("visual page artifact page_id does not match document_id/page_index")
    if not page_ref:
        raise ValueError("visual page artifact requires page_ref")
    if not _SHA256_RE.fullmatch(source_sha256):
        raise ValueError("visual page artifact requires lowercase source_sha256")
    if not _SHA256_RE.fullmatch(asset_sha256):
        raise ValueError("visual page artifact requires lowercase asset_sha256")
    if artifact.get("kind") != "page":
        raise ValueError("visual page artifact kind must be 'page'")
    return artifact
