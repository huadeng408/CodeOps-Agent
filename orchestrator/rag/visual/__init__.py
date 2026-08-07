"""Visual pilot artifacts (design spec §4.2, plan Task 5.1).

These builders consume ONLY MinerU-rendered pages/assets — never the source
PDF. Page and crop artifacts carry source hash + page/element/bbox + model
revision so every visual embedding is traceable to a text document/page and
can live in a physically separate ES index.
"""

from __future__ import annotations

import hashlib
from typing import Any

from orchestrator.rag.elements import Element

# Bbox scale applied when cropping a page for a visual encoder. The original
# bbox stays in the artifact; the scaled copy is what the encoder consumes.
CROP_SCALE = 1000


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
