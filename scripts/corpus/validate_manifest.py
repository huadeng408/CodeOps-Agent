"""Validate corpus source manifests against the pinned-source contract.

Gate rules (design spec 2026-07-30 §3/§5.1):
- schema shape: source_id/repository_url/source_commit/include/exclude/license
- source_commit must be an immutable 40-char commit (no floating branches)
- license_sha256 must be a 64-char hex digest
- allowed_formats must be from the known set
- every source must declare expected_documents >= 1 and loader_user >= 1
- a source with an unresolved placeholder commit (all-zeros) is reported and
  blocks import (it is not a verified pinned commit)

The checker is pure: it never touches the network or the filesystem beyond
the manifest paths passed in.
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

import jsonschema
import yaml

SCHEMA_PATH = Path(__file__).resolve().parents[2] / "corpus" / "manifest.schema.json"

COMMIT_RE = re.compile(r"^[0-9a-f]{40}$")
HASH_RE = re.compile(r"^[0-9a-f]{64}$")
PLACEHOLDER_COMMIT = "0" * 40
PLACEHOLDER_HASH = "0" * 64


class ManifestError(Exception):
    """Raised when a manifest fails validation."""


def load_schema() -> dict:
    return json.loads(SCHEMA_PATH.read_text(encoding="utf-8"))


def validate_manifest(path: str | Path) -> list[str]:
    """Validate one manifest file; returns the list of blocking issues.

    Raises ManifestError only on unreadable/invalid YAML/JSON; schema and
    policy violations are returned as a list so callers can aggregate.
    """
    manifest_path = Path(path)
    try:
        payload = yaml.safe_load(manifest_path.read_text(encoding="utf-8"))
    except Exception as exc:  # noqa: BLE001 - surface any parse failure
        raise ManifestError(f"{manifest_path}: cannot parse manifest: {exc}") from exc

    schema = load_schema()
    try:
        jsonschema.validate(payload, schema)
    except jsonschema.ValidationError as exc:
        return [f"{manifest_path}: schema violation: {exc.message}"]

    issues: list[str] = []
    for source in payload.get("sources", []):
        sid = source["source_id"]
        commit = source.get("source_commit", "")
        if not COMMIT_RE.match(commit):
            issues.append(f"{sid}: source_commit {commit!r} is not a 40-char immutable commit")
        elif commit == PLACEHOLDER_COMMIT:
            issues.append(f"{sid}: source_commit is the all-zero placeholder; pin the verified upstream commit before import")
        license_hash = source.get("license_sha256", "")
        if not HASH_RE.match(license_hash):
            issues.append(f"{sid}: license_sha256 {license_hash!r} is not a 64-char hex digest")
        elif license_hash == PLACEHOLDER_HASH:
            issues.append(f"{sid}: license_sha256 is the all-zero placeholder; record the real license hash before import")
    return issues


def main(argv: list[str] | None = None) -> int:
    argv = argv if argv is not None else sys.argv[1:]
    if len(argv) != 1:
        print("usage: python -m scripts.corpus.validate_manifest <manifest.yaml>", file=sys.stderr)
        return 2
    issues = validate_manifest(argv[0])
    if issues:
        for issue in issues:
            print(f"BLOCK: {issue}", file=sys.stderr)
        print(f"manifest rejected: {len(issues)} blocking issue(s)", file=sys.stderr)
        return 1
    print("manifest OK: all sources pinned and license-verifiable")
    return 0


if __name__ == "__main__":
    sys.exit(main())
