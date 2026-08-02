"""Loader for eval/datasets/manifest.yaml (plan Task 7.1)."""

from __future__ import annotations

from pathlib import Path

import yaml

from eval.manifest import ALLOWED_LICENSES

_DEFAULT = Path(__file__).resolve().parents[1] / "datasets" / "manifest.yaml"


def load_dataset_manifest(path: str | Path = _DEFAULT) -> list[dict]:
    """Load the YAML dataset manifest; returns list of dataset records."""
    payload = yaml.safe_load(Path(path).read_text(encoding="utf-8"))
    return payload.get("datasets", [])


def validate_dataset_records(records: list[dict]) -> list[str]:
    """Shape-validate dataset records; returns blocking issues.

    Mirrors the manifest schema for datasets: pinned revision, sha256,
    allowlisted license and a scorer are required. Placeholder all-zero
    commits/hashes are reported as blocking (never import with fakes).
    """
    issues: list[str] = []
    for record in records:
        name = record.get("name", "?")
        revision = record.get("revision", "")
        sha = record.get("sha256", "")
        license_spdx = record.get("license_spdx", "")
        scorer = record.get("scorer", "")
        if len(revision) < 7 or " " in revision:
            issues.append(f"{name}: revision {revision!r} is not pinned")
        elif revision == "0" * 40:
            issues.append(f"{name}: revision is the all-zero placeholder")
        if len(sha) != 64 or any(c not in "0123456789abcdef" for c in sha):
            issues.append(f"{name}: sha256 {sha!r} is not a 64-char hex digest")
        elif sha == "0" * 64:
            issues.append(f"{name}: sha256 is the all-zero placeholder")
        if license_spdx not in ALLOWED_LICENSES:
            issues.append(f"{name}: license {license_spdx!r} not in allowlist")
        if not scorer:
            issues.append(f"{name}: scorer required")
    return issues
