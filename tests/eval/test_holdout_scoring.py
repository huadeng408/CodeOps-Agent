"""A sealed external holdout may be scored only through its privileged loader."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from orchestrator.eval.holdout import HoldoutIntakeError, seal_holdout
from orchestrator.eval.runner import main as runner_main
from orchestrator.eval.runner import run_verified_holdout_eval


def _write_jsonl(path: Path, rows: list[dict]) -> Path:
    path.write_text("".join(json.dumps(row) + "\n" for row in rows), encoding="utf-8")
    return path


def _sealed_inputs(tmp_path: Path) -> tuple[Path, Path, Path, Path]:
    questions = _write_jsonl(
        tmp_path / "questions.jsonl",
        [{
            "query_id": "external-holdout-001",
            "query": "A question independently authored outside the development corpus.",
            "source_id": "go",
            "language": "en",
            "query_type": "concept",
        }],
    )
    qrels = _write_jsonl(
        tmp_path / "qrels.jsonl",
        [{
            "query_id": "external-holdout-001",
            "document_id": "go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/holdout-only.html",
            "section_path": ["Types"],
            "relevance": 1,
            "source_id": "go",
            "language": "en",
            "query_type": "concept",
        }],
    )
    attestation = tmp_path / "attestation.json"
    attestation.write_text(
        json.dumps({
            "attestation_type": "HUMAN_NET_NEW",
            "author_role": "independent_dataset_steward",
            "created_utc": "2026-08-14T00:00:00Z",
            "statement": "The questions were authored outside the agent-visible development corpus.",
        }),
        encoding="utf-8",
    )
    manifest = seal_holdout(questions, qrels, attestation, tmp_path / "sealed")
    return manifest, questions, qrels, attestation


def test_verified_holdout_eval_releases_labels_only_after_revalidating_the_seal(tmp_path: Path) -> None:
    manifest, questions, qrels, attestation = _sealed_inputs(tmp_path)
    predictions = _write_jsonl(
        tmp_path / "predictions.jsonl",
        [{
            "query_id": "external-holdout-001",
            "document_id": "go@5d29d80b6c9960c3fc39e14e3e8b9a0f52041fed:doc/holdout-only.html",
            "section_path": ["Types"],
            "score": 1.0,
        }],
    )
    report_path = tmp_path / "report.json"

    result = run_verified_holdout_eval(
        holdout_manifest_path=manifest,
        questions_path=questions,
        qrels_path=qrels,
        attestation_path=attestation,
        predictions_path=predictions,
        corpus_generation="test-generation",
        index_alias="test-index",
        output_path=report_path,
    )

    report = json.loads(report_path.read_text(encoding="utf-8"))
    assert result["holdout_manifest_sha256"]
    assert report["meta"]["evaluation_kind"] == "verified_external_holdout"
    assert report["meta"]["holdout_manifest_sha256"] == result["holdout_manifest_sha256"]
    assert report["overall"]["queries"] == 1


def test_verified_holdout_eval_rejects_tampered_labels_before_writing_a_report(tmp_path: Path) -> None:
    manifest, questions, qrels, attestation = _sealed_inputs(tmp_path)
    predictions = _write_jsonl(tmp_path / "predictions.jsonl", [])
    report_path = tmp_path / "report.json"
    qrels.write_text(qrels.read_text(encoding="utf-8").replace('"relevance": 1', '"relevance": 0'), encoding="utf-8")

    with pytest.raises(HoldoutIntakeError, match="HOLDOUT_QRELS_HASH_MISMATCH"):
        run_verified_holdout_eval(
            holdout_manifest_path=manifest,
            questions_path=questions,
            qrels_path=qrels,
            attestation_path=attestation,
            predictions_path=predictions,
            corpus_generation="test-generation",
            index_alias="test-index",
            output_path=report_path,
        )

    assert not report_path.exists()


def test_runner_cli_uses_the_verified_holdout_path_when_all_attestation_inputs_are_supplied(tmp_path: Path) -> None:
    manifest, questions, qrels, attestation = _sealed_inputs(tmp_path)
    predictions = _write_jsonl(tmp_path / "predictions.jsonl", [])
    report_path = tmp_path / "report.json"

    exit_code = runner_main([
        "--qrels-path", str(qrels),
        "--predictions-path", str(predictions),
        "--corpus-generation", "test-generation",
        "--index-alias", "test-index",
        "--output-path", str(report_path),
        "--holdout-manifest-path", str(manifest),
        "--holdout-questions-path", str(questions),
        "--holdout-attestation-path", str(attestation),
    ])

    assert exit_code == 0
    report = json.loads(report_path.read_text(encoding="utf-8"))
    assert report["meta"]["evaluation_kind"] == "verified_external_holdout"
