"""Corpus manifest validation tests (Task 3.1).

The shipped corpus/sources.yaml currently uses all-zero placeholders for
commit/hash (they must be pinned before real import), so the manifest is
valid *shape-wise* but is expected to be reported as blocked.
"""

from __future__ import annotations

import copy
from pathlib import Path

import pytest
import yaml

from scripts.corpus.validate_manifest import (
    PLACEHOLDER_COMMIT,
    PLACEHOLDER_HASH,
    ManifestError,
    load_schema,
    validate_manifest,
)

MANIFEST = Path(__file__).parents[2] / "corpus" / "sources.yaml"
SCHEMA = Path(__file__).parents[2] / "corpus" / "manifest.schema.json"

VALID_SOURCE = {
    "source_id": "python",
    "repository_url": "https://github.com/python/cpython",
    "source_commit": "0123456789abcdef0123456789abcdef01234567",
    "include_paths": ["Doc/"],
    "exclude_paths": [],
    "license_spdx": "PSF-2.0",
    "license_path": "LICENSE",
    "license_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
    "allowed_formats": ["rst"],
    "expected_documents": 100,
    "loader_user": 1,
    "language": "en",
}


def valid_manifest(sources: list[dict] | None = None) -> dict:
    return {"schema_version": "1", "generation": "techdocs-2026-07-30-v1", "sources": sources or [copy.deepcopy(VALID_SOURCE)]}


def test_schema_itself_is_valid_json() -> None:
    payload = load_schema()
    assert payload["$schema"].startswith("http://json-schema.org/")


def test_manifest_schema_shape_is_frozen() -> None:
    payload = load_schema()
    props = payload["properties"]
    assert "sources" in props
    source_required = payload["definitions"]["source"]["required"]
    for field in ("source_commit", "license_sha256", "allowed_formats", "expected_documents", "loader_user"):
        assert field in source_required, f"required field {field} missing from schema"


def test_valid_manifest_passes() -> None:
    assert validate_manifest_path(yaml.safe_dump(valid_manifest())) == []


def test_floating_branch_commit_rejected() -> None:
    m = valid_manifest()
    m["sources"][0]["source_commit"] = "main"
    issues = validate_manifest_path(yaml.safe_dump(m))
    assert any("not a 40-char immutable commit" in issue for issue in issues)


def test_short_commit_rejected() -> None:
    m = valid_manifest()
    m["sources"][0]["source_commit"] = "0123456789abcdef0123456789abcdef0123456"  # 39 chars
    issues = validate_manifest_path(yaml.safe_dump(m))
    assert any("not a 40-char immutable commit" in issue for issue in issues)


def test_missing_license_hash_rejected() -> None:
    m = valid_manifest()
    del m["sources"][0]["license_sha256"]
    issues = validate_manifest_path(yaml.safe_dump(m))
    assert any("license_sha256" in issue for issue in issues)


def test_bad_license_hash_rejected() -> None:
    m = valid_manifest()
    m["sources"][0]["license_sha256"] = "xyz"
    issues = validate_manifest_path(yaml.safe_dump(m))
    assert any("not a 64-char hex digest" in issue for issue in issues)


def test_unknown_format_rejected() -> None:
    m = valid_manifest()
    m["sources"][0]["allowed_formats"] = ["pdf"]
    issues = validate_manifest_path(yaml.safe_dump(m))
    assert any("schema violation" in issue for issue in issues)


def test_path_traversal_in_include_rejected_by_schema() -> None:
    # A path escaping the source root must fail schema validation (no
    # absolute paths or .. segments are permitted by the pattern).
    m = valid_manifest()
    m["sources"][0]["include_paths"] = ["../../etc/"]
    issues = validate_manifest_path(yaml.safe_dump(m))
    assert any("schema violation" in issue for issue in issues)


def test_placeholder_commit_blocks_import() -> None:
    m = valid_manifest()
    m["sources"][0]["source_commit"] = PLACEHOLDER_COMMIT
    issues = validate_manifest_path(yaml.safe_dump(m))
    assert any("all-zero placeholder" in issue for issue in issues)


def test_placeholder_license_hash_blocks_import() -> None:
    m = valid_manifest()
    m["sources"][0]["license_sha256"] = PLACEHOLDER_HASH
    issues = validate_manifest_path(yaml.safe_dump(m))
    assert any("all-zero placeholder" in issue for issue in issues)


def test_shipped_manifest_is_shape_valid_but_blocked_on_placeholders(tmp_path: Path) -> None:
    shipped = yaml.safe_load(MANIFEST.read_text(encoding="utf-8"))
    assert shipped["schema_version"] == "1"
    assert len(shipped["sources"]) >= 6
    issues = validate_manifest_path(MANIFEST.read_text(encoding="utf-8"))
    # The shipped manifest is schema-valid (no schema violations) but every
    # placeholder commit/hash must be reported as blocking import.
    assert not any("schema violation" in issue for issue in issues)
    assert any("placeholder" in issue for issue in issues)


def test_unreadable_manifest_raises(tmp_path: Path) -> None:
    bogus = tmp_path / "bogus.yaml"
    bogus.write_text(": : : not yaml [[", encoding="utf-8")
    with pytest.raises(ManifestError):
        validate_manifest(bogus)


def validate_manifest_path(content: str) -> list[str]:
    """Run validate_manifest against an in-memory string via a temp file."""
    import tempfile

    with tempfile.NamedTemporaryFile("w", suffix=".yaml", delete=False, encoding="utf-8") as f:
        f.write(content)
        tmp = f.name
    try:
        return validate_manifest(tmp)
    finally:
        Path(tmp).unlink(missing_ok=True)
