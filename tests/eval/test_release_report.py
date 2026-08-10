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
from pathlib import Path

import pytest

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
    result = rr.evaluate_gates(qrels, policy=policy)
    assert result.eligible is True, result.failed_codes
    assert result.failed_codes == ()


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


def test_r15_cli_returns_3_for_not_release_eligible(tmp_path: Path) -> None:
    rows = [_row(f"ok{i:03d}") for i in range(23)]
    qrels = _write_qrels(tmp_path, rows)
    policy_path = _write_policy(tmp_path)
    out = tmp_path / "report.json"

    rc = rr.main([
        "--qrels", str(qrels), "--policy", str(policy_path), "--out", str(out),
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


def test_r20_real_policy_and_qrels_on_disk_refuse(tmp_path: Path) -> None:
    """The shipped policy against the shipped qrels must refuse, with codes.

    This is the E5 deliverable asserted against the real artifacts rather than
    a fixture, so a drift in either one fails here.
    """
    out = tmp_path / "real-report.json"
    rc = rr.main(["--out", str(out)])
    assert rc == 3, "the real corpus is not release-eligible"

    report = json.loads(out.read_text(encoding="utf-8"))
    assert report["verdict"] == "NOT_RELEASE_ELIGIBLE"
    assert report["metrics"] == {}
    assert report["counts"]["total_rows"] == 180
    assert report["counts"]["golden_rows"] == 23
    assert report["counts"]["disputed_rows"] == 157
    assert report["counts"]["pure_negative_rows"] == 17
    assert report["counts"]["pure_negative_golden_rows"] == 0
    assert set(report["missing_sources"]) == {"docker", "kubernetes"}
    for code in (
        "GOLDEN_SET_TOO_SMALL",
        "GOLDEN_SET_ALL_POSITIVE",
        "SOURCE_COVERAGE_INCOMPLETE",
        "PURE_NEGATIVE_SET_UNREVIEWED",
        "TOO_MANY_DISPUTED",
    ):
        assert code in report["failed_codes"], code
    # provenance must be bound even when refusing
    assert len(report["policy_sha256"]) == 64
    assert len(report["qrels_sha256"]) == 64


def test_r17_cli_never_returns_1(tmp_path: Path) -> None:
    """1 is RESERVED_UNUSED so a crash can never read as a verdict."""
    policy_path = _write_policy(tmp_path)
    for qrels_arg in (str(tmp_path / "absent.jsonl"), str(_eligible_qrels(tmp_path))):
        rc = rr.main([
            "--qrels", qrels_arg, "--policy", str(policy_path),
            "--out", str(tmp_path / "r.json"),
        ])
        assert rc != 1, f"exit 1 is reserved; got it for {qrels_arg}"
