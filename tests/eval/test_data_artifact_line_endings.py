"""Byte-pinned artifacts must not carry CRLF.

The defect this pins: with ``core.autocrlf=true`` (the Windows default) Git
rewrites LF to CRLF on checkout.  For the artifacts under ``data/`` the bytes
*are* the evidence — each policy is pinned by a ``.sha256`` sidecar, and
queries/qrels bytes are pinned inside ``contamination-policy.v1.json``.  A
converted checkout therefore fails the pin check, and the failure reads as
"the policy was tampered with" or "bytes changed since the freeze" when the
truth is "Git changed the line endings".  That is an infrastructure fault
wearing a business verdict's clothes, the same shape catalogued in the
portfolio's defect chain.

``.gitattributes`` marks these paths ``-text`` so Git stops converting them.
These tests fail if that guard is removed, if a new pinned artifact is added
outside its coverage, or if someone commits CRLF bytes directly.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parents[2]
DATA_ROOT = REPO_ROOT / "data"
GITATTRIBUTES = REPO_ROOT / ".gitattributes"

#: Extensions whose bytes are pinned, hashed, or byte-compared somewhere.
PINNED_SUFFIXES = (".json", ".jsonl", ".sha256", ".rc")


def _pinned_files() -> list[Path]:
    if not DATA_ROOT.is_dir():
        return []
    return sorted(
        p
        for p in DATA_ROOT.rglob("*")
        if p.is_file() and p.suffix in PINNED_SUFFIXES
    )


def test_there_are_pinned_files_to_check() -> None:
    """Guard against the whole suite passing because the glob found nothing.

    Without this, deleting ``data/`` or renaming the extensions would make every
    assertion below vacuously true — a gate whose PASS carries no information.
    """
    assert len(_pinned_files()) >= 20


@pytest.mark.parametrize("path", _pinned_files(), ids=lambda p: p.name)
def test_pinned_artifact_has_no_crlf(path: Path) -> None:
    raw = path.read_bytes()
    crlf = raw.count(b"\r\n")
    assert crlf == 0, (
        f"{path.relative_to(REPO_ROOT)} contains {crlf} CRLF sequences. "
        "Its bytes are pinned, so a line-ending conversion silently breaks the "
        "hash. Check that .gitattributes marks this path -text, then re-checkout."
    )


def test_gitattributes_marks_data_artifacts_as_non_text() -> None:
    """The guard itself must exist; the CRLF assertions above rely on it."""
    assert GITATTRIBUTES.is_file(), ".gitattributes is missing"
    body = GITATTRIBUTES.read_text(encoding="utf-8")
    for pattern in ("data/**/*.json", "data/**/*.jsonl", "data/**/*.sha256"):
        assert pattern in body, f".gitattributes does not cover {pattern}"
        line = next(l for l in body.splitlines() if l.strip().startswith(pattern))
        assert "-text" in line, f"{pattern} is not marked -text"


def test_every_sidecar_matches_its_target() -> None:
    """End-to-end: the pin the sidecar records is the pin on disk."""
    sidecars = sorted(DATA_ROOT.rglob("*.sha256"))
    assert sidecars, "no .sha256 sidecars found"
    for sidecar in sidecars:
        target = sidecar.with_suffix("")
        assert target.is_file(), f"sidecar without target: {sidecar}"
        recorded = sidecar.read_text(encoding="utf-8").split()[0].strip().lower()
        actual = hashlib.sha256(target.read_bytes()).hexdigest()
        assert actual == recorded, (
            f"{target.relative_to(REPO_ROOT)} hashes to {actual} but its "
            f"sidecar records {recorded}"
        )


def test_contamination_policy_input_pins_are_lf_bytes() -> None:
    """The E4 policy's queries/qrels pins must match the LF bytes on disk.

    Pins the specific regression: these two were originally computed over a
    CRLF-converted working tree, so they only verified on a Windows clone.
    """
    policy_path = DATA_ROOT / "eval" / "contamination" / "contamination-policy.v1.json"
    if not policy_path.is_file():
        pytest.skip("E4 contamination policy not present")
    pins = json.loads(policy_path.read_text(encoding="utf-8"))["input_pins"]
    for file_key, sha_key, bytes_key in (
        ("queries_file", "queries_sha256", "queries_bytes"),
        ("qrels_file", "qrels_sha256", "qrels_bytes"),
        ("split_policy_file", "split_policy_sha256", None),
    ):
        target = REPO_ROOT / pins[file_key]
        raw = target.read_bytes()
        assert raw.count(b"\r\n") == 0, f"{file_key} is CRLF on disk"
        assert hashlib.sha256(raw).hexdigest() == pins[sha_key], (
            f"{file_key} pin does not match its LF bytes"
        )
        if bytes_key is not None:
            assert len(raw) == pins[bytes_key], f"{bytes_key} disagrees with LF length"
