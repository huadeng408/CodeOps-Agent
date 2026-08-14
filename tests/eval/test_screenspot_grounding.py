"""ScreenSpot-Pro stays a standalone GUI-grounding evaluation lane."""

from __future__ import annotations

import base64
import hashlib
import json
from pathlib import Path

import pytest

_PNG_2X2 = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAADElEQVR42mP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC"
)


def _fixture(tmp_path: Path, *, bbox: list[int] | None = None, size: list[int] | None = None) -> tuple[Path, Path]:
    annotation = tmp_path / "annotations.json"
    image = tmp_path / "screen.png"
    annotation.write_text(
        json.dumps(
            [
                {
                    "id": "sample-1",
                    "img_filename": "screen.png",
                    "bbox": bbox or [0, 0, 1, 1],
                    "instruction": "Click the upper-left target",
                    "img_size": size or [2, 2],
                    "application": "fixture",
                    "platform": "test",
                    "ui_type": "button",
                    "group": "fixture",
                }
            ]
        ),
        encoding="utf-8",
    )
    image.write_bytes(_PNG_2X2)
    return annotation, image


def _pin_fixture_source(monkeypatch: pytest.MonkeyPatch, annotation: Path, image: Path) -> None:
    import eval.benchmarks.screenspot_grounding as grounding

    monkeypatch.setattr(
        grounding,
        "SCREENSPOT_SAMPLE_MANIFEST",
        {
            "sample-1": {
                "annotation_filename": annotation.name,
                "annotation_sha256": hashlib.sha256(annotation.read_bytes()).hexdigest(),
                "image_filename": "screen.png",
                "image_sha256": hashlib.sha256(image.read_bytes()).hexdigest(),
            }
        },
    )


def test_score_screenspot_center_baseline_writes_isolated_receipt(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from eval.benchmarks.screenspot_grounding import score_screenspot_sample

    annotation, image = _fixture(tmp_path)
    _pin_fixture_source(monkeypatch, annotation, image)
    result = score_screenspot_sample(annotation, image, "sample-1", tmp_path / "out")

    report = json.loads(result.report_path.read_text(encoding="utf-8"))
    assert report["prediction_method"] == "image_center_baseline"
    assert report["prediction_point"] == [1.0, 1.0]
    assert report["point_in_box"] is False
    assert report["point_in_box_rate"] == 0.0
    assert report["gates"] == {
        "document_retrieval": "NOT_APPLICABLE",
        "pdf_rag_gate": "NOT_APPLICABLE",
        "answer_quality": "NOT_APPLICABLE",
        "human_review": "NOT_APPLICABLE",
    }
    receipt = json.loads(result.receipt_path.read_text(encoding="utf-8"))
    assert receipt["source"]["license_spdx"] == "MIT"
    assert receipt["source"]["revision"] == "210e78d3844251110bff86c95835ebd37a6930fa"
    assert receipt["inputs_sha256"]["annotation"]
    assert receipt["inputs_sha256"]["image"]


@pytest.mark.parametrize(
    ("bbox", "size", "error"),
    [([0, 0, 0, 1], [2, 2], "positive area"), ([0, 0, 1, 1], [3, 2], "image dimensions")],
)
def test_score_screenspot_rejects_invalid_gold_or_image(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, bbox: list[int], size: list[int], error: str
) -> None:
    from eval.benchmarks.screenspot_grounding import (
        ScreenSpotSourceError,
        score_screenspot_sample,
    )

    annotation, image = _fixture(tmp_path, bbox=bbox, size=size)
    _pin_fixture_source(monkeypatch, annotation, image)
    with pytest.raises(ScreenSpotSourceError, match=error):
        score_screenspot_sample(annotation, image, "sample-1", tmp_path / "out")


@pytest.mark.parametrize("invalid_coordinate", ["NaN", "Infinity"])
def test_score_screenspot_rejects_non_finite_bbox_coordinates(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, invalid_coordinate: str
) -> None:
    from eval.benchmarks.screenspot_grounding import (
        ScreenSpotSourceError,
        score_screenspot_sample,
    )

    annotation, image = _fixture(tmp_path, bbox=[0, 0, invalid_coordinate, 1])
    _pin_fixture_source(monkeypatch, annotation, image)

    with pytest.raises(ScreenSpotSourceError, match="finite"):
        score_screenspot_sample(annotation, image, "sample-1", tmp_path / "out")


def test_score_screenspot_rejects_source_bytes_or_declared_image_path_tampering(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    from eval.benchmarks.screenspot_grounding import (
        ScreenSpotSourceError,
        score_screenspot_sample,
    )

    annotation, image = _fixture(tmp_path)
    _pin_fixture_source(monkeypatch, annotation, image)
    image.write_bytes(image.read_bytes() + b"tampered")

    with pytest.raises(ScreenSpotSourceError, match="source manifest"):
        score_screenspot_sample(annotation, image, "sample-1", tmp_path / "out")

    annotation, image = _fixture(tmp_path)
    _pin_fixture_source(monkeypatch, annotation, image)
    row = json.loads(annotation.read_text(encoding="utf-8"))[0]
    row["instruction"] = "Tampered annotation content with the same image path"
    annotation.write_text(json.dumps([row]), encoding="utf-8")

    with pytest.raises(ScreenSpotSourceError, match="source manifest"):
        score_screenspot_sample(annotation, image, "sample-1", tmp_path / "out")

    annotation, image = _fixture(tmp_path)
    row = json.loads(annotation.read_text(encoding="utf-8"))[0]
    row["img_filename"] = "renamed.png"
    annotation.write_text(json.dumps([row]), encoding="utf-8")
    _pin_fixture_source(monkeypatch, annotation, image)

    with pytest.raises(ScreenSpotSourceError, match="source manifest"):
        score_screenspot_sample(annotation, image, "sample-1", tmp_path / "out")
