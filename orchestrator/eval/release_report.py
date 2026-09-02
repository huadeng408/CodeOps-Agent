"""E5 — RAG release report that refuses to score an inadequate golden set.

The E5 release contract scores "only the locked golden set", reports
pure negatives separately, bind every hash, and emit per-query detail with a
bootstrap CI.  Measured against the corpus as it actually exists, the honest
deliverable is a scorer that **refuses**, because the golden set cannot carry
a release claim:

* 23 of 180 rows passed arbitration (12.8%); the other 157 are DISPUTED;
* all 23 carry relevance 1.0, so precision and false-positive rate are
  undefined rather than merely noisy;
* docker and kubernetes contribute 0 of the 23, so no claim spans the 6
  declared sources;
* all 17 pure negatives are DISPUTED, so E5's own pure-negative deliverable
  has no eligible data;
* 178 of 180 ``section_path`` labels do not resolve against the live index,
  so section-level metrics are structurally uncomputable.

This module follows the E3 precedent in ``split.py``: an unmeasurable quantity
*raises* instead of returning ``0.0``.  A ``0.0`` flows into a release table
and is indistinguishable from a measured zero, which is strictly worse than a
crash.  Every refusal carries a named code from the policy's
``failure_semantics``.

Exit codes (policy ``exit_codes``):

* ``0`` RELEASE_ELIGIBLE — every gate passed and all bindings present
* ``1`` RESERVED_UNUSED
* ``2`` data fault or contract violation; nothing was measured
* ``3`` NOT_RELEASE_ELIGIBLE — gates evaluated, at least one failed

``1`` is deliberately unused.  In the contamination scanner exit 1 is the
BLOCKING business verdict "contamination found", so an escaping exception
there makes a data fault masquerade as a verdict.  Here ``2`` always means
"nothing was measured" and ``3`` always means "measured, then refused", so a
crash can never be read as a release decision.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import random
import re
import statistics
import subprocess
import sys
from collections.abc import Iterable, Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

try:
    from cryptography.exceptions import InvalidSignature
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PublicKey
except ImportError:  # pragma: no cover
    InvalidSignature = ValueError  # type: ignore[assignment,misc]
    Ed25519PublicKey = None  # type: ignore[assignment,misc]

__all__ = [
    "EXIT_ELIGIBLE",
    "EXIT_RESERVED_UNUSED",
    "EXIT_DATA_FAULT",
    "EXIT_NOT_ELIGIBLE",
    "GateResult",
    "KNOWN_POLICY_VERSIONS",
    "POLICY_PATH",
    "QRELS_PATH",
    "QUERIES_PATH",
    "SPLIT_MANIFEST_PATH",
    "bootstrap_ci",
    "current_dirty_hash",
    "evaluate_gates",
    "load_policy",
    "load_external_bindings",
    "load_review_identity",
    "macro_average",
    "main",
    "policy_sha256",
    "validate_model_identity",
    "validate_split_manifest",
    "require_bindings",
    "require_granularity_allowed",
    "require_metric_emission_allowed",
    "require_release_bindings",
    "select_scoreable_rows",
]


def _repo_root() -> Path:
    return Path(__file__).resolve().parents[2]


REPO_ROOT = _repo_root()
POLICY_PATH = REPO_ROOT / "data" / "eval" / "techdocs" / "release-policy.v1.json"
QRELS_PATH = REPO_ROOT / "data" / "eval" / "techdocs" / "qrels.sol-review.jsonl"
QUERIES_PATH = REPO_ROOT / "data" / "eval" / "techdocs" / "queries.text.jsonl"
SPLIT_MANIFEST_PATH = (
    REPO_ROOT / "data" / "eval" / "techdocs" / "splits" / "split-manifest.v1.json"
)
REVIEW_PASS_A_PATH = REPO_ROOT / "data" / "eval" / "techdocs" / "qrels.sol-review-pass-a.jsonl"
REVIEW_PASS_B_PATH = REPO_ROOT / "data" / "eval" / "techdocs" / "qrels.sol-review-pass-b.jsonl"

KNOWN_POLICY_VERSIONS = ("v1",)

#: Only rows that survived arbitration may be scored.
ELIGIBLE_REVIEW_STATUS = "AI_REVIEWED"
#: Rows whose labels were never resolved.
DISPUTED_REVIEW_STATUS = "DISPUTED"
#: Fields every qrel row must carry before it can be reasoned about at all.
REQUIRED_QREL_FIELDS = ("query_id", "document_id", "relevance", "source_id", "review_status")

EXIT_ELIGIBLE = 0
EXIT_RESERVED_UNUSED = 1
EXIT_DATA_FAULT = 2
EXIT_NOT_ELIGIBLE = 3
TRUSTED_POLICY_SHA256 = "db898b98f7d368f12db4a51f248d05d19cae7b27c0807561cde4f3539e06c7f2"

_REPORTER_OWNED_BINDINGS = frozenset(
    {
        "qrels_sha256",
        "queries_sha256",
        "policy_sha256",
        "split_manifest_sha256",
        "model_identity_sha256",
    }
)
_EXTERNAL_BINDINGS = frozenset(
    {
        "git_sha",
        "dirty_hash",
        "scorer_name",
        "scorer_version",
        "index_name",
        "index_alias",
        "index_mapping_hash",
        "index_document_count",
        "index_corpus_generation",
        "index_model_version",
        "predictions_sha256",
    }
)
_SHA256_BINDINGS = frozenset(
    {"dirty_hash", "index_mapping_hash", "predictions_sha256"}
)
_REVIEW_QREL_FIELDS = (
    "document_id",
    "section_path",
    "relevance",
    "source_id",
    "language",
    "query_type",
)


# ---------------------------------------------------------------------------
# Policy binding
# ---------------------------------------------------------------------------


def _sidecar_for(path: Path) -> Path:
    return path.with_name(path.name + ".sha256")


def policy_sha256(path: Path | str | None = None) -> str:
    """sha256 of the policy bytes on disk."""
    target = Path(path) if path is not None else POLICY_PATH
    if not target.is_file():
        raise ValueError(f"POLICY_MISSING: no release policy at {target}")
    return hashlib.sha256(target.read_bytes()).hexdigest()


def load_policy(path: Path | str | None = None) -> dict[str, Any]:
    """Load the release policy, verifying its hash sidecar and version.

    There is no implicit fallback policy: a release decision made against an
    unknown or drifted policy is not reproducible, and a scorer that quietly
    invents its own thresholds is not measuring anything a reader can check.
    """
    target = Path(path) if path is not None else POLICY_PATH
    if not target.is_file():
        raise ValueError(f"POLICY_MISSING: no release policy at {target}")

    raw = target.read_bytes()
    actual = hashlib.sha256(raw).hexdigest()

    sidecar = _sidecar_for(target)
    if not sidecar.is_file():
        raise ValueError(f"POLICY_MISSING: no sha256 sidecar at {sidecar}")
    recorded = sidecar.read_text(encoding="utf-8").split()
    if not recorded:
        raise ValueError(f"POLICY_HASH_MISMATCH: empty sidecar at {sidecar}")
    if recorded[0].lower() != actual:
        raise ValueError(
            "POLICY_HASH_MISMATCH: policy bytes do not match the sidecar; "
            f"expected {recorded[0]}, computed {actual}"
        )

    try:
        policy = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise ValueError(f"POLICY_MISSING: policy at {target} is not valid JSON: {exc}") from exc
    if not isinstance(policy, dict):
        raise ValueError(f"POLICY_MISSING: policy at {target} is not a JSON object")

    version = policy.get("policy_version")
    if version not in KNOWN_POLICY_VERSIONS:
        raise ValueError(
            f"POLICY_VERSION_UNKNOWN: {version!r} is not understood by this code "
            f"(known: {', '.join(KNOWN_POLICY_VERSIONS)}). A newer policy must never "
            "be accepted silently."
        )
    return policy


# ---------------------------------------------------------------------------
# Gate evaluation
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class GateResult:
    """Outcome of evaluating every release gate against a qrels file.

    ``eligible`` is only ``True`` when ``failed_codes`` is empty.  The counts
    are carried alongside the verdict so a reader never has to trust the
    boolean on its own.
    """

    eligible: bool
    failed_codes: tuple[str, ...]
    total_rows: int
    golden_rows: int
    disputed_rows: int
    golden_positive_rows: int
    golden_negative_rows: int
    pure_negative_rows: int
    pure_negative_golden_rows: int
    missing_sources: tuple[str, ...]
    model_identity_status: str = "MODEL_IDENTITY_UNVERIFIED"
    holdout_status: str = "BLOCKED"
    golden_sources: Mapping[str, int] = field(default_factory=dict)
    detail: Mapping[str, str] = field(default_factory=dict)


def _load_qrels(path: Path | str) -> list[dict[str, Any]]:
    target = Path(path)
    if not target.is_file():
        raise ValueError(f"QRELS_MISSING: no qrels file at {target}")
    rows: list[dict[str, Any]] = []
    for lineno, line in enumerate(
        target.read_text(encoding="utf-8").splitlines(), start=1
    ):
        line = line.strip()
        if not line:
            continue
        try:
            row = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(
                f"QRELS_SCHEMA_INVALID: line {lineno} of {target} is not valid JSON: {exc}"
            ) from exc
        if not isinstance(row, dict):
            raise ValueError(
                f"QRELS_SCHEMA_INVALID: line {lineno} of {target} is not an object"
            )
        missing = [f for f in REQUIRED_QREL_FIELDS if f not in row]
        if missing:
            raise ValueError(
                f"QRELS_SCHEMA_INVALID: line {lineno} of {target} lacks "
                f"{', '.join(missing)}"
            )
        rows.append(row)
    if not rows:
        raise ValueError(f"QRELS_MISSING: {target} contains no rows")
    qids = [str(row["query_id"]) for row in rows]
    duplicates = sorted({qid for qid in qids if qids.count(qid) > 1})
    if duplicates:
        raise ValueError(
            "QRELS_DUPLICATE_QID: duplicate query_id values would inflate release "
            f"thresholds: {', '.join(duplicates)}"
        )
    return rows


def _relevance(row: Mapping[str, Any]) -> float:
    try:
        return float(row["relevance"])
    except (TypeError, ValueError) as exc:
        raise ValueError(
            f"QRELS_SCHEMA_INVALID: relevance {row.get('relevance')!r} for "
            f"{row.get('query_id')!r} is not a number"
        ) from exc


def validate_model_identity(identity: Mapping[str, Any] | None) -> tuple[str, str]:
    """Validate response-side identity for two independent review passes.

    Requested model names and caller-supplied booleans are inputs, not
    evidence.  Each pass must preserve the provider-reported model, response
    ID and immutable revision/fingerprint.  The passes must be distinct and
    must identify the same provider revision.
    """
    if not isinstance(identity, Mapping):
        return "MODEL_IDENTITY_UNVERIFIED", "no response-side model identity artifact"
    if identity.get("attestation_status") != "MODEL_IDENTITY_ATTESTED":
        return "MODEL_IDENTITY_UNVERIFIED", "independent signed review attestation is absent"

    endpoint_host = str(identity.get("endpoint_host", "")).strip().lower()
    passes = identity.get("passes")
    if not endpoint_host or not isinstance(passes, list) or len(passes) != 2:
        return (
            "MODEL_IDENTITY_UNVERIFIED",
            "endpoint host and exactly two response-side review pass identities are required",
        )
    if identity.get("passes_independent") is not True:
        return (
            "MODEL_REVIEW_PASSES_NOT_INDEPENDENT",
            "review pass response ID disjointness was not explicitly attested",
        )

    normalized: list[tuple[str, str, str, str]] = []
    for index, item in enumerate(passes, start=1):
        if not isinstance(item, Mapping):
            return "MODEL_IDENTITY_UNVERIFIED", f"review pass {index} identity is not an object"
        requested = str(item.get("requested_model", "")).strip()
        reported = str(item.get("reported_model", "")).strip()
        response_id = str(item.get("response_id", "")).strip()
        revision = str(item.get("immutable_revision", "")).strip()
        if not reported or not response_id or not revision or revision.lower() == "unknown":
            return (
                "MODEL_IDENTITY_UNVERIFIED",
                f"review pass {index} lacks reported_model, response_id, or immutable_revision",
            )
        if not requested:
            return (
                "MODEL_IDENTITY_UNVERIFIED",
                f"review pass {index} lacks requested_model provenance",
            )
        normalized.append((requested, reported, response_id, revision))

    if normalized[0][2] == normalized[1][2]:
        return (
            "MODEL_REVIEW_PASSES_NOT_INDEPENDENT",
            "both review passes carry the same provider response ID",
        )
    if normalized[0][1] != normalized[1][1] or normalized[0][3] != normalized[1][3]:
        return (
            "MODEL_IDENTITY_UNVERIFIED",
            "review passes do not identify the same provider model revision",
        )
    return "VERIFIED", "two independent response-side identities pin one provider model revision"


def _read_review_sidecar(path: Path) -> tuple[list[dict[str, Any]], bytes]:
    if not path.is_file():
        raise ValueError(f"MODEL_IDENTITY_MISSING: no review sidecar at {path}")
    raw = path.read_bytes()
    rows: list[dict[str, Any]] = []
    for line_no, line in enumerate(raw.decode("utf-8").splitlines(), start=1):
        if not line.strip():
            continue
        try:
            row = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(
                f"MODEL_IDENTITY_INVALID: {path} line {line_no} is not JSON"
            ) from exc
        if not isinstance(row, dict) or not str(row.get("query_id", "")).strip():
            raise ValueError(
                f"MODEL_IDENTITY_INVALID: {path} line {line_no} lacks query_id"
            )
        rows.append(row)
    if not rows:
        raise ValueError(f"MODEL_IDENTITY_INVALID: {path} contains no review rows")
    return rows, raw


def load_review_identity(
    pass_a_path: Path | str,
    pass_b_path: Path | str,
    *,
    expected_qids: set[str],
    expected_qrel_rows: Mapping[str, Mapping[str, Any]] | None = None,
    expected_review_statuses: Mapping[str, str] | None = None,
    expected_qrels_sha256: str | None = None,
    expected_queries_sha256: str | None = None,
    attestation_path: Path | str | None = None,
    attestation_public_key_b64: str = "",
) -> tuple[dict[str, Any], str]:
    """Build a hash-bound identity artifact from the two raw review sidecars."""
    pass_rows: list[list[dict[str, Any]]] = []
    raw_parts: list[bytes] = []
    for label, source in (("A", Path(pass_a_path)), ("B", Path(pass_b_path))):
        rows, raw = _read_review_sidecar(source)
        qids = [str(row["query_id"]) for row in rows]
        if len(qids) != len(set(qids)) or set(qids) != expected_qids:
            raise ValueError(
                f"MODEL_IDENTITY_QID_MISMATCH: review pass {label} does not match qrels"
            )
        pass_rows.append(rows)
        raw_parts.append(raw)

    if expected_qrel_rows is not None:
        for label, rows in zip(("A", "B"), pass_rows, strict=True):
            for row in rows:
                qid = str(row["query_id"])
                expected = expected_qrel_rows.get(qid)
                if expected is None:
                    raise ValueError(
                        f"REVIEW_QREL_MISMATCH: pass {label} has unknown query_id {qid}"
                    )
                mismatched = [
                    field
                    for field in _REVIEW_QREL_FIELDS
                    if row.get(field) != expected.get(field)
                ]
                if mismatched:
                    raise ValueError(
                        "REVIEW_QREL_MISMATCH: review pass "
                        f"{label} row {qid} differs from qrels in "
                        + ", ".join(mismatched)
                    )

    if expected_review_statuses is not None:
        by_pass = [{str(row["query_id"]): row for row in rows} for rows in pass_rows]
        boolean_fields = (
            "answerable", "language_correct", "query_type_correct",
            "relevance_correct", "section_correct", "evidence_sufficient",
        )
        for qid in sorted(expected_qids):
            a = by_pass[0][qid]
            b = by_pass[1][qid]
            av = a.get("verdicts") if isinstance(a.get("verdicts"), dict) else {}
            bv = b.get("verdicts") if isinstance(b.get("verdicts"), dict) else {}
            valid = (
                a.get("review_status") == ELIGIBLE_REVIEW_STATUS
                and b.get("review_status") == ELIGIBLE_REVIEW_STATUS
                and all(av.get(field) is True and bv.get(field) is True for field in boolean_fields)
                and av.get("contamination_risk") == "none"
                and bv.get("contamination_risk") == "none"
                and min(float(av.get("confidence", 0)), float(bv.get("confidence", 0))) >= 0.7
            )
            derived = "AI_REVIEWED" if valid else "DISPUTED"
            if expected_review_statuses.get(qid) != derived:
                raise ValueError(
                    "REVIEW_STATUS_MISMATCH: qrels status for "
                    f"{qid} is {expected_review_statuses.get(qid)!r}, but A/B "
                    f"verdicts deterministically derive {derived!r}"
                )

    identities: list[dict[str, str]] = []
    response_id_sets: list[set[str]] = []
    endpoint_hosts: set[str] | None = None
    for rows in pass_rows:
        hosts = {str(row.get("reviewer_endpoint_host", "")).strip().lower() for row in rows} - {""}
        requested = {str(row.get("reviewer_model", "")).strip() for row in rows} - {""}
        reported = {
            str(row.get("reviewer_reported_model", "")).strip() for row in rows
        } - {""}
        revisions = {
            str(row.get("reviewer_system_fingerprint", "")).strip() for row in rows
        } - {""}
        response_ids = {
            str(row.get("reviewer_response_id", "")).strip() for row in rows
        } - {""}
        complete = (
            len(hosts) == 1
            and len(requested) == 1
            and len(reported) == 1
            and len(revisions) == 1
            and len(response_ids) == len(rows)
            and all(
                row.get("reviewer_identity_status") == "MODEL_IDENTITY_VERIFIED"
                for row in rows
            )
        )
        identities.append(
            {
                "requested_model": next(iter(requested), "") if complete else "",
                "reported_model": next(iter(reported), "") if complete else "",
                "response_id": (
                    hashlib.sha256("\n".join(sorted(response_ids)).encode("utf-8")).hexdigest()
                    if complete
                    else ""
                ),
                "immutable_revision": next(iter(revisions), "") if complete else "",
            }
        )
        response_id_sets.append(response_ids)
        endpoint_hosts = hosts if endpoint_hosts is None else endpoint_hosts & hosts

    digest = hashlib.sha256(
        b"pass-a\0" + raw_parts[0] + b"\0pass-b\0" + raw_parts[1]
    ).hexdigest()
    identity: dict[str, Any] = {
            "endpoint_host": next(iter(endpoint_hosts or ()), ""),
            "passes": identities,
            "passes_independent": not bool(response_id_sets[0] & response_id_sets[1]),
        }
    if (
        attestation_public_key_b64
        and attestation_path is not None
        and Path(attestation_path).is_file()
    ):
        try:
            if expected_qrels_sha256 is None:
                raise ValueError("current qrels digest was not supplied")
            if expected_queries_sha256 is None:
                raise ValueError("current queries digest was not supplied")
            expected_qrels_digest = expected_qrels_sha256.strip().lower()
            if not re.fullmatch(r"[0-9a-f]{64}", expected_qrels_digest):
                raise ValueError("current qrels digest is not SHA-256")
            expected_queries_digest = expected_queries_sha256.strip().lower()
            if not re.fullmatch(r"[0-9a-f]{64}", expected_queries_digest):
                raise ValueError("current queries digest is not SHA-256")
            attestation = json.loads(Path(attestation_path).read_text(encoding="utf-8"))
            signed_payload = {
                "sidecars_sha256": digest,
                "qrels_sha256": expected_qrels_digest,
                "identity": identity,
            }
            signed_payload["queries_sha256"] = expected_queries_digest
            message = json.dumps(
                signed_payload,
                ensure_ascii=False,
                sort_keys=True,
                separators=(",", ":"),
            ).encode("utf-8")
            signature = base64.b64decode(str(attestation["signature_b64"]), validate=True)
            public_key = base64.b64decode(attestation_public_key_b64, validate=True)
            if Ed25519PublicKey is None:
                raise ValueError("MODEL_ATTESTATION_UNAVAILABLE: cryptography is not installed")
            Ed25519PublicKey.from_public_bytes(public_key).verify(signature, message)
            if attestation.get("sidecars_sha256") != digest:
                raise ValueError("MODEL_ATTESTATION_INVALID: sidecar digest does not match")
            if str(attestation.get("qrels_sha256", "")).lower() != expected_qrels_digest:
                raise ValueError("MODEL_ATTESTATION_INVALID: qrels digest does not match")
            if str(attestation.get("queries_sha256", "")).lower() != expected_queries_digest:
                raise ValueError("MODEL_ATTESTATION_INVALID: queries digest does not match")
            identity["attestation_status"] = "MODEL_IDENTITY_ATTESTED"
        except (KeyError, ValueError, TypeError, InvalidSignature, base64.binascii.Error) as exc:
            raise ValueError(f"MODEL_ATTESTATION_INVALID: {exc}") from exc
    return identity, digest


def validate_split_manifest(manifest: Mapping[str, Any] | None) -> tuple[str, str]:
    """Require a non-empty, hash-bound hidden holdout declaration."""
    if not isinstance(manifest, Mapping):
        return "HOLDOUT_NOT_MEASURABLE", "no split manifest was supplied"

    size = manifest.get("holdout_size")
    status = str(manifest.get("holdout_status", "")).strip()
    qids_hash = str(manifest.get("holdout_qids_sha256", "")).strip().lower()
    policy_hash = str(manifest.get("policy_sha256", "")).strip().lower()
    if isinstance(size, bool) or not isinstance(size, int) or size <= 0:
        return "HOLDOUT_NOT_MEASURABLE", "hidden holdout is empty or has an invalid size"
    if status != "VERIFIED":
        return "HOLDOUT_NOT_MEASURABLE", f"hidden holdout status is {status!r}, not VERIFIED"
    for label, digest in (("holdout membership", qids_hash), ("split policy", policy_hash)):
        if len(digest) != 64 or any(char not in "0123456789abcdef" for char in digest):
            return "HOLDOUT_NOT_MEASURABLE", f"{label} SHA-256 is missing or invalid"
        if digest == "0" * 64 or digest == hashlib.sha256(b"").hexdigest():
            return "HOLDOUT_NOT_MEASURABLE", f"{label} SHA-256 cannot identify an empty set"
    return "VERIFIED", "non-empty hidden holdout is status- and hash-bound"


def evaluate_gates(
    qrels_path: Path | str | None = None,
    *,
    policy: Mapping[str, Any] | None = None,
    model_identity: Mapping[str, Any] | None = None,
    split_manifest: Mapping[str, Any] | None = None,
) -> GateResult:
    """Evaluate every release gate. A gate that cannot be evaluated FAILS.

    Never raises for an inadequate set — that is the honest verdict
    (:data:`EXIT_NOT_ELIGIBLE`).  It raises only for a data fault, where
    nothing was measured at all.
    """
    resolved_policy = dict(policy) if policy is not None else load_policy()
    gates = resolved_policy.get("gates", {})
    rows = _load_qrels(qrels_path if qrels_path is not None else QRELS_PATH)

    total = len(rows)
    golden = [r for r in rows if r.get("review_status") == ELIGIBLE_REVIEW_STATUS]
    disputed = [r for r in rows if r.get("review_status") == DISPUTED_REVIEW_STATUS]
    pure_negative = [r for r in rows if _relevance(r) <= 0.0]
    pure_negative_golden = [
        r for r in pure_negative if r.get("review_status") == ELIGIBLE_REVIEW_STATUS
    ]
    golden_positive = [r for r in golden if _relevance(r) > 0.0]
    golden_negative = [r for r in golden if _relevance(r) <= 0.0]

    golden_sources: dict[str, int] = {}
    for row in golden:
        key = str(row.get("source_id", ""))
        golden_sources[key] = golden_sources.get(key, 0) + 1

    declared = tuple(gates.get("declared_sources", ()))
    missing_sources = tuple(s for s in declared if golden_sources.get(s, 0) == 0)

    failed: list[str] = []
    detail: dict[str, str] = {}

    identity_status, identity_detail = validate_model_identity(model_identity)
    identity_policy = gates.get("model_identity", {})
    if identity_status == "VERIFIED" and isinstance(identity_policy, Mapping):
        endpoint_host = str((model_identity or {}).get("endpoint_host", "")).lower()
        allowed_hosts = {
            str(host).strip().lower()
            for host in identity_policy.get("allowed_endpoint_hosts", [])
            if str(host).strip()
        }
        required_model = str(
            identity_policy.get("required_requested_model", "")
        ).strip()
        reported_prefixes = tuple(
            str(prefix).strip()
            for prefix in identity_policy.get("allowed_reported_model_prefixes", [])
            if str(prefix).strip()
        )
        requested_models = {
            str(item.get("requested_model", "")).strip()
            for item in (model_identity or {}).get("passes", [])
            if isinstance(item, Mapping)
        }
        reported_models = {
            str(item.get("reported_model", "")).strip()
            for item in (model_identity or {}).get("passes", [])
            if isinstance(item, Mapping)
        }
        if (
            (allowed_hosts and endpoint_host not in allowed_hosts)
            or (required_model and requested_models != {required_model})
            or (
                reported_prefixes
                and any(
                    not any(
                        re.fullmatch(
                            re.escape(prefix) + r"(?:-[0-9]{4}-[0-9]{2})?",
                            model,
                        )
                        is not None
                        for prefix in reported_prefixes
                    )
                    for model in reported_models
                )
            )
        ):
            identity_status = "MODEL_PROVIDER_MISMATCH"
            identity_detail = (
                "review endpoint host or requested model does not match the "
                "versioned release policy"
            )
    if gates.get("require_model_identity", False) and identity_status != "VERIFIED":
        failed.append(identity_status)
        detail[identity_status] = identity_detail

    holdout_status, holdout_detail = validate_split_manifest(split_manifest)
    if gates.get("require_hidden_holdout", False) and holdout_status != "VERIFIED":
        failed.append(holdout_status)
        detail[holdout_status] = holdout_detail

    min_rows = int(gates.get("min_golden_rows", 0))
    if len(golden) < min_rows:
        failed.append("GOLDEN_SET_TOO_SMALL")
        detail["GOLDEN_SET_TOO_SMALL"] = (
            f"{len(golden)} arbitrated rows < required {min_rows}"
        )

    min_fraction = float(gates.get("min_golden_fraction", 0.0))
    if total and (len(golden) / total) < min_fraction:
        code = "GOLDEN_FRACTION_TOO_LOW"
        failed.append(code)
        detail[code] = (
            f"{len(golden)}/{total} = {len(golden) / total:.3f} < required {min_fraction}"
        )

    if gates.get("require_negative_in_golden", False):
        min_neg = int(gates.get("min_negative_golden_rows", 1))
        if not golden_negative:
            failed.append("GOLDEN_SET_ALL_POSITIVE")
            detail["GOLDEN_SET_ALL_POSITIVE"] = (
                f"all {len(golden)} arbitrated rows are positive; precision and "
                "false-positive rate are undefined without a labelled negative"
            )
        elif len(golden_negative) < min_neg:
            code = "GOLDEN_NEGATIVES_TOO_FEW"
            failed.append(code)
            detail[code] = f"{len(golden_negative)} negative rows < required {min_neg}"

    if gates.get("require_all_declared_sources", False) and missing_sources:
        failed.append("SOURCE_COVERAGE_INCOMPLETE")
        detail["SOURCE_COVERAGE_INCOMPLETE"] = (
            f"no arbitrated rows for: {', '.join(missing_sources)}"
        )

    max_disputed = float(gates.get("max_disputed_fraction", 1.0))
    if total and (len(disputed) / total) > max_disputed:
        failed.append("TOO_MANY_DISPUTED")
        detail["TOO_MANY_DISPUTED"] = (
            f"{len(disputed)}/{total} = {len(disputed) / total:.3f} disputed > "
            f"allowed {max_disputed}"
        )

    # E5's own pure-negative deliverable needs arbitrated negatives to exist.
    if pure_negative and not pure_negative_golden:
        failed.append("PURE_NEGATIVE_SET_UNREVIEWED")
        detail["PURE_NEGATIVE_SET_UNREVIEWED"] = (
            f"all {len(pure_negative)} pure-negative rows are unarbitrated, so the "
            "false-positive / empty-rate report has no eligible data"
        )

    return GateResult(
        eligible=not failed,
        failed_codes=tuple(failed),
        total_rows=total,
        golden_rows=len(golden),
        disputed_rows=len(disputed),
        golden_positive_rows=len(golden_positive),
        golden_negative_rows=len(golden_negative),
        pure_negative_rows=len(pure_negative),
        pure_negative_golden_rows=len(pure_negative_golden),
        missing_sources=missing_sources,
        model_identity_status=identity_status,
        holdout_status=holdout_status,
        golden_sources=golden_sources,
        detail=detail,
    )


# ---------------------------------------------------------------------------
# Refusal guards
# ---------------------------------------------------------------------------


def require_metric_emission_allowed(result: GateResult) -> None:
    """Raise unless every gate passed.

    The E3 precedent: returning ``0.0`` here would be far worse than raising,
    because a zero reaches a release table and reads as a measurement.
    """
    if not result.eligible:
        raise ValueError(
            "METRIC_REQUESTED_WHILE_INELIGIBLE: refusing to emit a release metric; "
            f"failed gates: {', '.join(result.failed_codes)}"
        )


def require_granularity_allowed(
    granularity: str, *, policy: Mapping[str, Any] | None = None
) -> None:
    """Only document-level scoring is admissible at policy v1.

    178 of 180 ``section_path`` labels do not resolve against the index, so a
    section metric would report a structural label defect as retrieval quality.
    """
    resolved = dict(policy) if policy is not None else load_policy()
    allowed = str(resolved.get("scoring", {}).get("granularity", "document"))
    if granularity != allowed:
        raise ValueError(
            f"SECTION_METRIC_REQUESTED: granularity {granularity!r} is not permitted "
            f"at policy {resolved.get('policy_version')}; only {allowed!r} is "
            "admissible because section_path does not resolve against the index"
        )


def select_scoreable_rows(
    rows: Iterable[Mapping[str, Any]],
    *,
    policy: Mapping[str, Any] | None = None,
    strict: bool = False,
) -> tuple[dict[str, Any], ...]:
    """Return only arbitrated rows.

    With ``strict=True`` a DISPUTED row is an error rather than something to
    filter out, so a caller cannot quietly widen its denominator by passing
    unarbitrated rows in.
    """
    selected: list[dict[str, Any]] = []
    for row in rows:
        status = row.get("review_status")
        if status == ELIGIBLE_REVIEW_STATUS:
            selected.append(dict(row))
            continue
        if strict:
            raise ValueError(
                f"DISPUTED_ROW_SCORED: {row.get('query_id')!r} has review_status "
                f"{status!r}; only {ELIGIBLE_REVIEW_STATUS} rows may be scored"
            )
    return tuple(selected)


def macro_average(
    per_query: Mapping[str, float],
    *,
    pure_negative_qids: Iterable[str] = (),
    policy: Mapping[str, Any] | None = None,
) -> float:
    """Macro-average per-query scores, refusing to absorb pure negatives.

    A query with no relevant document scores 0.0 by construction.  Averaging
    it in silently deflates the mean and makes the corpus look worse in a way
    that has nothing to do with retrieval — the defect already present in
    ``bm25-report.json``, which averaged all 180 rows including 17 pure
    negatives.
    """
    negatives = set(pure_negative_qids)
    overlap = sorted(negatives & set(per_query))
    if overlap:
        raise ValueError(
            "PURE_NEGATIVE_IN_MACRO_AVERAGE: "
            f"{', '.join(overlap)} are pure negatives and must be reported as "
            "false-positive / empty-rate instead of averaged into a relevance metric"
        )
    if not per_query:
        raise ValueError(
            "METRIC_REQUESTED_WHILE_INELIGIBLE: no scoreable queries, so a "
            "macro-average would be an empty mean reported as a number"
        )
    return statistics.fmean(per_query.values())


def require_bindings(
    bindings: Mapping[str, Any], *, policy: Mapping[str, Any] | None = None
) -> None:
    """Every ``required_bindings`` entry must be present and non-empty."""
    resolved = dict(policy) if policy is not None else load_policy()
    required = tuple(resolved.get("scoring", {}).get("required_bindings", ()))
    missing = [
        key for key in required if not str(bindings.get(key, "") or "").strip()
    ]
    if missing:
        raise ValueError(
            f"BINDING_MISSING: {', '.join(missing)} absent or empty; a report whose "
            "provenance is not fully bound cannot be release evidence"
        )


def current_dirty_hash(repo_root: Path | str = REPO_ROOT) -> str:
    """Hash all HEAD-relative changes, including non-ignored untracked files."""
    root = Path(repo_root).resolve()
    try:
        diff = subprocess.run(
            ["git", "diff", "--no-ext-diff", "--binary", "HEAD", "--"],
            cwd=root,
            check=True,
            capture_output=True,
        ).stdout
        untracked = subprocess.run(
            ["git", "ls-files", "--others", "--exclude-standard", "-z"],
            cwd=root,
            check=True,
            capture_output=True,
        ).stdout
        top_level = Path(
            subprocess.run(
                ["git", "rev-parse", "--show-toplevel"],
                cwd=root,
                check=True,
                capture_output=True,
                text=True,
                encoding="utf-8",
                errors="strict",
            ).stdout.strip()
        )
    except (OSError, subprocess.CalledProcessError) as exc:
        raise ValueError(f"BINDING_GIT_UNAVAILABLE: {exc}") from exc
    if not diff and not untracked:
        return "0" * 64
    digest = hashlib.sha256(b"tracked\0" + diff + b"\0untracked\0")
    for encoded_path in sorted(path for path in untracked.split(b"\0") if path):
        relative = encoded_path.decode("utf-8", errors="surrogateescape")
        target = top_level / relative
        digest.update(encoded_path)
        digest.update(b"\0")
        digest.update(target.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def _official_scorer_results(
    qrel_rows: Mapping[str, Mapping[str, Any]], predictions: list[dict[str, Any]]
) -> tuple[dict[str, Any], dict[str, dict[str, Any]]]:
    from orchestrator.eval.runner import _score_query

    scoreable_rows = {
        qid: row
        for qid, row in qrel_rows.items()
        if str(row.get("review_status", "")) == ELIGIBLE_REVIEW_STATUS
    }
    if not scoreable_rows:
        raise ValueError(
            "METRIC_REQUESTED_WHILE_INELIGIBLE: no scoreable queries; no AI_REVIEWED "
            "qrels are available for official scoring"
        )
    hits_by_qid: dict[str, list[dict[str, Any]]] = {}
    for prediction in predictions:
        hits_by_qid.setdefault(str(prediction["query_id"]), []).append(prediction)
    per_query: dict[str, dict[str, Any]] = {}
    for qid in sorted(scoreable_rows):
        hits = sorted(
            hits_by_qid.get(qid, []),
            key=lambda hit: (
                -float(hit.get("score", 0.0)),
                str(hit["document_id"]),
                tuple(hit.get("section_path") or []),
            ),
        )
        per_query[qid] = _score_query([dict(scoreable_rows[qid])], hits)
    scoreable_qids = [
        qid
        for qid, row in scoreable_rows.items()
        if float(row.get("relevance", 0.0)) > 0
    ]
    if not scoreable_qids:
        raise ValueError(
            "METRIC_REQUESTED_WHILE_INELIGIBLE: no scoreable queries, so official "
            "metrics cannot be computed for a pure-negative qrels set"
        )
    pure_negative_qids = [qid for qid in scoreable_rows if qid not in scoreable_qids]
    values = [per_query[qid] for qid in scoreable_qids]
    pure_negative_values = [per_query[qid] for qid in pure_negative_qids]
    overall = {
        "queries": len(values),
        "recall@5": statistics.fmean(v["recall@5"] for v in values),
        "mrr@10": statistics.fmean(v["mrr@10"] for v in values),
        "ndcg@10": statistics.fmean(v["ndcg@10"] for v in values),
        "empty_results": sum(v["empty"] for v in values),
        "wrong_hits": sum(v["wrong_hits"] for v in values),
        "pure_negative_queries": len(pure_negative_values),
        "pure_negative_empty_results": sum(v["empty"] for v in pure_negative_values),
        "pure_negative_false_positive_rate": (
            statistics.fmean(float(not v["empty"]) for v in pure_negative_values)
            if pure_negative_values
            else 0.0
        ),
    }
    return overall, per_query


def load_external_bindings(
    path: Path | str,
    *,
    attestation_public_key_b64: str = "",
    expected_qrels_sha256: str | None = None,
    expected_queries_sha256: str | None = None,
    expected_qids: set[str] | None = None,
    expected_qrel_rows: Mapping[str, Mapping[str, Any]] | None = None,
) -> dict[str, Any]:
    """Load provenance owned by the canonical retrieval/scorer run.

    Reporter-owned hashes are always derived from the bytes consumed by this
    process and therefore cannot be supplied or overridden by a caller.
    """
    target = Path(path)
    if not target.is_file():
        raise ValueError(f"BINDING_MISSING: no external binding manifest at {target}")
    try:
        payload = json.loads(target.read_text(encoding="utf-8"))
    except json.JSONDecodeError as exc:
        raise ValueError(f"BINDING_INVALID: {target} is not valid JSON") from exc
    if not isinstance(payload, dict):
        raise ValueError("BINDING_INVALID: external binding manifest is not an object")

    reserved = sorted(set(payload) & _REPORTER_OWNED_BINDINGS)
    if reserved:
        raise ValueError(
            "BINDING_RESERVED: reporter-owned fields cannot be supplied: "
            + ", ".join(reserved)
        )
    unknown = sorted(set(payload) - {"artifact_root", "attestation_path"})
    if unknown:
        raise ValueError("BINDING_INVALID: unknown external fields: " + ", ".join(unknown))
    root = Path(str(payload.get("artifact_root", ""))).resolve()
    checksum_path = root / "checksums.sha256"
    if not checksum_path.is_file():
        raise ValueError("BINDING_MISSING: canonical artifact checksums.sha256 is absent")
    if not attestation_public_key_b64:
        raise ValueError("BINDING_ATTESTATION_MISSING: policy has no artifact trust root")
    attestation_path = Path(str(payload.get("attestation_path", "")))
    if not attestation_path.is_file():
        raise ValueError("BINDING_ATTESTATION_MISSING: signed artifact receipt is absent")
    try:
        attestation = json.loads(attestation_path.read_text(encoding="utf-8"))
        signature = base64.b64decode(str(attestation["signature_b64"]), validate=True)
        public_key = base64.b64decode(attestation_public_key_b64, validate=True)
        checksum_bytes = checksum_path.read_bytes()
        if attestation.get("checksums_sha256") != hashlib.sha256(checksum_bytes).hexdigest():
            raise ValueError("signed checksum digest does not match")
        if Ed25519PublicKey is None:
            raise ValueError("cryptography is not installed")
        Ed25519PublicKey.from_public_bytes(public_key).verify(signature, checksum_bytes)
    except (KeyError, ValueError, TypeError, InvalidSignature, base64.binascii.Error) as exc:
        raise ValueError(f"BINDING_ATTESTATION_INVALID: {exc}") from exc

    pinned: dict[str, str] = {}
    for line in checksum_path.read_text(encoding="utf-8").splitlines():
        digest, sep, rel = line.partition("  ")
        if not sep or not rel or Path(rel).is_absolute() or ".." in Path(rel).parts:
            raise ValueError(f"BINDING_INVALID: malformed checksum entry {line!r}")
        artifact = (root / rel).resolve()
        try:
            artifact.relative_to(root)
        except ValueError as exc:
            raise ValueError(f"BINDING_INVALID: artifact escapes root: {rel}") from exc
        if not artifact.is_file():
            raise ValueError(f"BINDING_CHECKSUM_MISMATCH: missing {rel}")
        actual = hashlib.sha256(artifact.read_bytes()).hexdigest()
        if actual != digest.lower():
            raise ValueError(f"BINDING_CHECKSUM_MISMATCH: {rel}")
        pinned[rel.replace("\\", "/")] = actual

    required_files = {
        "run-manifest.json",
        "predictions.jsonl",
        "scorer/metadata.json",
        "scorer/report.json",
        "scorer/per-query.jsonl",
    }
    missing = sorted(required_files - set(pinned))
    if missing:
        raise ValueError("BINDING_MISSING: unpinned canonical artifacts: " + ", ".join(missing))
    manifest = json.loads((root / "run-manifest.json").read_text(encoding="utf-8"))
    scorer = json.loads((root / "scorer" / "metadata.json").read_text(encoding="utf-8"))
    if not isinstance(manifest, dict) or not isinstance(scorer, dict):
        raise ValueError("BINDING_INVALID: canonical metadata must be JSON objects")
    manifest_qrels_hash = str(manifest.get("qrels_hash", "")).strip().lower()
    if not re.fullmatch(r"[0-9a-f]{64}", manifest_qrels_hash):
        raise ValueError(
            "BINDING_QRELS_MISMATCH: canonical run does not bind a qrels SHA-256 digest"
        )
    manifest_queries_hash = str(manifest.get("queries_hash", "")).strip().lower()
    if not re.fullmatch(r"[0-9a-f]{64}", manifest_queries_hash):
        raise ValueError(
            "BINDING_QUERIES_MISMATCH: canonical run does not bind a queries SHA-256 digest"
        )
    if expected_qrels_sha256 is not None and manifest_qrels_hash != expected_qrels_sha256.lower():
        raise ValueError(
            "BINDING_QRELS_MISMATCH: canonical run was not produced for current qrels"
        )
    if expected_queries_sha256 is not None and manifest_queries_hash != expected_queries_sha256.lower():
        raise ValueError(
            "BINDING_QUERIES_MISMATCH: canonical run was not produced for current queries"
        )
    if expected_qrels_sha256 is not None and expected_queries_sha256 is None:
        raise ValueError(
            "BINDING_QUERIES_MISMATCH: current queries digest was not supplied"
        )
    predictions: list[dict[str, Any]] = []
    prediction_keys: list[tuple[str, str, str]] = []
    predictions_path = root / "predictions.jsonl"
    for line_no, line in enumerate(
        predictions_path.read_text(encoding="utf-8").splitlines(), start=1
    ):
        if not line.strip():
            continue
        try:
            prediction = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(
                f"BINDING_PREDICTIONS_INVALID: line {line_no} is not JSON"
            ) from exc
        if (
            not isinstance(prediction, dict)
            or not str(prediction.get("query_id", "")).strip()
            or not str(prediction.get("document_id", "")).strip()
        ):
            raise ValueError(
                f"BINDING_PREDICTIONS_INVALID: line {line_no} lacks query_id or document_id"
            )
        section_path = prediction.get("section_path") or []
        if not isinstance(section_path, list):
            raise ValueError(
                f"BINDING_PREDICTIONS_INVALID: line {line_no} section_path is not a list"
            )
        prediction["section_path"] = section_path
        predictions.append(prediction)
        prediction_keys.append(
            (
                str(prediction["query_id"]),
                str(prediction["document_id"]),
                json.dumps(section_path, ensure_ascii=False, separators=(",", ":")),
            )
        )
    prediction_qids = {key[0] for key in prediction_keys}
    if expected_qids is not None:
        if len(prediction_keys) != len(set(prediction_keys)) or not prediction_qids <= expected_qids:
            raise ValueError(
                "BINDING_PREDICTIONS_QID_MISMATCH: predictions must contain no "
                "foreign qid and no duplicate query/document/section hit; an absent "
                "qid represents an empty result"
            )
    git_sha = str(manifest.get("git_sha", "")).strip()
    try:
        current_git = subprocess.run(
            ["git", "rev-parse", "HEAD"],
            cwd=REPO_ROOT,
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
        ).stdout.strip()
    except (OSError, subprocess.CalledProcessError) as exc:
        raise ValueError(f"BINDING_GIT_UNAVAILABLE: {exc}") from exc
    if git_sha != current_git:
        raise ValueError("BINDING_GIT_MISMATCH: canonical run is not for current HEAD")
    dirty_hash = str(manifest.get("dirty_hash", "")).strip().lower()
    if not re.fullmatch(r"[0-9a-f]{64}", dirty_hash):
        raise ValueError("BINDING_INVALID: dirty_hash is not a SHA-256 digest")
    if dirty_hash != current_dirty_hash():
        raise ValueError(
            "BINDING_DIRTY_MISMATCH: canonical run is not for the current tracked diff"
        )
    bindings = {
        "git_sha": git_sha,
        "dirty_hash": dirty_hash,
        "scorer_name": str(scorer.get("scorer_name", "")),
        "scorer_version": str(scorer.get("scorer_version", "")),
        "index_name": str(manifest.get("physical_index", "")),
        "index_alias": str(manifest.get("index_alias", "")),
        "index_mapping_hash": str(manifest.get("index_mapping_hash", "")).lower(),
        "index_document_count": str(manifest.get("index_document_count", "")),
        "index_corpus_generation": str(manifest.get("corpus_generation", "")),
        "index_model_version": str(manifest.get("model_revision", "")),
        "predictions_sha256": pinned["predictions.jsonl"],
    }
    missing_values = sorted(key for key, value in bindings.items() if not str(value).strip())
    if missing_values:
        raise ValueError(
            "BINDING_INVALID: canonical metadata has empty fields: "
            + ", ".join(missing_values)
        )
    for key in _SHA256_BINDINGS:
        value = bindings[key].lower()
        if len(value) != 64 or any(char not in "0123456789abcdef" for char in value):
            raise ValueError(f"BINDING_INVALID: {key} is not a SHA-256 digest")
    index_alias_target = str(manifest.get("index_alias_target", "")).strip()
    if not index_alias_target:
        raise ValueError("BINDING_INVALID: index_alias_target is empty")
    if index_alias_target != bindings["index_name"]:
        raise ValueError(
            "BINDING_INDEX_ALIAS_MISMATCH: index_alias_target does not match physical_index"
        )
    try:
        index_document_count = int(bindings["index_document_count"])
    except (TypeError, ValueError) as exc:
        raise ValueError("BINDING_INVALID: index_document_count is not an integer") from exc
    if index_document_count <= 0:
        raise ValueError("BINDING_INVALID: index_document_count must be positive")
    try:
        scorer_report = json.loads(
            (root / "scorer" / "report.json").read_text(encoding="utf-8")
        )
        per_query_rows, _ = _read_review_sidecar(root / "scorer" / "per-query.jsonl")
    except (json.JSONDecodeError, UnicodeDecodeError, ValueError) as exc:
        raise ValueError(f"BINDING_SCORER_MISMATCH: invalid scorer output: {exc}") from exc
    signed_per_query = {str(row["query_id"]): {k: v for k, v in row.items() if k != "query_id"} for row in per_query_rows}
    if len(signed_per_query) != len(per_query_rows):
        raise ValueError("BINDING_SCORER_MISMATCH: duplicate per-query scorer rows")
    if expected_qrel_rows is not None:
        recomputed_overall, recomputed_per_query = _official_scorer_results(
            expected_qrel_rows, predictions
        )
        if (
            not isinstance(scorer_report, dict)
            or scorer_report.get("overall") != recomputed_overall
            or signed_per_query != recomputed_per_query
        ):
            raise ValueError(
                "BINDING_SCORER_MISMATCH: signed scorer output does not match "
                "deterministic recomputation from current qrels and predictions"
            )
        bindings["official_metrics"] = recomputed_overall
    return bindings


def require_release_bindings(
    result: GateResult,
    report: Mapping[str, Any],
    *,
    policy: Mapping[str, Any] | None = None,
) -> None:
    """Require full provenance only when a report would claim eligibility."""
    if result.eligible:
        require_bindings(report, policy=policy)


def bootstrap_ci(
    values: Sequence[float],
    *,
    iterations: int = 1000,
    confidence: float = 0.95,
    seed: int = 20260810,
) -> tuple[float, float]:
    """Percentile bootstrap CI over *values*.

    Reported to expose its own width on a small set, never to convert an
    ineligible set into a releasable one.
    """
    if not values:
        raise ValueError(
            "METRIC_REQUESTED_WHILE_INELIGIBLE: bootstrap over an empty sample"
        )
    if iterations < 1:
        raise ValueError("METRIC_REQUESTED_WHILE_INELIGIBLE: iterations must be >= 1")
    rng = random.Random(seed)
    n = len(values)
    means = sorted(
        statistics.fmean(rng.choice(values) for _ in range(n))
        for _ in range(iterations)
    )
    tail = (1.0 - confidence) / 2.0
    lo_idx = min(int(tail * iterations), iterations - 1)
    hi_idx = min(int((1.0 - tail) * iterations), iterations - 1)
    return means[lo_idx], means[hi_idx]


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def _build_report(
    result: GateResult,
    *,
    policy: Mapping[str, Any],
    policy_hash: str,
    qrels_hash: str,
    queries_hash: str,
    qrels_path: str,
    split_manifest_hash: str,
    model_identity_hash: str,
    external_bindings: Mapping[str, Any] | None = None,
) -> dict[str, Any]:
    external = dict(external_bindings or {})
    official_metrics = external.pop("official_metrics", {})
    report = {
        "report_kind": "e5-rag-release-report",
        "policy_id": policy.get("policy_id", ""),
        "policy_version": policy.get("policy_version", ""),
        "policy_sha256": policy_hash,
        "qrels_path": qrels_path,
        "qrels_sha256": qrels_hash,
        "queries_sha256": queries_hash,
        "split_manifest_sha256": split_manifest_hash,
        "model_identity_sha256": model_identity_hash,
        "verdict": "RELEASE_ELIGIBLE" if result.eligible else "NOT_RELEASE_ELIGIBLE",
        "failed_codes": list(result.failed_codes),
        "failed_detail": dict(result.detail),
        "counts": {
            "total_rows": result.total_rows,
            "golden_rows": result.golden_rows,
            "disputed_rows": result.disputed_rows,
            "golden_positive_rows": result.golden_positive_rows,
            "golden_negative_rows": result.golden_negative_rows,
            "pure_negative_rows": result.pure_negative_rows,
            "pure_negative_golden_rows": result.pure_negative_golden_rows,
        },
        "golden_sources": dict(result.golden_sources),
        "missing_sources": list(result.missing_sources),
        "model_identity_status": result.model_identity_status,
        "holdout_status": result.holdout_status,
        "granularity": policy.get("scoring", {}).get("granularity", "document"),
        # Empty by contract while ineligible: no metric may escape a failed gate.
        "metrics": official_metrics if result.eligible else {},
    }
    report.update(external)
    return report


def main(argv: Sequence[str] | None = None) -> int:
    """Evaluate the gates and write a report. Returns the policy exit code."""
    parser = argparse.ArgumentParser(
        prog="release_report",
        description="E5 RAG release report (refuses to score an ineligible set)",
    )
    parser.add_argument("--qrels", default=str(QRELS_PATH))
    parser.add_argument("--queries", default=str(QUERIES_PATH))
    parser.add_argument("--policy", default=str(POLICY_PATH))
    parser.add_argument("--out", default="")
    parser.add_argument("--review-pass-a", default=str(REVIEW_PASS_A_PATH))
    parser.add_argument("--review-pass-b", default=str(REVIEW_PASS_B_PATH))
    parser.add_argument(
        "--review-attestation",
        default=str(REPO_ROOT / "data" / "eval" / "techdocs" / "sol-review-attestation.json"),
    )
    parser.add_argument("--split-manifest", default=str(SPLIT_MANIFEST_PATH))
    parser.add_argument(
        "--bindings",
        default="",
        help="JSON manifest containing canonical Git/scorer/index/predictions bindings",
    )
    args = parser.parse_args(list(argv) if argv is not None else None)

    if args.out:
        stale_out = Path(args.out)
        if stale_out.is_file():
            try:
                stale_out.unlink()
            except OSError as exc:
                print(f"ERROR: REPORT_STALE_OUTPUT: {exc}", file=sys.stderr)
                return EXIT_DATA_FAULT

    try:
        policy = load_policy(args.policy)
        policy_hash = policy_sha256(args.policy)
        if policy_hash != TRUSTED_POLICY_SHA256:
            raise ValueError(
                "POLICY_UNTRUSTED: release policy is not pinned by the code trust root"
            )
        qrels_path = Path(args.qrels)
        qrel_rows = _load_qrels(qrels_path)
        expected_qids = {str(row["query_id"]) for row in qrel_rows}
        queries_path = Path(args.queries)
        query_rows, _ = _read_review_sidecar(queries_path)
        query_qids = [str(row["query_id"]) for row in query_rows]
        if len(query_qids) != len(set(query_qids)) or set(query_qids) != expected_qids:
            raise ValueError(
                "QUERIES_QID_MISMATCH: queries must contain each qrels qid exactly once"
            )
        queries_hash = hashlib.sha256(queries_path.read_bytes()).hexdigest()
        expected_review_statuses = {
            str(row["query_id"]): str(row["review_status"]) for row in qrel_rows
        }
        expected_qrel_rows = {str(row["query_id"]): row for row in qrel_rows}
        qrels_hash = hashlib.sha256(qrels_path.read_bytes()).hexdigest()
        model_identity, model_identity_hash = load_review_identity(
            args.review_pass_a,
            args.review_pass_b,
            expected_qids=expected_qids,
            expected_qrel_rows=expected_qrel_rows,
            expected_review_statuses=expected_review_statuses,
            expected_qrels_sha256=qrels_hash,
            expected_queries_sha256=queries_hash,
            attestation_path=args.review_attestation,
            attestation_public_key_b64=str(
                policy.get("gates", {}).get("model_identity", {}).get(
                    "attestation_public_key_b64", ""
                )
            ),
        )
        split_path = Path(args.split_manifest)
        if not split_path.is_file():
            raise ValueError(f"SPLIT_MANIFEST_MISSING: no split manifest at {split_path}")
        split_bytes = split_path.read_bytes()
        loaded_split = json.loads(split_bytes)
        if not isinstance(loaded_split, dict):
            raise ValueError("SPLIT_MANIFEST_INVALID: split manifest is not an object")
        from orchestrator.eval.split import validate_manifest

        validate_manifest(loaded_split)
        split_manifest_hash = hashlib.sha256(split_bytes).hexdigest()
        result = evaluate_gates(
            qrels_path,
            policy=policy,
            model_identity=model_identity,
            split_manifest=loaded_split,
        )
        external_bindings = (
            load_external_bindings(
                args.bindings,
                attestation_public_key_b64=str(
                    policy.get("scoring", {}).get(
                        "artifact_attestation_public_key_b64", ""
                    )
                ),
                expected_qrels_sha256=qrels_hash,
                expected_queries_sha256=queries_hash,
                expected_qids=expected_qids,
                expected_qrel_rows=expected_qrel_rows,
            )
            if args.bindings
            else {}
        )
    except ValueError as exc:
        # Any named failure code above means nothing was measured.
        print(f"ERROR: {exc}", file=sys.stderr)
        return EXIT_DATA_FAULT
    except OSError as exc:
        print(f"ERROR: QRELS_MISSING: {exc}", file=sys.stderr)
        return EXIT_DATA_FAULT

    report = _build_report(
        result,
        policy=policy,
        policy_hash=policy_hash,
        qrels_hash=qrels_hash,
        queries_hash=queries_hash,
        qrels_path=str(qrels_path),
        split_manifest_hash=split_manifest_hash,
        model_identity_hash=model_identity_hash,
        external_bindings=external_bindings,
    )

    if result.eligible:
        try:
            require_release_bindings(result, report, policy=policy)
            require_metric_emission_allowed(result)
        except ValueError as exc:
            print(f"ERROR: {exc}", file=sys.stderr)
            return EXIT_DATA_FAULT

    if args.out:
        out = Path(args.out)
        out.parent.mkdir(parents=True, exist_ok=True)
        out.write_bytes(
            (json.dumps(report, ensure_ascii=False, indent=2) + "\n").encode("utf-8")
        )

    print(f"verdict      : {report['verdict']}")
    print(f"golden rows  : {result.golden_rows} of {result.total_rows}")
    print(f"disputed     : {result.disputed_rows}")
    if result.failed_codes:
        print("failed gates :")
        for code in result.failed_codes:
            print(f"  - {code}: {result.detail.get(code, '')}")

    if not result.eligible:
        return EXIT_NOT_ELIGIBLE

    return EXIT_ELIGIBLE


if __name__ == "__main__":
    raise SystemExit(main())
