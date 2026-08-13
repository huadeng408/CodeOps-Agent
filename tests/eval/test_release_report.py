"""E5 — the release report must refuse to score an ineligible golden set.

Design map line 1108 asks E5 to score "only the locked golden set", report
pure negatives separately, bind every hash, and emit per-query detail with a
bootstrap CI.  Measured against the corpus as it actually exists, the honest
deliverable is a scorer that **refuses**:

* 23 of 180 rows passed arbitration (12.8%); 157 are DISPUTED;
* all 23 carry relevance 1.0, so precision/FP-rate are undefined;
* docker and kubernetes contribute 0 of the 23;
* all 17 pure negatives are DISPUTED, so E5's own pure-negative deliverable
  has no eligible data;
* 178 of 180 section_path labels do not resolve against the index.

So these tests pin refusal semantics, mirroring E3's ``holdout BLOCKED
(size 0)`` precedent: an empty or inadequate measurement raises rather than
emitting a ``0.0`` that would flow into a release table indistinguishable
from a real measurement.

Exit-code contract (policy ``exit_codes``): 0 eligible, 1 RESERVED_UNUSED,
2 data fault (nothing measured), 3 NOT_RELEASE_ELIGIBLE (measured, refused).
1 is deliberately left unused so a crash can never be read as a verdict —
the contamination scanner's exit 1 already means a business verdict, and an
escaping exception there would masquerade as one.
"""

from __future__ import annotations

import hashlib
import json
import subprocess
import base64
from pathlib import Path
from typing import Any, Mapping

import pytest
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from orchestrator.eval import release_report as rr


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _write_policy(tmp_path: Path, **overrides) -> Path:
    """A minimal but structurally valid policy, with its hash sidecar."""
    policy = {
        "policy_id": "e5-release-policy",
        "policy_version": "v1",
        "gates": {
            "min_golden_rows": 60,
            "min_golden_fraction": 0.33,
            "require_negative_in_golden": True,
            "min_negative_golden_rows": 10,
            "require_all_declared_sources": True,
            "declared_sources": ["docker", "git", "go", "kubernetes", "postgresql", "python"],
            "max_disputed_fraction": 0.2,
            "require_model_identity": True,
            "require_hidden_holdout": True,
            "model_identity": {
                "allowed_endpoint_hosts": ["api.openai.com"],
                "required_requested_model": "gpt-5.6-sol",
                "allowed_reported_model_prefixes": ["gpt-5.6-sol"],
            },
        },
        "scoring": {
            "granularity": "document",
            "eligible_rows_rule": "review_status == AI_REVIEWED only",
            "required_bindings": ["git_sha", "qrels_sha256", "policy_sha256"],
            "bootstrap": {"iterations": 100, "confidence": 0.95, "seed": 1},
        },
        "exit_codes": {"0": "eligible", "1": "RESERVED_UNUSED", "2": "data fault", "3": "refused"},
    }
    policy.update(overrides)
    path = tmp_path / "release-policy.v1.json"
    raw = json.dumps(policy, ensure_ascii=False, indent=2).encode("utf-8")
    path.write_bytes(raw)
    sidecar = tmp_path / "release-policy.v1.json.sha256"
    sidecar.write_text(
        hashlib.sha256(raw).hexdigest() + "  " + path.name + "\n", encoding="utf-8"
    )
    return path


def _write_qrels(tmp_path: Path, rows: list[dict]) -> Path:
    path = tmp_path / "qrels.jsonl"
    path.write_text(
        "".join(json.dumps(r, ensure_ascii=False) + "\n" for r in rows),
        encoding="utf-8",
    )
    return path


def _write_review_pass(
    tmp_path: Path, pass_id: str, rows: list[dict], *, duplicate_ids: bool = False
) -> Path:
    path = tmp_path / f"pass-{pass_id.lower()}.jsonl"
    output = []
    for index, row in enumerate(rows):
        response_id = "resp-shared" if duplicate_ids else f"resp-{pass_id}-{index}"
        output.append(
            {
                "query_id": row["query_id"],
                "document_id": row["document_id"],
                "section_path": row["section_path"],
                "relevance": row["relevance"],
                "source_id": row["source_id"],
                "language": row["language"],
                "query_type": row["query_type"],
                "review_pass": pass_id,
                "reviewer_model": "gpt-5.6-sol",
                "reviewer_reported_model": "gpt-5.6-sol",
                "reviewer_system_fingerprint": "fp_sol_revision_a",
                "reviewer_response_id": response_id,
                "reviewer_identity_status": "MODEL_IDENTITY_VERIFIED",
                "reviewer_endpoint_host": "api.openai.com",
                "review_confidence": 0.95,
                "verdicts": {
                    "answerable": True,
                    "language_correct": True,
                    "query_type_correct": True,
                    "relevance_correct": True,
                    "section_correct": True,
                    "evidence_sufficient": True,
                    "contamination_risk": "none",
                    "confidence": 0.95,
                },
                "review_status": row.get("review_status", "AI_REVIEWED"),
            }
        )
    path.write_text(
        "".join(json.dumps(item) + "\n" for item in output), encoding="utf-8"
    )
    return path


def _row(qid: str, *, status="AI_REVIEWED", relevance=1.0, source="git", doc=None):
    return {
        "query_id": qid,
        "document_id": doc or f"{source}@abc123:docs/{qid}.md",
        "section_path": ["Some Heading"],
        "relevance": relevance,
        "source_id": source,
        "language": "en",
        "query_type": "concept",
        "review_status": status,
    }


def _eligible_qrels(tmp_path: Path) -> Path:
    """A golden set that passes every gate: 6 sources, negatives, ≥60 rows."""
    sources = ["docker", "git", "go", "kubernetes", "postgresql", "python"]
    rows = []
    for i in range(72):
        src = sources[i % 6]
        # every 6th row is a labelled negative -> 12 negatives
        rel = 0.0 if i % 6 == 5 else 1.0
        rows.append(_row(f"q{i:03d}", relevance=rel, source=src))
    return _write_qrels(tmp_path, rows)


def _verified_model_identity() -> dict:
    return {
        "endpoint_host": "api.openai.com",
        "attestation_status": "MODEL_IDENTITY_ATTESTED",
        "passes_independent": True,
        "passes": [
            {
                "requested_model": "gpt-5.6-sol",
                "reported_model": "gpt-5.6-sol",
                "response_id": "resp-pass-a",
                "immutable_revision": "fp_sol_revision_a",
            },
            {
                "requested_model": "gpt-5.6-sol",
                "reported_model": "gpt-5.6-sol",
                "response_id": "resp-pass-b",
                "immutable_revision": "fp_sol_revision_a",
            },
        ],
    }


def _verified_split_manifest() -> dict:
    return {
        "policy_id": "e3-split-policy",
        "policy_version": "v2",
        "policy_sha256": "a" * 64,
        "holdout_size": 24,
        "holdout_status": "VERIFIED",
        "holdout_qids_sha256": "b" * 64,
    }


def _write_binding_artifacts(
    tmp_path: Path, *, manifest_overrides: Mapping[str, Any] | None = None,
    qids: list[str] | None = None, qrels_hash: str | None = None
) -> tuple[Path, str]:
    root = tmp_path / "canonical-run"
    (root / "scorer").mkdir(parents=True)
    manifest = {
        "git_sha": subprocess.run(
            ["git", "rev-parse", "HEAD"],
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
        ).stdout.strip(),
        "dirty_hash": "0" * 64,
        "physical_index": "knowledge_base_v2_bge_m3",
        "corpus_generation": "techdocs-v2",
        "model_revision": "BAAI/bge-m3@revision",
    }
    if qrels_hash is not None:
        manifest["qrels_hash"] = qrels_hash
    manifest.update(manifest_overrides or {})
    prediction_qids = qids or ["q1"]
    predictions = "".join(
        json.dumps({"query_id": qid, "document_id": f"doc-{qid}"}) + "\n"
        for qid in prediction_qids
    )
    files = {
        "run-manifest.json": json.dumps(manifest) + "\n",
        "predictions.jsonl": predictions,
        "scorer/metadata.json": json.dumps(
            {"scorer_name": "techdocs-document-scorer", "scorer_version": "1.0.0"}
        ) + "\n",
    }
    for rel, content in files.items():
        (root / rel).write_text(content, encoding="utf-8")
    checksums = []
    for rel in sorted(files):
        checksums.append(f"{hashlib.sha256((root / rel).read_bytes()).hexdigest()}  {rel}")
    (root / "checksums.sha256").write_text("\n".join(checksums) + "\n", encoding="utf-8")
    private_key = Ed25519PrivateKey.generate()
    public_key = base64.b64encode(
        private_key.public_key().public_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PublicFormat.Raw,
        )
    ).decode("ascii")
    attestation = root / "artifact-attestation.json"
    attestation.write_text(
        json.dumps(
            {
                "checksums_sha256": hashlib.sha256(
                    (root / "checksums.sha256").read_bytes()
                ).hexdigest(),
                "signature_b64": base64.b64encode(
                    private_key.sign((root / "checksums.sha256").read_bytes())
                ).decode("ascii"),
            }
        ),
        encoding="utf-8",
    )
    binding = tmp_path / "bindings.json"
    binding.write_text(
        json.dumps({"artifact_root": str(root), "attestation_path": str(attestation)}),
        encoding="utf-8",
    )
    return binding, public_key


def _sign_review_attestation(
    tmp_path: Path,
    pass_a: Path,
    pass_b: Path,
    qids: set[str],
    *,
    qrels_sha256: str,
) -> tuple[Path, str]:
    identity, digest = rr.load_review_identity(pass_a, pass_b, expected_qids=qids)
    private_key = Ed25519PrivateKey.generate()
    public_key = base64.b64encode(
        private_key.public_key().public_bytes(
            encoding=serialization.Encoding.Raw,
            format=serialization.PublicFormat.Raw,
        )
    ).decode("ascii")
    message = json.dumps(
        {
            "sidecars_sha256": digest,
            "qrels_sha256": qrels_sha256,
            "identity": identity,
        },
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    path = tmp_path / "review-attestation.json"
    path.write_text(
        json.dumps(
            {
                "sidecars_sha256": digest,
                "qrels_sha256": qrels_sha256,
                "signature_b64": base64.b64encode(private_key.sign(message)).decode("ascii"),
            }
        ),
        encoding="utf-8",
    )
    return path, public_key


# ---------------------------------------------------------------------------
# R1-R3: policy binding
# ---------------------------------------------------------------------------


def test_r1_missing_policy_raises_policy_missing(tmp_path: Path) -> None:
    with pytest.raises(ValueError, match="POLICY_MISSING"):
        rr.load_policy(tmp_path / "nope.json")


def test_r2_policy_hash_mismatch_raises(tmp_path: Path) -> None:
    path = _write_policy(tmp_path)
    path.write_bytes(path.read_bytes() + b"\n")  # drift from the sidecar
    with pytest.raises(ValueError, match="POLICY_HASH_MISMATCH"):
        rr.load_policy(path)


def test_r3_unknown_policy_version_raises(tmp_path: Path) -> None:
    path = _write_policy(tmp_path, policy_version="v99")
    raw = path.read_bytes()
    (tmp_path / "release-policy.v1.json.sha256").write_text(
        hashlib.sha256(raw).hexdigest() + "  " + path.name + "\n", encoding="utf-8"
    )
    with pytest.raises(ValueError, match="POLICY_VERSION_UNKNOWN"):
        rr.load_policy(path)


# ---------------------------------------------------------------------------
# R4-R5: qrels contract
# ---------------------------------------------------------------------------


def test_r4_missing_qrels_raises(tmp_path: Path) -> None:
    policy = rr.load_policy(_write_policy(tmp_path))
    with pytest.raises(ValueError, match="QRELS_MISSING"):
        rr.evaluate_gates(tmp_path / "absent.jsonl", policy=policy)


def test_r5_qrel_row_missing_required_field_raises(tmp_path: Path) -> None:
    policy = rr.load_policy(_write_policy(tmp_path))
    bad = _row("q1")
    del bad["review_status"]
    qrels = _write_qrels(tmp_path, [bad])
    with pytest.raises(ValueError, match="QRELS_SCHEMA_INVALID"):
        rr.evaluate_gates(qrels, policy=policy)


# ---------------------------------------------------------------------------
# R6-R9: the gates, evaluated on the real shape of the data
# ---------------------------------------------------------------------------


def test_r6_real_corpus_shape_is_not_release_eligible(tmp_path: Path) -> None:
    """23/180 all-positive, 4 of 6 sources: every blocking reason fires."""
    rows = []
    for i in range(23):
        rows.append(_row(f"ok{i:03d}", status="AI_REVIEWED", relevance=1.0,
                         source=["git", "go", "postgresql", "python"][i % 4]))
    for i in range(140):
        rows.append(_row(f"d{i:03d}", status="DISPUTED", relevance=1.0))
    for i in range(17):
        rows.append(_row(f"n{i:03d}", status="DISPUTED", relevance=0.0))
    qrels = _write_qrels(tmp_path, rows)
    policy = rr.load_policy(_write_policy(tmp_path))

    result = rr.evaluate_gates(qrels, policy=policy)

    assert result.eligible is False
    codes = set(result.failed_codes)
    assert "GOLDEN_SET_TOO_SMALL" in codes
    assert "GOLDEN_SET_ALL_POSITIVE" in codes
    assert "SOURCE_COVERAGE_INCOMPLETE" in codes
    assert "PURE_NEGATIVE_SET_UNREVIEWED" in codes
    assert "TOO_MANY_DISPUTED" in codes
    # the numbers must be reported, not just the verdict
    assert result.golden_rows == 23
    assert result.total_rows == 180
    assert result.disputed_rows == 157


def test_r7_missing_source_is_named_not_just_counted(tmp_path: Path) -> None:
    rows = [
        _row(f"ok{i:03d}", source=["git", "go", "postgresql", "python"][i % 4])
        for i in range(23)
    ]
    qrels = _write_qrels(tmp_path, rows)
    policy = rr.load_policy(_write_policy(tmp_path))
    result = rr.evaluate_gates(qrels, policy=policy)
    assert set(result.missing_sources) == {"docker", "kubernetes"}


def test_r8_eligible_shape_passes_every_gate(tmp_path: Path) -> None:
    """The gate logic must be able to say yes, or it proves nothing."""
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))
    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity=_verified_model_identity(),
        split_manifest=_verified_split_manifest(),
    )
    assert result.eligible is True, result.failed_codes
    assert result.failed_codes == ()


def test_r8a_requested_model_name_is_not_identity_evidence(tmp_path: Path) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))

    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity={
            "provider": "openai",
            "passes": [
                {"requested_model": "gpt-5.6-sol"},
                {"requested_model": "gpt-5.6-sol"},
            ],
        },
    )

    assert result.eligible is False
    assert "MODEL_IDENTITY_UNVERIFIED" in result.failed_codes


def test_r8b_verified_boolean_cannot_replace_response_evidence(tmp_path: Path) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))

    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity={"identity_verified": True, "model": "gpt-5.6-sol"},
    )

    assert result.eligible is False
    assert "MODEL_IDENTITY_UNVERIFIED" in result.failed_codes


def test_r8c_review_passes_must_have_distinct_response_ids(tmp_path: Path) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))
    identity = _verified_model_identity()
    identity["passes"][1]["response_id"] = identity["passes"][0]["response_id"]

    result = rr.evaluate_gates(qrels, policy=policy, model_identity=identity)

    assert result.eligible is False
    assert "MODEL_REVIEW_PASSES_NOT_INDEPENDENT" in result.failed_codes


def test_r8c2_review_independence_must_be_explicitly_true(tmp_path: Path) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))
    identity = _verified_model_identity()
    identity.pop("passes_independent", None)

    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity=identity,
        split_manifest=_verified_split_manifest(),
    )

    assert "MODEL_REVIEW_PASSES_NOT_INDEPENDENT" in result.failed_codes


def test_r8d_empty_holdout_blocks_otherwise_eligible_release(tmp_path: Path) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))
    split_manifest = _verified_split_manifest()
    split_manifest.update(
        {
            "policy_version": "v1",
            "holdout_size": 0,
            "holdout_status": "BLOCKED",
            "holdout_qids_sha256": hashlib.sha256(b"").hexdigest(),
        }
    )

    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity=_verified_model_identity(),
        split_manifest=split_manifest,
    )

    assert result.eligible is False
    assert "HOLDOUT_NOT_MEASURABLE" in result.failed_codes


def test_r8e_split_verified_boolean_without_membership_hash_is_refused(
    tmp_path: Path,
) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))
    split_manifest = _verified_split_manifest()
    split_manifest["holdout_qids_sha256"] = ""

    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity=_verified_model_identity(),
        split_manifest=split_manifest,
    )

    assert result.eligible is False
    assert "HOLDOUT_NOT_MEASURABLE" in result.failed_codes


def test_r8f_deepseek_endpoint_cannot_verify_openai_golden_review(
    tmp_path: Path,
) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))
    identity = _verified_model_identity()
    identity["endpoint_host"] = "api.deepseek.com"

    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity=identity,
        split_manifest=_verified_split_manifest(),
    )

    assert result.eligible is False
    assert "MODEL_PROVIDER_MISMATCH" in result.failed_codes


def test_r8g_requested_model_must_match_policy(tmp_path: Path) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))
    identity = _verified_model_identity()
    for pass_identity in identity["passes"]:
        pass_identity["requested_model"] = "gpt-5.6-terra"
        pass_identity["reported_model"] = "gpt-5.6-terra"

    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity=identity,
        split_manifest=_verified_split_manifest(),
    )

    assert result.eligible is False
    assert "MODEL_PROVIDER_MISMATCH" in result.failed_codes


def test_r8h_versioned_sol_reported_model_is_allowed_by_policy(
    tmp_path: Path,
) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))
    identity = _verified_model_identity()
    for pass_identity in identity["passes"]:
        pass_identity["reported_model"] = "gpt-5.6-sol-2026-07"

    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity=identity,
        split_manifest=_verified_split_manifest(),
    )

    assert result.eligible is True, result.failed_codes


def test_r9_all_positive_golden_set_is_blocked_even_when_large(tmp_path: Path) -> None:
    """Size alone must not buy eligibility when no negative exists."""
    sources = ["docker", "git", "go", "kubernetes", "postgresql", "python"]
    rows = [_row(f"q{i:03d}", relevance=1.0, source=sources[i % 6]) for i in range(72)]
    qrels = _write_qrels(tmp_path, rows)
    policy = rr.load_policy(_write_policy(tmp_path))
    result = rr.evaluate_gates(qrels, policy=policy)
    assert result.eligible is False
    assert "GOLDEN_SET_ALL_POSITIVE" in result.failed_codes


# ---------------------------------------------------------------------------
# R10-R12: refusal semantics — no metric may escape an ineligible set
# ---------------------------------------------------------------------------


def test_r10_metric_request_while_ineligible_raises_not_returns_zero(
    tmp_path: Path,
) -> None:
    """The E3 precedent: refuse, never emit a 0.0 that reads as measured."""
    rows = [_row(f"ok{i:03d}") for i in range(23)]
    qrels = _write_qrels(tmp_path, rows)
    policy = rr.load_policy(_write_policy(tmp_path))
    result = rr.evaluate_gates(qrels, policy=policy)

    with pytest.raises(ValueError, match="METRIC_REQUESTED_WHILE_INELIGIBLE"):
        rr.require_metric_emission_allowed(result)


def test_r11_section_metric_is_forbidden_at_v1(tmp_path: Path) -> None:
    """178/180 section_path labels do not resolve; section metrics must raise."""
    policy = rr.load_policy(_write_policy(tmp_path))
    with pytest.raises(ValueError, match="SECTION_METRIC_REQUESTED"):
        rr.require_granularity_allowed("section", policy=policy)
    # document granularity is the only admissible one at v1
    rr.require_granularity_allowed("document", policy=policy)


def test_r12_disputed_row_reaching_scorer_raises(tmp_path: Path) -> None:
    policy = rr.load_policy(_write_policy(tmp_path))
    with pytest.raises(ValueError, match="DISPUTED_ROW_SCORED"):
        rr.select_scoreable_rows(
            [_row("q1", status="DISPUTED")], policy=policy, strict=True
        )


def test_r13_pure_negative_never_enters_macro_average(tmp_path: Path) -> None:
    """A pure negative scores 0.0 by construction and would deflate the mean."""
    policy = rr.load_policy(_write_policy(tmp_path))
    with pytest.raises(ValueError, match="PURE_NEGATIVE_IN_MACRO_AVERAGE"):
        rr.macro_average({"q1": 1.0}, pure_negative_qids={"q1"}, policy=policy)


def test_r14_report_missing_binding_raises(tmp_path: Path) -> None:
    policy = rr.load_policy(_write_policy(tmp_path))
    with pytest.raises(ValueError, match="BINDING_MISSING"):
        rr.require_bindings({"git_sha": "abc", "qrels_sha256": ""}, policy=policy)
    rr.require_bindings(
        {"git_sha": "abc", "qrels_sha256": "d", "policy_sha256": "e"}, policy=policy
    )


# ---------------------------------------------------------------------------
# R15-R17: the CLI exit-code contract
# ---------------------------------------------------------------------------


def test_r15_cli_returns_3_for_not_release_eligible(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    rows = [_row(f"ok{i:03d}") for i in range(23)]
    qrels = _write_qrels(tmp_path, rows)
    pass_a = _write_review_pass(tmp_path, "A", rows)
    pass_b = _write_review_pass(tmp_path, "B", rows)
    policy_path = _write_policy(tmp_path)
    out = tmp_path / "report.json"

    monkeypatch.setattr(
        rr, "TRUSTED_POLICY_SHA256", hashlib.sha256(policy_path.read_bytes()).hexdigest()
    )
    rc = rr.main([
        "--qrels", str(qrels), "--policy", str(policy_path),
        "--review-pass-a", str(pass_a), "--review-pass-b", str(pass_b),
        "--out", str(out),
    ])
    assert rc == 3, "gates evaluated and failed is an honest verdict: exit 3"

    report = json.loads(out.read_text(encoding="utf-8"))
    assert report["verdict"] == "NOT_RELEASE_ELIGIBLE"
    assert report["metrics"] == {}, "no metric may be emitted when refusing"
    assert "GOLDEN_SET_TOO_SMALL" in report["failed_codes"]


def test_r16_cli_returns_2_for_data_fault(tmp_path: Path) -> None:
    policy_path = _write_policy(tmp_path)
    rc = rr.main([
        "--qrels", str(tmp_path / "absent.jsonl"),
        "--policy", str(policy_path),
        "--out", str(tmp_path / "r.json"),
    ])
    assert rc == 2, "a data fault means nothing was measured: exit 2, never 3"


def test_r18_bootstrap_is_deterministic_and_brackets_the_mean() -> None:
    """Same seed, same interval; and the CI must contain the sample mean."""
    values = [1.0, 0.0, 1.0, 1.0, 0.0, 1.0, 1.0, 0.0]
    a = rr.bootstrap_ci(values, iterations=200, seed=7)
    b = rr.bootstrap_ci(values, iterations=200, seed=7)
    assert a == b, "a seeded bootstrap must be reproducible"
    lo, hi = a
    mean = sum(values) / len(values)
    assert lo <= mean <= hi
    assert lo < hi, "a degenerate interval would hide the width it exists to show"


def test_r19_bootstrap_on_empty_sample_raises() -> None:
    with pytest.raises(ValueError, match="METRIC_REQUESTED_WHILE_INELIGIBLE"):
        rr.bootstrap_ci([])


def test_r20_real_policy_and_qrels_on_disk_fail_closed_on_review_drift(
    tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    """The shipped qrels and legacy review sidecars currently disagree."""
    out = tmp_path / "real-report.json"
    rc = rr.main(["--out", str(out)])
    captured = capsys.readouterr()
    assert rc == rr.EXIT_DATA_FAULT
    assert "REVIEW_QREL_MISMATCH" in captured.err
    assert not out.exists()


def test_r17_cli_never_returns_1(tmp_path: Path) -> None:
    """1 is RESERVED_UNUSED so a crash can never read as a verdict."""
    policy_path = _write_policy(tmp_path)
    for qrels_arg in (str(tmp_path / "absent.jsonl"), str(_eligible_qrels(tmp_path))):
        rc = rr.main([
            "--qrels", qrels_arg, "--policy", str(policy_path),
            "--out", str(tmp_path / "r.json"),
        ])
        assert rc != 1, f"exit 1 is reserved; got it for {qrels_arg}"


def test_r21_cli_rejects_forged_nonempty_holdout_manifest(tmp_path: Path) -> None:
    source = rr.SPLIT_MANIFEST_PATH
    forged = json.loads(source.read_text(encoding="utf-8"))
    forged.update(
        {
            "holdout_size": 24,
            "holdout_status": "VERIFIED",
            "holdout_qids_sha256": "b" * 64,
        }
    )
    forged_path = tmp_path / "forged-split-manifest.json"
    forged_path.write_text(json.dumps(forged), encoding="utf-8")

    rc = rr.main(
        [
            "--split-manifest",
            str(forged_path),
            "--out",
            str(tmp_path / "report.json"),
        ]
    )

    assert rc == 2, "a forged split is a contract fault, not an honest release verdict"


def test_r22_review_sidecars_build_identity_from_response_fields(tmp_path: Path) -> None:
    rows = [_row("q1"), _row("q2")]
    pass_a = _write_review_pass(tmp_path, "A", rows)
    pass_b = _write_review_pass(tmp_path, "B", rows)

    identity, digest = rr.load_review_identity(pass_a, pass_b, expected_qids={"q1", "q2"})

    assert len(digest) == 64
    assert identity["endpoint_host"] == "api.openai.com"
    assert identity["passes"][0]["response_id"] != identity["passes"][1]["response_id"]
    assert rr.validate_model_identity(identity)[0] == "MODEL_IDENTITY_UNVERIFIED"


def test_r23_review_sidecars_without_response_fields_stay_unverified(
    tmp_path: Path,
) -> None:
    rows = [_row("q1")]
    pass_a = tmp_path / "a.jsonl"
    pass_b = tmp_path / "b.jsonl"
    body = json.dumps(
        {"query_id": "q1", "review_pass": "A", "reviewer_model": "gpt-5.6-sol"}
    )
    pass_a.write_text(body + "\n", encoding="utf-8")
    pass_b.write_text(body.replace('"A"', '"B"') + "\n", encoding="utf-8")

    identity, digest = rr.load_review_identity(pass_a, pass_b, expected_qids={"q1"})

    assert len(digest) == 64
    assert rr.validate_model_identity(identity)[0] == "MODEL_IDENTITY_UNVERIFIED"


def test_r24_review_sidecar_qids_must_match_qrels(tmp_path: Path) -> None:
    rows = [_row("q1")]
    pass_a = _write_review_pass(tmp_path, "A", rows)
    pass_b = _write_review_pass(tmp_path, "B", rows)

    with pytest.raises(ValueError, match="MODEL_IDENTITY_QID_MISMATCH"):
        rr.load_review_identity(pass_a, pass_b, expected_qids={"q1", "q2"})


def test_r24a_duplicate_qids_are_a_data_fault(tmp_path: Path) -> None:
    qrels = _write_qrels(tmp_path, [_row("q1"), _row("q1")])

    with pytest.raises(ValueError, match="QRELS_DUPLICATE_QID"):
        rr.evaluate_gates(qrels, policy=rr.load_policy(_write_policy(tmp_path)))


def test_r24b_qrels_ai_reviewed_must_match_rederived_sidecar_arbitration(
    tmp_path: Path,
) -> None:
    rows = [_row("q1", status="AI_REVIEWED")]
    pass_a = _write_review_pass(tmp_path, "A", rows)
    pass_b = _write_review_pass(tmp_path, "B", rows)
    sidecar_row = json.loads(pass_b.read_text(encoding="utf-8"))
    sidecar_row["verdicts"]["section_correct"] = False
    pass_b.write_text(json.dumps(sidecar_row) + "\n", encoding="utf-8")

    with pytest.raises(ValueError, match="REVIEW_STATUS_MISMATCH"):
        rr.load_review_identity(
            pass_a,
            pass_b,
            expected_qids={"q1"},
            expected_review_statuses={"q1": "AI_REVIEWED"},
        )


def test_r24c_solaris_model_does_not_match_sol_version_prefix(tmp_path: Path) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))
    identity = _verified_model_identity()
    for item in identity["passes"]:
        item["reported_model"] = "gpt-5.6-solaris-forged"

    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity=identity,
        split_manifest=_verified_split_manifest(),
    )

    assert "MODEL_PROVIDER_MISMATCH" in result.failed_codes


def test_r24c2_arbitrary_sol_suffix_is_not_a_valid_model_version(tmp_path: Path) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))
    identity = _verified_model_identity()
    for item in identity["passes"]:
        item["reported_model"] = "gpt-5.6-sol-forged"

    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity=identity,
        split_manifest=_verified_split_manifest(),
    )

    assert "MODEL_PROVIDER_MISMATCH" in result.failed_codes


def test_r24d_cli_rejects_caller_controlled_policy(tmp_path: Path) -> None:
    policy = _write_policy(tmp_path, gates={}, scoring={"required_bindings": []})

    rc = rr.main(["--policy", str(policy)])

    assert rc == rr.EXIT_DATA_FAULT


def test_r24e_valid_signed_review_attestation_verifies_identity(tmp_path: Path) -> None:
    rows = [_row("q1")]
    qrels = _write_qrels(tmp_path, rows)
    qrels_sha256 = hashlib.sha256(qrels.read_bytes()).hexdigest()
    pass_a = _write_review_pass(tmp_path, "A", rows)
    pass_b = _write_review_pass(tmp_path, "B", rows)
    attestation, public_key = _sign_review_attestation(
        tmp_path, pass_a, pass_b, {"q1"}, qrels_sha256=qrels_sha256
    )

    identity, _ = rr.load_review_identity(
        pass_a,
        pass_b,
        expected_qids={"q1"},
        expected_qrels_sha256=qrels_sha256,
        attestation_path=attestation,
        attestation_public_key_b64=public_key,
    )

    assert rr.validate_model_identity(identity)[0] == "VERIFIED"


def test_r24f_signed_review_attestation_breaks_after_sidecar_tamper(
    tmp_path: Path,
) -> None:
    rows = [_row("q1")]
    qrels = _write_qrels(tmp_path, rows)
    qrels_sha256 = hashlib.sha256(qrels.read_bytes()).hexdigest()
    pass_a = _write_review_pass(tmp_path, "A", rows)
    pass_b = _write_review_pass(tmp_path, "B", rows)
    attestation, public_key = _sign_review_attestation(
        tmp_path, pass_a, pass_b, {"q1"}, qrels_sha256=qrels_sha256
    )
    pass_a.write_bytes(pass_a.read_bytes() + b"\n")

    with pytest.raises(ValueError, match="MODEL_ATTESTATION_INVALID"):
        rr.load_review_identity(
            pass_a,
            pass_b,
            expected_qids={"q1"},
            expected_qrels_sha256=qrels_sha256,
            attestation_path=attestation,
            attestation_public_key_b64=public_key,
        )


def test_r24g_signed_review_attestation_cannot_replay_against_other_qrels(
    tmp_path: Path,
) -> None:
    rows = [_row("q1")]
    original_qrels = _write_qrels(tmp_path, rows)
    original_hash = hashlib.sha256(original_qrels.read_bytes()).hexdigest()
    pass_a = _write_review_pass(tmp_path, "A", rows)
    pass_b = _write_review_pass(tmp_path, "B", rows)
    attestation, public_key = _sign_review_attestation(
        tmp_path, pass_a, pass_b, {"q1"}, qrels_sha256=original_hash
    )
    replacement_qrels = _write_qrels(
        tmp_path,
        [{**rows[0], "document_id": "git@other:docs/replayed.md"}],
    )
    replacement_hash = hashlib.sha256(replacement_qrels.read_bytes()).hexdigest()

    with pytest.raises(ValueError, match="MODEL_ATTESTATION_INVALID"):
        rr.load_review_identity(
            pass_a,
            pass_b,
            expected_qids={"q1"},
            expected_qrels_sha256=replacement_hash,
            attestation_path=attestation,
            attestation_public_key_b64=public_key,
        )


@pytest.mark.parametrize("status", [None, "", "UNKNOWN", "HUMAN_REVIEWED"])
def test_r24h_review_sidecar_requires_exact_ai_reviewed_status(
    tmp_path: Path, status: str | None
) -> None:
    rows = [_row("q1", status="AI_REVIEWED")]
    pass_a = _write_review_pass(tmp_path, "A", rows)
    pass_b = _write_review_pass(tmp_path, "B", rows)
    pass_a_row = json.loads(pass_a.read_text(encoding="utf-8"))
    if status is None:
        pass_a_row.pop("review_status")
    else:
        pass_a_row["review_status"] = status
    pass_a.write_text(json.dumps(pass_a_row) + "\n", encoding="utf-8")

    with pytest.raises(ValueError, match="REVIEW_STATUS_MISMATCH"):
        rr.load_review_identity(
            pass_a,
            pass_b,
            expected_qids={"q1"},
            expected_review_statuses={"q1": "AI_REVIEWED"},
        )


def test_r25_release_eligible_report_still_requires_every_provenance_binding(
    tmp_path: Path,
) -> None:
    qrels = _eligible_qrels(tmp_path)
    policy = rr.load_policy(_write_policy(tmp_path))
    result = rr.evaluate_gates(
        qrels,
        policy=policy,
        model_identity=_verified_model_identity(),
        split_manifest=_verified_split_manifest(),
    )
    assert result.eligible is True

    report = rr._build_report(
        result,
        policy=policy,
        policy_hash="a" * 64,
        qrels_hash="b" * 64,
        qrels_path=str(qrels),
        split_manifest_hash="c" * 64,
        model_identity_hash="d" * 64,
    )

    with pytest.raises(ValueError, match="BINDING_MISSING"):
        rr.require_release_bindings(result, report, policy=policy)


def test_r26_cli_accepts_external_canonical_bindings_on_eligible_path(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A fully eligible fixture must have a reachable CLI success path.

    Hashes derived by the release reporter remain reporter-owned.  The external
    manifest supplies only provenance that lives in the canonical retrieval
    run (Git, scorer, index and predictions).
    """
    qrels = _eligible_qrels(tmp_path)
    rows = [json.loads(line) for line in qrels.read_text(encoding="utf-8").splitlines()]
    policy_path = _write_policy(
        tmp_path,
        gates={"require_model_identity": False, "require_hidden_holdout": True},
        scoring={
            "granularity": "document",
            "eligible_rows_rule": "review_status == AI_REVIEWED only",
            "required_bindings": [
                "git_sha",
                "dirty_hash",
                "qrels_sha256",
                "policy_sha256",
                "split_manifest_sha256",
                "model_identity_sha256",
                "scorer_name",
                "scorer_version",
                "index_name",
                "index_corpus_generation",
                "index_model_version",
                "predictions_sha256",
            ],
            "bootstrap": {"iterations": 100, "confidence": 0.95, "seed": 1},
        },
    )
    pass_a = _write_review_pass(tmp_path, "A", rows)
    pass_b = _write_review_pass(tmp_path, "B", rows)
    split = tmp_path / "split.json"
    split.write_text(json.dumps(_verified_split_manifest()), encoding="utf-8")
    bindings, artifact_public_key = _write_binding_artifacts(
        tmp_path,
        qids=[row["query_id"] for row in rows],
        qrels_hash=hashlib.sha256(qrels.read_bytes()).hexdigest(),
    )
    policy_payload = json.loads(policy_path.read_text(encoding="utf-8"))
    policy_payload["scoring"]["artifact_attestation_public_key_b64"] = artifact_public_key
    policy_path.write_text(json.dumps(policy_payload), encoding="utf-8")
    policy_path.with_name(policy_path.name + ".sha256").write_text(
        hashlib.sha256(policy_path.read_bytes()).hexdigest() + "  " + policy_path.name + "\n",
        encoding="utf-8",
    )
    out = tmp_path / "report.json"

    # This fixture uses a policy-v2-shaped holdout object; split.py's v1
    # membership lock is independently covered in test_split_dev_lock.py.
    import orchestrator.eval.split as split_module

    monkeypatch.setattr(split_module, "validate_manifest", lambda manifest: None)
    monkeypatch.setattr(
        rr, "TRUSTED_POLICY_SHA256", hashlib.sha256(policy_path.read_bytes()).hexdigest()
    )

    rc = rr.main(
        [
            "--qrels", str(qrels),
            "--policy", str(policy_path),
            "--review-pass-a", str(pass_a),
            "--review-pass-b", str(pass_b),
            "--split-manifest", str(split),
            "--bindings", str(bindings),
            "--out", str(out),
        ],
    )

    assert rc == rr.EXIT_ELIGIBLE
    report = json.loads(out.read_text(encoding="utf-8"))
    assert report["verdict"] == "RELEASE_ELIGIBLE"
    assert report["git_sha"] == subprocess.run(
        ["git", "rev-parse", "HEAD"],
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
    ).stdout.strip()
    artifact_root = Path(json.loads(bindings.read_text(encoding="utf-8"))["artifact_root"])
    assert report["predictions_sha256"] == hashlib.sha256(
        (artifact_root / "predictions.jsonl").read_bytes()
    ).hexdigest()
    assert report["qrels_sha256"] == hashlib.sha256(qrels.read_bytes()).hexdigest()


def test_r27_external_bindings_cannot_override_reporter_owned_hashes(
    tmp_path: Path,
) -> None:
    path = tmp_path / "bindings.json"
    path.write_text(json.dumps({"qrels_sha256": "f" * 64}), encoding="utf-8")

    with pytest.raises(ValueError, match="BINDING_RESERVED"):
        rr.load_external_bindings(path)


@pytest.mark.parametrize("field", ["dirty_hash", "predictions_sha256"])
def test_r28_external_hash_bindings_must_be_sha256(
    tmp_path: Path, field: str
) -> None:
    path = tmp_path / "bindings.json"
    path.write_text(json.dumps({field: "not-a-sha"}), encoding="utf-8")

    with pytest.raises(ValueError, match="BINDING_INVALID"):
        rr.load_external_bindings(path)


def test_r28b_external_binding_rejects_tampered_canonical_artifact(tmp_path: Path) -> None:
    binding, public_key = _write_binding_artifacts(tmp_path)
    root = Path(json.loads(binding.read_text(encoding="utf-8"))["artifact_root"])
    (root / "predictions.jsonl").write_text("tampered\n", encoding="utf-8")

    with pytest.raises(ValueError, match="BINDING_CHECKSUM_MISMATCH"):
        rr.load_external_bindings(binding, attestation_public_key_b64=public_key)


def test_r28c_self_resigned_checksums_do_not_replace_artifact_attestation(
    tmp_path: Path,
) -> None:
    binding, public_key = _write_binding_artifacts(tmp_path)
    root = Path(json.loads(binding.read_text(encoding="utf-8"))["artifact_root"])
    predictions = root / "predictions.jsonl"
    predictions.write_text("tampered\n", encoding="utf-8")
    lines = (root / "checksums.sha256").read_text(encoding="utf-8").splitlines()
    rewritten = [
        f"{hashlib.sha256(predictions.read_bytes()).hexdigest()}  predictions.jsonl"
        if line.endswith("  predictions.jsonl") else line
        for line in lines
    ]
    (root / "checksums.sha256").write_text("\n".join(rewritten) + "\n", encoding="utf-8")

    with pytest.raises(ValueError, match="BINDING_ATTESTATION_INVALID"):
        rr.load_external_bindings(binding, attestation_public_key_b64=public_key)


def test_r28d_signed_artifact_still_rejects_malformed_dirty_hash(tmp_path: Path) -> None:
    binding, public_key = _write_binding_artifacts(
        tmp_path, manifest_overrides={"dirty_hash": "signed-but-not-a-sha"}
    )

    with pytest.raises(ValueError, match="BINDING_INVALID"):
        rr.load_external_bindings(binding, attestation_public_key_b64=public_key)


def test_r28e_signed_artifact_must_bind_current_qrels_and_query_set(tmp_path: Path) -> None:
    qrels = _eligible_qrels(tmp_path)
    qids = [json.loads(line)["query_id"] for line in qrels.read_text().splitlines()]
    binding, public_key = _write_binding_artifacts(
        tmp_path, qids=qids, qrels_hash=hashlib.sha256(qrels.read_bytes()).hexdigest()
    )
    rr.load_external_bindings(
        binding,
        attestation_public_key_b64=public_key,
        expected_qrels_sha256=hashlib.sha256(qrels.read_bytes()).hexdigest(),
        expected_qids=set(qids),
    )

    tampered_qrels = _write_qrels(tmp_path, [json.loads(line) for line in qrels.read_text().splitlines()[:-1]])
    with pytest.raises(ValueError, match="BINDING_QRELS_MISMATCH"):
        rr.load_external_bindings(
            binding,
            attestation_public_key_b64=public_key,
            expected_qrels_sha256=hashlib.sha256(tampered_qrels.read_bytes()).hexdigest(),
            expected_qids=set(qids[:-1]),
        )


def test_r28f_predictions_must_have_unique_qids_matching_qrels(tmp_path: Path) -> None:
    binding, public_key = _write_binding_artifacts(tmp_path, qids=["q1", "q1"], qrels_hash="a" * 64)
    with pytest.raises(ValueError, match="BINDING_PREDICTIONS_QID_MISMATCH"):
        rr.load_external_bindings(
            binding,
            attestation_public_key_b64=public_key,
            expected_qrels_sha256="a" * 64,
            expected_qids={"q1"},
        )


def test_r28g_review_sidecar_fields_must_match_qrels(tmp_path: Path) -> None:
    rows = [_row("q1")]
    pass_a = _write_review_pass(tmp_path, "A", rows)
    pass_b = _write_review_pass(tmp_path, "B", rows)
    altered = json.loads(pass_b.read_text())
    altered["source_id"] = "forged-source"
    pass_b.write_text(json.dumps(altered) + "\n")
    with pytest.raises(ValueError, match="REVIEW_QREL_MISMATCH"):
        rr.load_review_identity(
            pass_a, pass_b, expected_qids={"q1"},
            expected_qrel_rows={"q1": rows[0]},
        )


def test_r29_ineligible_or_unbound_report_is_never_written_as_release_eligible(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    qrels = _eligible_qrels(tmp_path)
    rows = [json.loads(line) for line in qrels.read_text(encoding="utf-8").splitlines()]
    policy_path = _write_policy(tmp_path)
    policy_payload = json.loads(policy_path.read_text(encoding="utf-8"))
    policy_payload["gates"]["require_model_identity"] = False
    policy_path.write_text(json.dumps(policy_payload), encoding="utf-8")
    policy_path.with_name(policy_path.name + ".sha256").write_text(
        hashlib.sha256(policy_path.read_bytes()).hexdigest() + "  " + policy_path.name + "\n",
        encoding="utf-8",
    )
    pass_a = _write_review_pass(tmp_path, "A", rows)
    pass_b = _write_review_pass(tmp_path, "B", rows)
    split = tmp_path / "split.json"
    split.write_text(json.dumps(_verified_split_manifest()), encoding="utf-8")
    out = tmp_path / "report.json"
    out.write_text(
        json.dumps({"verdict": "RELEASE_ELIGIBLE", "stale": True}),
        encoding="utf-8",
    )
    import orchestrator.eval.split as split_module
    monkeypatch.setattr(split_module, "validate_manifest", lambda manifest: None)
    monkeypatch.setattr(
        rr, "TRUSTED_POLICY_SHA256", hashlib.sha256(policy_path.read_bytes()).hexdigest()
    )

    rc = rr.main(
        [
            "--qrels", str(qrels), "--policy", str(policy_path),
            "--review-pass-a", str(pass_a), "--review-pass-b", str(pass_b),
            "--split-manifest", str(split), "--out", str(out),
        ],
    )

    assert rc == rr.EXIT_DATA_FAULT
    assert not out.exists(), "a stale eligible-looking report must not survive a data fault"
