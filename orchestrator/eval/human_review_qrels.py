"""Promote a user-confirmed text-review package into a new Qrels version.

The source Qrels and review inputs are immutable.  This module validates every
binding before it writes a separate, schema-valid Qrels JSONL and receipt.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from datetime import UTC, datetime
from pathlib import Path
from typing import Any, Iterable

from orchestrator.eval.metrics import validate_qrels


REVIEW_FIELDS = (
    "verdict_relevant",
    "verdict_answerable",
    "verdict_language_correct",
    "verdict_query_type_correct",
    "verdict_evidence_sufficient",
    "reviewer_notes",
)
PERMISSION_FIELDS = (
    "ocr_permission",
    "page_render_permission",
    "vectorization_permission",
    "internal_evaluation_permission",
    "public_display_permission",
)
QRELS_FIELDS = frozenset(
    {
        "query_id", "document_id", "section_path", "relevance", "source_id", "source_commit",
        "language", "query_type", "evidence_type", "reviewer_hash", "review_status",
        "reviewer_model", "reviewer_revision", "review_prompt_hash", "review_pass",
        "review_confidence", "review_evidence", "review_timestamp", "verdicts",
    }
)


class HumanReviewPromotionError(ValueError):
    """Raised when a review package is not safe to promote."""


def _load_jsonl(path: Path) -> list[dict[str, Any]]:
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise HumanReviewPromotionError(f"cannot read {path}") from exc
    rows: list[dict[str, Any]] = []
    for line_number, line in enumerate(lines, 1):
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except json.JSONDecodeError as exc:
            raise HumanReviewPromotionError(f"{path}:{line_number} is not JSON") from exc
        if not isinstance(row, dict):
            raise HumanReviewPromotionError(f"{path}:{line_number} is not an object")
        rows.append(row)
    return rows


def _load_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise HumanReviewPromotionError(f"cannot read JSON object {path}") from exc
    if not isinstance(value, dict):
        raise HumanReviewPromotionError(f"{path} is not a JSON object")
    return value


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _document_revision(document_id: str, source_id: str) -> str:
    prefix = f"{source_id}@"
    if not document_id.startswith(prefix) or ":" not in document_id:
        raise HumanReviewPromotionError(f"document_id has no pinned revision: {document_id}")
    return document_id[len(prefix):].split(":", 1)[0]


def _validate_source_declarations(paths: Iterable[Path], qrels: list[dict[str, Any]]) -> list[dict[str, str]]:
    declarations: dict[str, dict[str, str]] = {}
    for path in paths:
        payload = _load_json(path)
        declaration = payload.get("declaration")
        if payload.get("artifact_type") != "UNSUBMITTED_SOURCE_DECLARATION" or not isinstance(declaration, dict):
            raise HumanReviewPromotionError(f"invalid source declaration: {path}")
        revision = str(declaration.get("source_revision") or "")
        license_name = str(declaration.get("license") or "")
        if not str(declaration.get("source_location") or "") or not revision or not license_name:
            raise HumanReviewPromotionError(f"source declaration incomplete: {path}")
        if any(declaration.get(field) != "yes" for field in PERMISSION_FIELDS):
            raise HumanReviewPromotionError(f"source declaration lacks required permission: {path}")
        if revision in declarations:
            raise HumanReviewPromotionError(f"duplicate source declaration revision: {revision}")
        declarations[revision] = {
            "path": str(path).replace("\\", "/"),
            "sha256": _sha256(path),
            "source_location": str(declaration["source_location"]),
            "source_revision": revision,
            "license": license_name,
        }

    required: dict[str, str] = {}
    for row in qrels:
        source_id = str(row.get("source_id") or "")
        revision = _document_revision(str(row.get("document_id") or ""), source_id)
        previous = required.setdefault(source_id, revision)
        if previous != revision:
            raise HumanReviewPromotionError(f"source {source_id} has more than one revision")
    missing = {source: revision for source, revision in required.items() if revision not in declarations}
    if missing:
        raise HumanReviewPromotionError(f"missing source declarations: {missing}")
    return [declarations[revision] for _, revision in sorted(required.items())]


def _validate_review_package(
    original: list[dict[str, Any]], filled: list[dict[str, Any]], draft: dict[str, Any], original_sha256: str
) -> tuple[dict[tuple[str, str], dict[str, Any]], str]:
    if not original or len(original) != len(filled):
        raise HumanReviewPromotionError("worksheet rows do not match")
    if draft.get("artifact_type") != "UNSUBMITTED_REVIEW_DRAFT":
        raise HumanReviewPromotionError("review draft artifact type is invalid")
    if draft.get("worksheet_sha256") != original_sha256:
        raise HumanReviewPromotionError("review draft does not bind the original worksheet")
    decisions = draft.get("decisions")
    if not isinstance(decisions, list) or len(decisions) != len(original):
        raise HumanReviewPromotionError("review draft decision count does not match worksheet")

    reviewer_values = {str(row.get("reviewer") or "") for row in original}
    if len(reviewer_values) != 1 or not next(iter(reviewer_values)):
        raise HumanReviewPromotionError("worksheet must identify exactly one reviewer")
    reviewer = next(iter(reviewer_values))
    promoted: dict[tuple[str, str], dict[str, Any]] = {}
    seen_hashes: set[str] = set()
    for index, (before, after, decision) in enumerate(zip(original, filled, decisions), 1):
        if not isinstance(decision, dict):
            raise HumanReviewPromotionError(f"decision {index} is not an object")
        row_hash = str(before.get("row_hash") or "")
        if not row_hash or row_hash in seen_hashes or after.get("row_hash") != row_hash or decision.get("row_hash") != row_hash:
            raise HumanReviewPromotionError(f"row hash binding failed at row {index}")
        seen_hashes.add(row_hash)
        for key in set(before) | set(after):
            if key not in REVIEW_FIELDS and before.get(key) != after.get(key):
                raise HumanReviewPromotionError(f"non-review field changed at row {index}: {key}")
        if any(before.get(field) not in ("", None) for field in REVIEW_FIELDS):
            raise HumanReviewPromotionError(f"original worksheet was prefilled at row {index}")
        for field in REVIEW_FIELDS:
            if after.get(field) != decision.get(field):
                raise HumanReviewPromotionError(f"draft decision mismatch at row {index}: {field}")
        if after.get("verdict_relevant") not in {"yes", "no"}:
            raise HumanReviewPromotionError(f"missing relevance verdict at row {index}")
        if any(after.get(field) not in {"yes", "no"} for field in REVIEW_FIELDS[:5]):
            raise HumanReviewPromotionError(f"invalid review verdict at row {index}")
        if not str(after.get("reviewer_notes") or "").strip():
            raise HumanReviewPromotionError(f"missing reviewer note at row {index}")
        key = (str(after.get("query_id") or ""), str(after.get("document_id") or ""))
        if not all(key) or key in promoted:
            raise HumanReviewPromotionError(f"invalid or duplicate worksheet identity at row {index}")
        promoted[key] = after
    return promoted, reviewer


def promote_human_reviewed_qrels(
    arbitrated_qrels_path: str | Path,
    original_worksheet_path: str | Path,
    filled_worksheet_path: str | Path,
    draft_path: str | Path,
    source_declaration_paths: Iterable[str | Path],
    output_path: str | Path,
    receipt_path: str | Path,
    *,
    promoted_at: str | None = None,
) -> dict[str, Any]:
    """Create a new Qrels version for a user-confirmed human review package."""
    qrels_file = Path(arbitrated_qrels_path)
    original_file = Path(original_worksheet_path)
    filled_file = Path(filled_worksheet_path)
    draft_file = Path(draft_path)
    output_file = Path(output_path)
    receipt_file = Path(receipt_path)
    qrels = _load_jsonl(qrels_file)
    original = _load_jsonl(original_file)
    filled = _load_jsonl(filled_file)
    draft = _load_json(draft_file)
    original_sha256 = _sha256(original_file)
    reviewed, reviewer = _validate_review_package(original, filled, draft, original_sha256)
    declarations = _validate_source_declarations((Path(path) for path in source_declaration_paths), qrels)

    qrels_by_identity = {
        (str(row.get("query_id") or ""), str(row.get("document_id") or "")): row for row in qrels
    }
    if len(qrels_by_identity) != len(qrels) or set(reviewed) - set(qrels_by_identity):
        raise HumanReviewPromotionError("worksheet identities do not match arbitrated qrels")
    if any(qrels_by_identity[key].get("review_status") != "DISPUTED" for key in reviewed):
        raise HumanReviewPromotionError("only disputed qrels may be promoted")

    timestamp = promoted_at or datetime.now(UTC).isoformat()
    reviewer_hash = hashlib.sha256(f"human-review:{reviewer}".encode("utf-8")).hexdigest()[:16]
    output_rows: list[dict[str, Any]] = []
    for row in qrels:
        # AI-arbitration diagnostics stay in the immutable source sidecar.  A
        # formal Qrels version is deliberately projected to its published schema.
        output = {field: value for field, value in row.items() if field in QRELS_FIELDS}
        reviewed_row = reviewed.get((str(row.get("query_id") or ""), str(row.get("document_id") or "")))
        if reviewed_row is not None:
            for field in (
                "reviewer_model", "reviewer_revision", "review_prompt_hash", "review_pass",
                "review_confidence", "review_evidence", "review_timestamp", "verdicts",
            ):
                output.pop(field, None)
            output["relevance"] = 1 if reviewed_row["verdict_relevant"] == "yes" else 0
            output["review_status"] = "HUMAN_REVIEWED"
            output["reviewer_hash"] = reviewer_hash
            output["review_timestamp"] = timestamp
            output["review_evidence"] = f"human-worksheet:sha256:{original_sha256}:row:{reviewed_row['row_hash']}"
        output_rows.append(output)
    schema_issues = validate_qrels(output_rows)
    if schema_issues:
        raise HumanReviewPromotionError(f"promoted qrels violate schema: {schema_issues[:3]}")

    output_bytes = "".join(json.dumps(row, ensure_ascii=False) + "\n" for row in output_rows).encode("utf-8")
    output_sha256 = hashlib.sha256(output_bytes).hexdigest()
    receipt = {
        "artifact_type": "HUMAN_REVIEWED_QRELS_PROMOTION",
        "promoted_at": timestamp,
        "reviewer_hash": reviewer_hash,
        "counts": {"human_reviewed": len(reviewed), "unchanged": len(qrels) - len(reviewed)},
        "inputs": {
            "arbitrated_qrels_sha256": _sha256(qrels_file),
            "worksheet_original_sha256": original_sha256,
            "worksheet_filled_sha256": _sha256(filled_file),
            "review_draft_sha256": _sha256(draft_file),
        },
        "source_declarations": declarations,
        "outputs": {"qrels_sha256": output_sha256},
    }
    output_file.parent.mkdir(parents=True, exist_ok=True)
    receipt_file.parent.mkdir(parents=True, exist_ok=True)
    output_file.write_bytes(output_bytes)
    receipt_file.write_text(json.dumps(receipt, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return receipt


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Promote user-confirmed text reviews into new Qrels")
    parser.add_argument("--arbitrated-qrels", required=True)
    parser.add_argument("--original-worksheet", required=True)
    parser.add_argument("--filled-worksheet", required=True)
    parser.add_argument("--review-draft", required=True)
    parser.add_argument("--source-declaration", action="append", required=True)
    parser.add_argument("--out", required=True)
    parser.add_argument("--receipt", required=True)
    parser.add_argument("--promoted-at")
    args = parser.parse_args(argv)
    try:
        receipt = promote_human_reviewed_qrels(
            args.arbitrated_qrels, args.original_worksheet, args.filled_worksheet,
            args.review_draft, args.source_declaration, args.out, args.receipt,
            promoted_at=args.promoted_at,
        )
    except HumanReviewPromotionError as exc:
        print(f"ERROR: {exc}")
        return 2
    print(json.dumps(receipt["counts"], sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
