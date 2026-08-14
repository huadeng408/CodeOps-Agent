"""Fail-closed contract for non-gold multimodal evidence candidates."""

from __future__ import annotations

import pytest

from orchestrator.eval.multimodal_candidate import CandidateContractError, validate_candidate


def _candidate() -> dict:
    return {
        "schema_version": "multimodal-evidence-candidate/v1",
        "candidate_id": "docvqa:doc-001:page-002:element-004",
        "candidate_status": "AI_CANDIDATE",
        "source": {
            "source_id": "docvqa-page-pilot",
            "source_revision": "49bf8f13e13c41dd8cdb0cae5314e31c1da1e0d6",
            "license_spdx": "MIT",
        },
        "document_id": "doc-001",
        "page_id": "page-002",
        "element_id": "element-004",
        "bbox": [10.0, 20.0, 30.0, 40.0],
        "coordinate_system": "page_1000_xyxy",
        "page_image_sha256": "a" * 64,
        "mineru": {
            "version": "3.4.4",
            "ocr_mode": "explicit",
            "content_sha256": "b" * 64,
        },
    }


def test_valid_candidate_is_not_a_qrel() -> None:
    result = validate_candidate(_candidate())

    assert result["candidate_status"] == "AI_CANDIDATE"
    assert "review_status" not in result
    assert "relevance" not in result


@pytest.mark.parametrize(
    ("field", "value", "message"),
    [
        ("candidate_status", "HUMAN_REVIEWED", "candidate_status"),
        ("relevance", 1, "qrel-only field"),
        ("bbox", [10, 20, 10, 40], "x2 > x1 and y2 > y1"),
        ("coordinate_system", "normalized", "coordinate_system"),
        ("page_image_sha256", "not-a-hash", "page_image_sha256"),
    ],
)
def test_candidate_contract_rejects_gold_or_untraceable_evidence(
    field: str, value: object, message: str
) -> None:
    candidate = _candidate()
    candidate[field] = value

    with pytest.raises(CandidateContractError, match=message):
        validate_candidate(candidate)


def test_candidate_contract_requires_explicit_mineru_ocr() -> None:
    candidate = _candidate()
    candidate["mineru"] = {"version": "3.4.4", "ocr_mode": "auto", "content_sha256": "b" * 64}

    with pytest.raises(CandidateContractError, match="mineru.ocr_mode"):
        validate_candidate(candidate)


def test_candidate_contract_rejects_xywh_disguised_as_xyxy() -> None:
    candidate = _candidate()
    candidate["bbox"] = [100, 100, 50, 50]

    with pytest.raises(CandidateContractError, match="x2 > x1 and y2 > y1"):
        validate_candidate(candidate)


@pytest.mark.parametrize("coordinate", [float("nan"), float("inf"), float("-inf")])
def test_candidate_contract_rejects_non_finite_bbox_coordinates(coordinate: float) -> None:
    candidate = _candidate()
    candidate["bbox"] = [10.0, 20.0, coordinate, 40.0]

    with pytest.raises(CandidateContractError, match="finite"):
        validate_candidate(candidate)


def test_candidate_contract_rejects_license_outside_project_allowlist() -> None:
    candidate = _candidate()
    candidate["source"] = {**candidate["source"], "license_spdx": "OpenRAIL"}

    with pytest.raises(CandidateContractError, match="license_spdx"):
        validate_candidate(candidate)
