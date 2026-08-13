"""Loader for eval/datasets/manifest.yaml (plan Task 7.1)."""

from __future__ import annotations

import hashlib
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
        license_status = record.get("license_status", "")
        license_evidence = record.get("license_evidence")
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
        if license_status != "VERIFIED":
            issues.append(f"{name}: license status {license_status!r} is not VERIFIED")
        if (
            not isinstance(license_evidence, list)
            or not license_evidence
            or any(not isinstance(url, str) or not url.startswith("https://") for url in license_evidence)
        ):
            issues.append(f"{name}: HTTPS license evidence required")
        if not scorer:
            issues.append(f"{name}: scorer required")
        artifacts = record.get("artifacts")
        if not isinstance(artifacts, list) or not artifacts:
            issues.append(f"{name}: artifacts with pinned file hashes required")
        else:
            for index, artifact in enumerate(artifacts):
                if not isinstance(artifact, dict):
                    issues.append(f"{name}: artifacts[{index}] must be an object")
                    continue
                path = artifact.get("path", "")
                artifact_sha = artifact.get("sha256", "")
                if not isinstance(path, str) or not path.strip():
                    issues.append(f"{name}: artifacts[{index}].path required")
                if (
                    not isinstance(artifact_sha, str)
                    or len(artifact_sha) != 64
                    or any(c not in "0123456789abcdef" for c in artifact_sha)
                    or artifact_sha == "0" * 64
                ):
                    issues.append(f"{name}: artifacts[{index}].sha256 must be a pinned 64-char hex digest")
    return issues


def verify_dataset_artifacts(record: dict, cache_root: str | Path) -> list[str]:
    """Verify cached dataset bytes against the record's per-file hashes."""
    name = record.get("name", "?")
    root = Path(cache_root).resolve()
    issues: list[str] = []
    for artifact in record.get("artifacts", []):
        relative = Path(artifact.get("path", ""))
        candidate = (root / relative).resolve()
        try:
            candidate.relative_to(root)
        except ValueError:
            issues.append(f"{name}: artifact path escapes cache root: {relative}")
            continue
        if not candidate.is_file():
            issues.append(f"{name}: artifact missing: {relative.as_posix()}")
            continue
        actual = hashlib.sha256(candidate.read_bytes()).hexdigest()
        expected = artifact.get("sha256", "")
        if actual != expected:
            issues.append(
                f"{name}: artifact sha256 mismatch for {relative.as_posix()}: "
                f"expected {expected}, got {actual}"
            )
    return issues


def dataset_pin_payload(record: dict) -> dict:
    """Return the canonical dataset fields copied into a run manifest."""
    return {
        "dataset_name": record.get("name", ""),
        "dataset_revision": record.get("revision", ""),
        "dataset_hash": record.get("sha256", ""),
        "dataset_source_url": record.get("source_url", ""),
        "dataset_split": record.get("split", ""),
        "dataset_license": record.get("license_spdx", ""),
        "dataset_license_status": record.get("license_status", ""),
        "dataset_scorer": record.get("scorer", ""),
        "dataset_artifacts": record.get("artifacts", []),
    }
