"""Dataset manifest YAML tests (plan Task 7.1)."""

from __future__ import annotations

from pathlib import Path

from eval.datasets.loader import (
    _DEFAULT,
    load_dataset_manifest,
    validate_dataset_records,
)

SHIPPED = Path(__file__).parents[2] / "eval" / "datasets" / "manifest.yaml"


def test_shipped_manifest_loads_four_datasets() -> None:
    records = load_dataset_manifest(SHIPPED)
    names = {r["name"] for r in records}
    assert names == {"beir-nfcorpus", "miracl-zh", "bright", "vidore"}


def test_shipped_manifest_blocked_on_placeholders() -> None:
    """The shipped manifest is shape-valid but every dataset still uses
    all-zero placeholder revisions — the validator must BLOCK real import."""
    records = load_dataset_manifest(SHIPPED)
    issues = validate_dataset_records(records)
    assert len(issues) >= 8  # 4 datasets x (revision + sha256 placeholders)
    assert all("placeholder" in issue for issue in issues)


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
            "scorer": "official",
        }
    ]
    assert validate_dataset_records(records) == []
