"""Fail-closed validation for non-gold multimodal evidence candidates.

Candidate records are pre-review MinerU OCR outputs. They are deliberately not
qrels: a candidate never carries relevance, scores, or a human review state.
"""

from __future__ import annotations

import math
import re
from typing import Any

from eval.manifest import ALLOWED_LICENSES


SCHEMA_VERSION = "multimodal-evidence-candidate/v1"
CANDIDATE_STATUS = "AI_CANDIDATE"
COORDINATE_SYSTEM = "page_1000_xyxy"
_SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
_QREL_ONLY_FIELDS = frozenset(
    {
        "relevance",
        "review_status",
        "reviewer_hash",
        "score",
        "metrics",
    }
)


class CandidateContractError(ValueError):
    """Raised when a record cannot remain a non-gold evidence candidate."""


def validate_candidate(candidate: dict[str, Any]) -> dict[str, Any]:
    """Validate and return one immutable-pre-review candidate record.

    This validates provenance only. It cannot promote a record to a qrel or a
    human decision; such a transition requires a separate real review artifact.
    """
    if not isinstance(candidate, dict):
        raise CandidateContractError("candidate must be an object")

    forbidden = sorted(_QREL_ONLY_FIELDS.intersection(candidate))
    if forbidden:
        raise CandidateContractError(f"qrel-only field is forbidden: {forbidden[0]}")

    _require_exact(candidate, "schema_version", SCHEMA_VERSION)
    _require_exact(candidate, "candidate_status", CANDIDATE_STATUS)
    for field in ("candidate_id", "document_id", "page_id", "element_id"):
        _require_nonempty_string(candidate, field)

    source = candidate.get("source")
    if not isinstance(source, dict):
        raise CandidateContractError("source must be an object")
    for field in ("source_id", "source_revision"):
        _require_nonempty_string(source, field)
    license_spdx = source.get("license_spdx")
    if license_spdx not in ALLOWED_LICENSES:
        raise CandidateContractError("source.license_spdx is outside the allowlist")

    _require_exact(candidate, "coordinate_system", COORDINATE_SYSTEM)
    bbox = candidate.get("bbox")
    if not isinstance(bbox, list) or len(bbox) != 4 or not all(
        isinstance(value, (int, float)) and not isinstance(value, bool) for value in bbox
    ):
        raise CandidateContractError("bbox must be [x1, y1, x2, y2] numbers")
    if not all(math.isfinite(value) for value in bbox):
        raise CandidateContractError("bbox coordinates must be finite")
    if not all(0.0 <= value <= 1000.0 for value in bbox):
        raise CandidateContractError("bbox coordinates must be within page_1000_xyxy")
    if bbox[2] <= bbox[0] or bbox[3] <= bbox[1]:
        raise CandidateContractError("bbox must satisfy x2 > x1 and y2 > y1")

    _require_sha256(candidate, "page_image_sha256")
    mineru = candidate.get("mineru")
    if not isinstance(mineru, dict):
        raise CandidateContractError("mineru must be an object")
    _require_nonempty_string(mineru, "version")
    if mineru.get("ocr_mode") != "explicit":
        raise CandidateContractError("mineru.ocr_mode must be 'explicit'")
    _require_sha256(mineru, "content_sha256")
    return candidate


def _require_exact(record: dict[str, Any], field: str, expected: str) -> None:
    if record.get(field) != expected:
        raise CandidateContractError(f"{field} must be {expected!r}")


def _require_nonempty_string(record: dict[str, Any], field: str) -> None:
    if not isinstance(record.get(field), str) or not record[field].strip():
        raise CandidateContractError(f"{field} must be a non-empty string")


def _require_sha256(record: dict[str, Any], field: str) -> None:
    value = record.get(field)
    if not isinstance(value, str) or not _SHA256_RE.fullmatch(value):
        raise CandidateContractError(f"{field} must be a lowercase SHA-256")
