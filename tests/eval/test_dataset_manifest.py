"""Dataset manifest YAML tests (plan Task 7.1)."""

from __future__ import annotations

from pathlib import Path

from eval.datasets.loader import (
    _DEFAULT,
    dataset_pin_payload,
    load_dataset_manifest,
    validate_dataset_records,
    verify_dataset_artifacts,
)

SHIPPED = Path(__file__).parents[2] / "eval" / "datasets" / "manifest.yaml"


def test_shipped_manifest_loads_four_datasets() -> None:
    records = load_dataset_manifest(SHIPPED)
    names = {r["name"] for r in records}
    assert names == {"beir-nfcorpus", "miracl-zh", "bright", "vidore"}


def test_shipped_manifest_blocks_unverified_datasets() -> None:
    """The shipped manifest stays blocked until every dataset is verified."""
    records = load_dataset_manifest(SHIPPED)
    issues = validate_dataset_records(records)
    placeholder_issues = [issue for issue in issues if "placeholder" in issue]
    assert {issue.split(":", 1)[0] for issue in placeholder_issues} == {"bright", "vidore"}
    assert any("beir-nfcorpus: license status 'CONFLICT_UNRESOLVED'" in issue for issue in issues)
    assert any("miracl-zh: license status 'CONFLICT_UNRESOLVED'" in issue for issue in issues)
    assert {issue.split(":", 1)[0] for issue in issues if "artifacts" in issue} == {"bright", "vidore"}


def test_validate_rejects_floating_revision() -> None:
    records = [{"name": "x", "revision": "main", "sha256": "0" * 64, "license_spdx": "CC-BY-4.0", "scorer": "s"}]
    issues = validate_dataset_records(records)
    assert any("not pinned" in issue for issue in issues)


def test_validate_rejects_bad_license() -> None:
    records = [{"name": "x", "revision": "0123456789abcdef0123456789abcdef01234567", "sha256": "0" * 64, "license_spdx": "Unknown", "scorer": "s"}]
    issues = validate_dataset_records(records)
    assert any("license" in issue for issue in issues)


def test_validate_accepts_pinned() -> None:
    records = [
        {
            "name": "x",
            "revision": "0123456789abcdef0123456789abcdef01234567",
            "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
            "license_spdx": "CC-BY-4.0",
            "license_status": "VERIFIED",
            "license_evidence": ["https://example.invalid/license"],
            "scorer": "official",
            "artifacts": [
                {
                    "path": "snapshot.jsonl",
                    "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
                }
            ],
        }
    ]
    assert validate_dataset_records(records) == []


def test_validate_rejects_missing_artifact_file_pins() -> None:
    records = [
        {
            "name": "x",
            "revision": "0123456789abcdef0123456789abcdef01234567",
            "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
            "license_spdx": "CC-BY-4.0",
            "scorer": "official",
        }
    ]
    issues = validate_dataset_records(records)
    assert any("artifacts" in issue for issue in issues)


def test_verify_dataset_artifacts_checks_cached_bytes(tmp_path: Path) -> None:
    artifact = tmp_path / "queries.jsonl"
    artifact.write_bytes(b'{"id":"q1"}\n')
    record = {
        "name": "x",
        "artifacts": [
            {
                "path": "queries.jsonl",
                "sha256": "55bc597f21fb11e320c90585ad47ab8cd07305dca121566a24c3bae84ed52d14",
            }
        ],
    }

    assert verify_dataset_artifacts(record, tmp_path) == []

    artifact.write_bytes(b'{"id":"tampered"}\n')
    issues = verify_dataset_artifacts(record, tmp_path)
    assert len(issues) == 1
    assert "sha256 mismatch" in issues[0]


def test_validate_rejects_unresolved_license_evidence() -> None:
    records = [
        {
            "name": "x",
            "revision": "0123456789abcdef0123456789abcdef01234567",
            "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
            "license_spdx": "CC-BY-SA-4.0",
            "license_status": "CONFLICT_UNRESOLVED",
            "license_evidence": ["https://example.invalid/upstream-terms"],
            "scorer": "official",
            "artifacts": [
                {
                    "path": "snapshot.jsonl",
                    "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
                }
            ],
        }
    ]
    issues = validate_dataset_records(records)
    assert any("license status" in issue for issue in issues)


def test_validate_requires_license_evidence() -> None:
    records = [
        {
            "name": "x",
            "revision": "0123456789abcdef0123456789abcdef01234567",
            "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
            "license_spdx": "CC-BY-4.0",
            "license_status": "VERIFIED",
            "scorer": "official",
            "artifacts": [
                {
                    "path": "snapshot.jsonl",
                    "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
                }
            ],
        }
    ]
    issues = validate_dataset_records(records)
    assert any("license evidence" in issue for issue in issues)


def test_dataset_pin_payload_propagates_verified_manifest_fields() -> None:
    record = {
        "name": "x",
        "revision": "0123456789abcdef0123456789abcdef01234567",
        "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        "license_spdx": "CC-BY-4.0",
        "license_status": "VERIFIED",
        "license_evidence": ["https://example.invalid/license"],
        "scorer": "official-x",
        "source_url": "https://example.invalid/dataset",
        "split": "test",
        "artifacts": [
            {
                "path": "snapshot.jsonl",
                "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
            }
        ],
    }

    assert dataset_pin_payload(record) == {
        "dataset_name": "x",
        "dataset_revision": record["revision"],
        "dataset_hash": record["sha256"],
        "dataset_source_url": record["source_url"],
        "dataset_split": "test",
        "dataset_license": "CC-BY-4.0",
        "dataset_license_status": "VERIFIED",
        "dataset_scorer": "official-x",
        "dataset_artifacts": record["artifacts"],
    }
