"""E3 — the split policy artifact is the only source of truth, and it is hash-bound.

Per DESIGN-MAP-2026-08-07 §20.6.3 (E3) a split may not be decided by code
defaults or by whatever happens to be on disk.  It is decided by a committed,
hashed policy artifact.  Consequently:

* no policy file -> raise ``POLICY_MISSING``, never fall back to an implicit split
* bytes disagree with the sha256 sidecar -> raise ``POLICY_HASH_MISMATCH``
* unknown ``policy_version`` -> raise ``POLICY_VERSION_UNKNOWN``, never
  optimistically accept a newer schema

The policy also *claims* provenance numbers about the corpus.  A claim that is
never checked against raw bytes is exactly the kind of false marking this
project forbids, so these tests re-derive every number from
``qrels.text.jsonl`` / ``queries.text.jsonl`` and assert equality.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path

import pytest

from orchestrator.eval.split import (
    POLICY_PATH,
    SUPPORTED_POLICY_VERSIONS,
    load_policy,
    policy_sha256,
)

REPO_ROOT = Path(__file__).resolve().parents[2]
QRELS = REPO_ROOT / "data" / "eval" / "techdocs" / "qrels.text.jsonl"
QUERIES = REPO_ROOT / "data" / "eval" / "techdocs" / "queries.text.jsonl"


def _rows(path: Path) -> list[dict]:
    with path.open(encoding="utf-8") as handle:
        return [json.loads(line) for line in handle if line.strip()]


def test_policy_artifact_exists_and_matches_its_sidecar() -> None:
    """The committed policy bytes must equal the committed sha256 sidecar."""
    assert POLICY_PATH.exists(), f"policy artifact missing: {POLICY_PATH}"

    sidecar = POLICY_PATH.with_suffix(POLICY_PATH.suffix + ".sha256")
    assert sidecar.exists(), f"sha256 sidecar missing: {sidecar}"

    recorded = sidecar.read_text(encoding="utf-8").split()[0]
    actual = hashlib.sha256(POLICY_PATH.read_bytes()).hexdigest()
    assert recorded == actual, "policy bytes drifted from the recorded sidecar digest"

    # policy_sha256() must report the same digest the sidecar records
    assert policy_sha256() == actual


def test_load_policy_returns_v1_schema() -> None:
    policy = load_policy()
    assert policy["policy_id"] == "e3-split-policy"
    assert policy["policy_version"] == "v1"
    assert policy["policy_version"] in SUPPORTED_POLICY_VERSIONS
    assert policy["seed"] == 20260809
    assert policy["design_map_task"] == "E3"

    # Every failure code the policy declares must be a documented contract,
    # not a decorative list: each entry needs a code and a behaviour.
    codes = {entry["code"] for entry in policy["failure_semantics"]}
    for required in (
        "POLICY_MISSING",
        "POLICY_HASH_MISMATCH",
        "POLICY_VERSION_UNKNOWN",
        "HOLDOUT_EMPTY_METRIC_REQUESTED",
        "HOLDOUT_VERIFIED_CLAIMED",
        "DEV_QID_LEAKED_TO_HOLDOUT",
        "DEV_SET_SHRANK",
        "FIREWALL_PATH_DENIED",
        "FIREWALL_PATH_UNKNOWN",
        "MANIFEST_MISSING_FIELD",
        "MANIFEST_STALE",
    ):
        assert required in codes, f"policy does not declare failure code {required}"
    for entry in policy["failure_semantics"]:
        assert entry["behaviour"] == "raise", entry["code"]


def test_missing_policy_raises_policy_missing(tmp_path: Path) -> None:
    """Fail closed: an absent policy must never degrade into a default split."""
    with pytest.raises(ValueError, match="POLICY_MISSING"):
        load_policy(tmp_path / "nope.json")


def test_tampered_policy_raises_hash_mismatch(tmp_path: Path) -> None:
    """A policy whose bytes drifted from its sidecar is not trustworthy."""
    original = json.loads(POLICY_PATH.read_text(encoding="utf-8"))
    sidecar_digest = (
        POLICY_PATH.with_suffix(POLICY_PATH.suffix + ".sha256")
        .read_text(encoding="utf-8")
        .split()[0]
    )

    tampered = tmp_path / "split-policy.v1.json"
    mutated = dict(original)
    mutated["seed"] = 999  # a different seed is a different split
    tampered.write_text(json.dumps(mutated, indent=2), encoding="utf-8")
    (tmp_path / "split-policy.v1.json.sha256").write_text(
        f"{sidecar_digest}  split-policy.v1.json\n", encoding="utf-8"
    )

    with pytest.raises(ValueError, match="POLICY_HASH_MISMATCH"):
        load_policy(tampered)


def test_unknown_policy_version_raises(tmp_path: Path) -> None:
    """A future schema must be rejected loudly, not silently accepted."""
    original = json.loads(POLICY_PATH.read_text(encoding="utf-8"))
    future = dict(original)
    future["policy_version"] = "v99"

    path = tmp_path / "split-policy.v99.json"
    body = json.dumps(future, indent=2).encode("utf-8")
    path.write_bytes(body)
    (tmp_path / "split-policy.v99.json.sha256").write_text(
        f"{hashlib.sha256(body).hexdigest()}  split-policy.v99.json\n", encoding="utf-8"
    )

    with pytest.raises(ValueError, match="POLICY_VERSION_UNKNOWN"):
        load_policy(path)


def test_provenance_claims_match_raw_corpus_bytes() -> None:
    """Re-derive every provenance number instead of trusting the policy text."""
    policy = load_policy()
    prov = policy["provenance"]

    qrels = _rows(QRELS)
    queries = _rows(QUERIES)

    assert prov["total_queries"] == len(queries) == 180
    assert prov["positive_rows"] == sum(1 for r in qrels if int(r["relevance"]) > 0)
    assert prov["pure_negative_rows"] == sum(1 for r in qrels if int(r["relevance"]) == 0)
    assert prov["distinct_docs_referenced_by_qrels"] == len({r["document_id"] for r in qrels})

    # dev.size is a claim about the same population
    assert policy["dev"]["size"] == len({r["query_id"] for r in queries})


def test_strata_distributions_match_raw_corpus_bytes() -> None:
    """A stratum count that does not match the data would silently skew a split."""
    policy = load_policy()
    qrels = _rows(QRELS)

    for field, key in (("source_id", "source_id"), ("language", "language"), ("query_type", "query_type")):
        claimed = policy["strata"][key]
        observed: dict[str, int] = {}
        for row in qrels:
            observed[row[field]] = observed.get(row[field], 0) + 1
        assert claimed == observed, f"stratum {key} disagrees with qrels"


def test_corpus_source_pins_match_document_ids() -> None:
    """Each pinned revision must actually appear in the corpus it claims to pin."""
    policy = load_policy()
    # ``note`` is provenance documentation, not a source entry.
    pins = {k: v for k, v in policy["corpus_source_pins"].items() if k != "note"}
    qrels = _rows(QRELS)

    observed: dict[str, set[str]] = {}
    for row in qrels:
        source, _, rest = row["document_id"].partition("@")
        revision = rest.split(":", 1)[0]
        observed.setdefault(source, set()).add(revision)

    assert set(pins) == set(observed), "pinned sources differ from corpus sources"
    assert len(pins) == 6
    for source, revision in pins.items():
        pinned = revision.split("@")[-1].split(":")[0]
        assert observed[source] == {pinned}, (
            f"{source}: policy pins {pinned!r} but corpus carries {sorted(observed[source])}"
        )
