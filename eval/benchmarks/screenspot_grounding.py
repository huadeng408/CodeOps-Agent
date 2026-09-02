"""A separate, non-RAG ScreenSpot-Pro GUI grounding receipt."""

from __future__ import annotations

import hashlib
import json
import math
import struct
from dataclasses import dataclass
from pathlib import Path
from typing import Any

# ScreenSpot grounding evaluates GUI coordinates and does not use the RAG
# trace/pin contract.
TRACE_CAPABILITIES: tuple[str, ...] = ()

SCREENSPOT_REPOSITORY = "likaixin/ScreenSpot-Pro"
SCREENSPOT_REVISION = "210e78d3844251110bff86c95835ebd37a6930fa"
SCREENSPOT_SAMPLE_MANIFEST = {
    "vscode_macos_0": {
        "annotation_filename": "vscode_macos.json",
        "annotation_sha256": "55f9b91d3986d8fb027c76b0518b2eb8aa94139601014c4125f81bf839a4667a",
        "image_filename": "vscode_mac/screenshot_2024-12-03_15-15-02.png",
        "image_sha256": "e2aba1cb9e3b31e3500178d0cebda30615e93fd74d1a3c3b353cc1dd5230cd1d",
    }
}


class ScreenSpotSourceError(ValueError):
    """Raised when the pinned GUI-grounding source cannot be verified."""


@dataclass(frozen=True)
class ScreenSpotScore:
    report_path: Path
    receipt_path: Path


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _png_dimensions(path: Path) -> tuple[int, int]:
    data = path.read_bytes()
    if len(data) < 24 or data[:8] != b"\x89PNG\r\n\x1a\n" or data[12:16] != b"IHDR":
        raise ScreenSpotSourceError("invalid PNG")
    return struct.unpack(">II", data[16:24])


def _find_annotation(path: Path, sample_id: str) -> dict[str, Any]:
    try:
        rows = json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise ScreenSpotSourceError("invalid annotation JSON") from exc
    if not isinstance(rows, list):
        raise ScreenSpotSourceError("annotation must be a list")
    matches = [row for row in rows if isinstance(row, dict) and row.get("id") == sample_id]
    if len(matches) != 1:
        raise ScreenSpotSourceError("sample id must resolve to exactly one annotation")
    return matches[0]


def _bbox(value: Any, dimensions: tuple[int, int]) -> list[float]:
    if not isinstance(value, list) or len(value) != 4:
        raise ScreenSpotSourceError("bbox must have four coordinates")
    try:
        x1, y1, x2, y2 = (float(coordinate) for coordinate in value)
    except (TypeError, ValueError) as exc:
        raise ScreenSpotSourceError("bbox must be numeric") from exc
    if not all(math.isfinite(coordinate) for coordinate in (x1, y1, x2, y2)):
        raise ScreenSpotSourceError("bbox coordinates must be finite")
    width, height = dimensions
    if x2 <= x1 or y2 <= y1:
        raise ScreenSpotSourceError("bbox must have positive area")
    if x1 < 0 or y1 < 0 or x2 > width or y2 > height:
        raise ScreenSpotSourceError("bbox must be inside image dimensions")
    return [x1, y1, x2, y2]


def _validate_source_manifest(
    annotation_file: Path, image_file: Path, annotation: dict[str, Any], sample_id: str
) -> None:
    manifest = SCREENSPOT_SAMPLE_MANIFEST.get(sample_id)
    if manifest is None:
        raise ScreenSpotSourceError("sample id is not in the source manifest")
    if (
        annotation_file.name != manifest["annotation_filename"]
        or annotation.get("img_filename") != manifest["image_filename"]
        or _sha256(annotation_file) != manifest["annotation_sha256"]
        or _sha256(image_file) != manifest["image_sha256"]
    ):
        raise ScreenSpotSourceError("source manifest does not match supplied files")


def score_screenspot_sample(
    annotation_path: Path | str,
    image_path: Path | str,
    sample_id: str,
    out_root: Path | str,
) -> ScreenSpotScore:
    """Score a non-model image-center baseline against one pinned GUI label."""
    annotation_file = Path(annotation_path)
    image_file = Path(image_path)
    if not annotation_file.is_file() or not image_file.is_file():
        raise ScreenSpotSourceError("annotation and image must exist")
    annotation = _find_annotation(annotation_file, sample_id)
    _validate_source_manifest(annotation_file, image_file, annotation, sample_id)
    dimensions = _png_dimensions(image_file)
    expected_size = annotation.get("img_size")
    if expected_size != list(dimensions):
        raise ScreenSpotSourceError("annotation image dimensions do not match PNG")
    gold_bbox = _bbox(annotation.get("bbox"), dimensions)
    width, height = dimensions
    point = [width / 2.0, height / 2.0]
    point_in_box = gold_bbox[0] <= point[0] < gold_bbox[2] and gold_bbox[1] <= point[1] < gold_bbox[3]
    gates = {
        "document_retrieval": "NOT_APPLICABLE",
        "pdf_rag_gate": "NOT_APPLICABLE",
        "answer_quality": "NOT_APPLICABLE",
        "human_review": "NOT_APPLICABLE",
    }
    report = {
        "lane": "screenspot_pro_gui_grounding",
        "prediction_method": "image_center_baseline",
        "sample_id": sample_id,
        "image_filename": annotation.get("img_filename"),
        "image_dimensions": list(dimensions),
        "prediction_point": point,
        "point_in_box": point_in_box,
        "point_in_box_rate": 1.0 if point_in_box else 0.0,
        "iou": 0.0,
        "gates": gates,
    }
    output = Path(out_root)
    output.mkdir(parents=True, exist_ok=True)
    report_path = output / "grounding-report.json"
    report_path.write_text(json.dumps(report, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    receipt = {
        "source": {
            "repository": SCREENSPOT_REPOSITORY,
            "revision": SCREENSPOT_REVISION,
            "license_spdx": "MIT",
        },
        "sample_id": sample_id,
        "inputs_sha256": {"annotation": _sha256(annotation_file), "image": _sha256(image_file)},
        "outputs_sha256": {"grounding-report.json": _sha256(report_path)},
        "gates": gates,
    }
    receipt_path = output / "receipt.json"
    receipt_path.write_text(json.dumps(receipt, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    return ScreenSpotScore(report_path=report_path, receipt_path=receipt_path)
