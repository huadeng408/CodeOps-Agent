"""Controlled intake for a genuinely new TechDocs holdout.

The current repository contains only a permanently exposed development set.
This module accepts a future, externally authored question/qrel pair, produces
an agent-safe question view, and records immutable content hashes.  It cannot
prove a semantic claim such as "the author never showed this to an agent";
that claim remains an explicit human attestation rather than a fabricated
machine verdict.
"""

from __future__ import annotations

import hashlib
import json
import os
import tempfile
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from orchestrator.eval.split import dev_qids, require_no_dev_holdout_overlap


SCHEMA_VERSION = "techdocs-holdout-seal/v1"
_REQUIRED_QUESTION_FIELDS = ("query_id", "query", "source_id", "language", "query_type")
_REQUIRED_QREL_FIELDS = (
    "query_id",
    "document_id",
    "section_path",
    "relevance",
    "source_id",
    "language",
    "query_type",
)


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


_DEV_QUERIES_PATH = _repo_root() / "data" / "eval" / "techdocs" / "queries.text.jsonl"
_DEV_QRELS_PATH = _repo_root() / "data" / "eval" / "techdocs" / "qrels.text.jsonl"
REPO_ROOT = _repo_root()


class HoldoutIntakeError(ValueError):
    """Raised when a holdout intake cannot be safely sealed or verified."""


def seal_holdout(
    questions_path: Path | str,
    qrels_path: Path | str,
    attestation_path: Path | str,
    out_dir: Path | str,
) -> Path:
    """Seal externally authored inputs and publish only agent-safe questions.

    ``out_dir`` is intentionally a new directory.  It never receives qrels;
    a privileged scorer must be passed the original qrels path explicitly and
    call :func:`verify_sealed_holdout` before scoring.
    """
    questions_file = Path(questions_path)
    qrels_file = Path(qrels_path)
    attestation_file = Path(attestation_path)
    destination = Path(out_dir)
    for path in (questions_file, qrels_file, attestation_file, destination):
        _require_external_path(path)
    questions = _load_jsonl(questions_file, "HOLDOUT_QUESTIONS")
    qrels = _load_jsonl(qrels_file, "HOLDOUT_QRELS")
    _validate_questions(questions)
    _validate_qrels(qrels)
    _validate_membership(questions, qrels)
    attestation = _load_attestation(attestation_file)

    if destination.exists():
        raise HoldoutIntakeError("HOLDOUT_OUTPUT_EXISTS")
    destination.parent.mkdir(parents=True, exist_ok=True)
    evaluation_view = _question_view_bytes(questions)
    manifest = {
        "schema_version": SCHEMA_VERSION,
        "status": "SEALED_NOT_SCORED",
        "semantic_unseen": "HUMAN_ATTESTED_NOT_MACHINE_PROVABLE",
        "sealed_utc": datetime.now(UTC).isoformat(),
        "question_count": len(questions),
        "qrel_count": len(qrels),
        "questions_sha256": _sha256(questions_file),
        "qrels_sha256": _sha256(qrels_file),
        "attestation_sha256": _sha256(attestation_file),
        "attestation_type": attestation["attestation_type"],
        "evaluation_view": "evaluation-questions.jsonl",
        "evaluation_view_sha256": hashlib.sha256(evaluation_view).hexdigest(),
        "labels_materialized": False,
        "dev_qids_sha256": _qid_hash(dev_qids()),
    }
    with tempfile.TemporaryDirectory(prefix=f".{destination.name}.", dir=destination.parent) as work:
        staged = Path(work)
        (staged / "evaluation-questions.jsonl").write_bytes(evaluation_view)
        (staged / "holdout-manifest.json").write_text(
            json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
        )
        os.replace(staged, destination)
    return destination / "holdout-manifest.json"


def verify_sealed_holdout(
    manifest_path: Path | str,
    questions_path: Path | str,
    qrels_path: Path | str,
    attestation_path: Path | str,
) -> dict[str, Any]:
    """Revalidate a sealed holdout immediately before a privileged score run."""
    manifest_file = Path(manifest_path)
    for path in (manifest_file, Path(questions_path), Path(qrels_path), Path(attestation_path)):
        _require_external_path(path)
    try:
        manifest = json.loads(manifest_file.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise HoldoutIntakeError("HOLDOUT_MANIFEST_INVALID") from exc
    if not isinstance(manifest, dict) or manifest.get("schema_version") != SCHEMA_VERSION:
        raise HoldoutIntakeError("HOLDOUT_MANIFEST_INVALID")
    if manifest.get("status") != "SEALED_NOT_SCORED":
        raise HoldoutIntakeError("HOLDOUT_MANIFEST_STATUS_INVALID")
    if manifest.get("semantic_unseen") != "HUMAN_ATTESTED_NOT_MACHINE_PROVABLE":
        raise HoldoutIntakeError("HOLDOUT_UNSEEN_CLAIM_INVALID")

    evaluation_view = manifest_file.parent / "evaluation-questions.jsonl"
    if manifest.get("evaluation_view") != evaluation_view.name or (
        manifest.get("evaluation_view_sha256") != _sha256(evaluation_view)
    ):
        raise HoldoutIntakeError("HOLDOUT_EVALUATION_VIEW_HASH_MISMATCH")

    questions_file = Path(questions_path)
    qrels_file = Path(qrels_path)
    attestation_file = Path(attestation_path)
    expected_hashes = {
        "questions_sha256": (questions_file, "HOLDOUT_QUESTIONS_HASH_MISMATCH"),
        "qrels_sha256": (qrels_file, "HOLDOUT_QRELS_HASH_MISMATCH"),
        "attestation_sha256": (attestation_file, "HOLDOUT_ATTESTATION_HASH_MISMATCH"),
    }
    for key, (path, error) in expected_hashes.items():
        if manifest.get(key) != _sha256(path):
            raise HoldoutIntakeError(error)

    questions = _load_jsonl(questions_file, "HOLDOUT_QUESTIONS")
    qrels = _load_jsonl(qrels_file, "HOLDOUT_QRELS")
    _validate_questions(questions)
    _validate_qrels(qrels)
    _validate_membership(questions, qrels)
    _load_attestation(attestation_file)
    if manifest.get("dev_qids_sha256") != _qid_hash(dev_qids()):
        raise HoldoutIntakeError("HOLDOUT_DEV_LOCK_CHANGED")
    return manifest


def load_verified_holdout_qrels(
    manifest_path: Path | str,
    questions_path: Path | str,
    qrels_path: Path | str,
    attestation_path: Path | str,
) -> list[dict[str, Any]]:
    """Release holdout labels only after the complete seal has been rechecked.

    This is the only supported hand-off from a sealed holdout to a privileged
    scorer. Generic/dev scoring entrypoints accept arbitrary qrels and must not
    be represented as a holdout evaluation path.
    """
    verify_sealed_holdout(manifest_path, questions_path, qrels_path, attestation_path)
    return _load_jsonl(Path(qrels_path), "HOLDOUT_QRELS")


def _load_jsonl(path: Path, code: str) -> list[dict[str, Any]]:
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except OSError as exc:
        raise HoldoutIntakeError(f"{code}_MISSING") from exc
    rows: list[dict[str, Any]] = []
    for line_number, line in enumerate(lines, 1):
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except json.JSONDecodeError as exc:
            raise HoldoutIntakeError(f"{code}_JSONL_INVALID_LINE_{line_number}") from exc
        if not isinstance(row, dict):
            raise HoldoutIntakeError(f"{code}_NOT_OBJECT_LINE_{line_number}")
        rows.append(row)
    if not rows:
        raise HoldoutIntakeError(f"{code}_EMPTY")
    return rows


def _validate_questions(questions: list[dict[str, Any]]) -> None:
    seen: set[str] = set()
    for row in questions:
        if set(row) != set(_REQUIRED_QUESTION_FIELDS):
            raise HoldoutIntakeError("HOLDOUT_QUESTION_FIELDS_INVALID")
        for field in _REQUIRED_QUESTION_FIELDS:
            if not isinstance(row.get(field), str) or not row[field].strip():
                raise HoldoutIntakeError(f"HOLDOUT_QUESTION_INVALID_{field.upper()}")
        query_id = row["query_id"]
        if query_id in seen:
            raise HoldoutIntakeError("HOLDOUT_QUESTION_DUPLICATE_QID")
        seen.add(query_id)


def _validate_qrels(qrels: list[dict[str, Any]]) -> None:
    for row in qrels:
        for field in _REQUIRED_QREL_FIELDS:
            if field == "section_path":
                if not isinstance(row.get(field), list) or not all(
                    isinstance(item, str) and item.strip() for item in row[field]
                ):
                    raise HoldoutIntakeError("HOLDOUT_QREL_INVALID_SECTION_PATH")
            elif field == "relevance":
                if not isinstance(row.get(field), (int, float)) or isinstance(row[field], bool):
                    raise HoldoutIntakeError("HOLDOUT_QREL_INVALID_RELEVANCE")
            elif not isinstance(row.get(field), str) or not row[field].strip():
                raise HoldoutIntakeError(f"HOLDOUT_QREL_INVALID_{field.upper()}")


def _validate_membership(questions: list[dict[str, Any]], qrels: list[dict[str, Any]]) -> None:
    question_qids = {str(row["query_id"]) for row in questions}
    qrel_qids = {str(row["query_id"]) for row in qrels}
    try:
        require_no_dev_holdout_overlap(dev_qids(), question_qids)
    except ValueError as exc:
        raise HoldoutIntakeError(str(exc)) from exc
    if qrel_qids != question_qids:
        raise HoldoutIntakeError("HOLDOUT_QREL_QUERY_MISMATCH")
    dev_query_texts = {
        _normalize_query_text(str(row["query"]))
        for row in _load_jsonl(_DEV_QUERIES_PATH, "DEV_QUERIES")
    }
    if any(_normalize_query_text(str(row["query"])) in dev_query_texts for row in questions):
        raise HoldoutIntakeError("DEV_QUERY_TEXT_LEAKED_TO_HOLDOUT")
    dev_document_ids = {
        str(row["document_id"])
        for row in _load_jsonl(_DEV_QRELS_PATH, "DEV_QRELS")
        if isinstance(row.get("document_id"), str)
    }
    if any(str(row["document_id"]) in dev_document_ids for row in qrels):
        raise HoldoutIntakeError("DEV_DOCUMENT_LEAKED_TO_HOLDOUT")


def _load_attestation(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise HoldoutIntakeError("HOLDOUT_ATTESTATION_INVALID") from exc
    if not isinstance(value, dict) or value.get("attestation_type") != "HUMAN_NET_NEW":
        raise HoldoutIntakeError("HOLDOUT_ATTESTATION_INVALID")
    for field in ("author_role", "created_utc", "statement"):
        if not isinstance(value.get(field), str) or not value[field].strip():
            raise HoldoutIntakeError(f"HOLDOUT_ATTESTATION_MISSING_{field.upper()}")
    return value


def _sha256(path: Path) -> str:
    try:
        return hashlib.sha256(path.read_bytes()).hexdigest()
    except OSError as exc:
        raise HoldoutIntakeError("HOLDOUT_INPUT_MISSING") from exc


def _qid_hash(qids: tuple[str, ...]) -> str:
    payload = "\n".join(sorted(qids))
    if payload:
        payload += "\n"
    return hashlib.sha256(payload.encode("utf-8")).hexdigest()


def _normalize_query_text(value: str) -> str:
    """Catch exact/whitespace variants without claiming semantic equivalence."""
    return " ".join(value.casefold().split())


def _question_view_bytes(questions: list[dict[str, Any]]) -> bytes:
    """Project an allowlisted record shape instead of copying supplied bytes."""
    return b"".join(
        (json.dumps({field: row[field] for field in _REQUIRED_QUESTION_FIELDS}, ensure_ascii=False) + "\n").encode("utf-8")
        for row in questions
    )


def _require_external_path(path: Path) -> None:
    try:
        path.resolve().relative_to(REPO_ROOT.resolve())
    except ValueError:
        return
    raise HoldoutIntakeError("HOLDOUT_PATH_INSIDE_REPOSITORY")
