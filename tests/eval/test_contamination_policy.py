"""E4 contamination-policy binding tests.

The E4 card orders the contamination policy committed and hash-pinned BEFORE
implementation, and forbids moving a frozen SLA without a new policy version.
These tests bind the frozen artifact to reality:

* the policy bytes must match their sha256 sidecar exactly (LF-only, the
  ``<sha256>  <basename>\\n`` convention already used by split-policy.v1.json);
* every pinned input hash is RE-DERIVED from the real file on disk rather than
  trusted from the policy text;
* layer 2 must be directional containment, never Jaccard or a ``min()``
  denominator -- the metric that was measured and rejected;
* layer 3 must state in writing that it cannot detect containment, so it is
  never mistaken for a containment detector;
* a skipped layer must forbid a clean verdict and exit code 0.

Offline: no Elasticsearch and no embedding service are required.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parents[2]
POLICY_PATH = REPO_ROOT / "data" / "eval" / "contamination" / "contamination-policy.v1.json"
SIDECAR_PATH = POLICY_PATH.with_suffix(POLICY_PATH.suffix + ".sha256")

SUPPORTED_POLICY_VERSIONS = ("v1",)

# The rejected metric, recorded so no future edit can quietly re-introduce it.
FORBIDDEN_LAYER2_DENOMINATORS = ("min(", "symmetric", "|q_ngrams OR c_ngrams|")


def _policy() -> dict:
    return json.loads(POLICY_PATH.read_text(encoding="utf-8"))


def _layers_by_id() -> dict[int, dict]:
    return {layer["id"]: layer for layer in _policy()["layers"]}


def _sha256_of(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


# --------------------------------------------------------------------------- #
# Freeze integrity
# --------------------------------------------------------------------------- #


def test_policy_and_sidecar_exist() -> None:
    assert POLICY_PATH.is_file(), f"frozen policy missing at {POLICY_PATH}"
    assert SIDECAR_PATH.is_file(), f"sha256 sidecar missing at {SIDECAR_PATH}"


def test_policy_bytes_match_the_sidecar_digest() -> None:
    """The freeze is only real if the bytes still hash to the pinned digest."""
    declared = SIDECAR_PATH.read_text(encoding="utf-8").split()[0]
    assert _sha256_of(POLICY_PATH) == declared


def test_sidecar_follows_the_split_policy_convention() -> None:
    """``<sha256>  <basename>\\n``, LF-only -- parity with split-policy.v1.json."""
    raw = SIDECAR_PATH.read_bytes()
    assert b"\r\n" not in raw, "sidecar must be LF-only, not CRLF"
    expected = f"{_sha256_of(POLICY_PATH)}  {POLICY_PATH.name}\n".encode()
    assert raw == expected


def test_policy_json_is_lf_only() -> None:
    """CRLF would change the bytes and therefore the frozen digest."""
    assert b"\r\n" not in POLICY_PATH.read_bytes()


def test_policy_identity_and_version() -> None:
    policy = _policy()
    assert policy["policy_id"] == "e4-contamination-policy"
    assert policy["policy_version"] in SUPPORTED_POLICY_VERSIONS


# --------------------------------------------------------------------------- #
# Pinned inputs re-derived from disk (not trusted from the policy text)
# --------------------------------------------------------------------------- #


@pytest.mark.parametrize(
    ("file_key", "sha_key", "bytes_key"),
    [
        ("queries_file", "queries_sha256", "queries_bytes"),
        ("qrels_file", "qrels_sha256", "qrels_bytes"),
    ],
)
def test_input_pins_match_on_disk_bytes(file_key: str, sha_key: str, bytes_key: str) -> None:
    pins = _policy()["input_pins"]
    path = REPO_ROOT / pins[file_key]
    assert path.is_file(), f"pinned input missing: {path}"
    assert _sha256_of(path) == pins[sha_key], f"{file_key} bytes changed since the freeze"
    assert path.stat().st_size == pins[bytes_key]


def test_split_policy_pin_matches_the_e3_artifact() -> None:
    """E4 pins the E3 split policy, so the two tasks cannot drift apart."""
    pins = _policy()["input_pins"]
    split_policy = REPO_ROOT / pins["split_policy_file"]
    assert split_policy.is_file()
    assert _sha256_of(split_policy) == pins["split_policy_sha256"]


def test_query_count_matches_the_real_queries_file() -> None:
    pins = _policy()["input_pins"]
    queries_path = REPO_ROOT / pins["queries_file"]
    real_count = sum(1 for line in queries_path.read_text(encoding="utf-8").splitlines() if line.strip())
    assert _policy()["data_scale"]["queries"] == real_count


def test_corpus_source_pins_are_real_shas() -> None:
    """Six real revisions, no placeholders; ``note`` is documentation, not a pin."""
    pins = {k: v for k, v in _policy()["corpus_source_pins"].items() if k != "note"}
    assert set(pins) == {"docker", "git", "go", "kubernetes", "postgresql", "python"}
    for source, revision in pins.items():
        assert len(revision) == 40, f"{source} pin is not a full sha1"
        assert all(c in "0123456789abcdef" for c in revision), f"{source} pin is not hex"


# --------------------------------------------------------------------------- #
# Four layers, with their blind spots in writing
# --------------------------------------------------------------------------- #


def test_four_layers_declared_with_metric_and_blind_spots() -> None:
    layers = _layers_by_id()
    assert sorted(layers) == [1, 2, 3, 4]
    for layer_id, layer in layers.items():
        for required in ("name", "metric", "threshold", "detects", "cannot_detect"):
            assert required in layer, f"layer {layer_id} lacks {required}"
        assert layer["cannot_detect"].strip(), f"layer {layer_id} must state its blind spot"
        assert 0.0 < float(layer["threshold"]) <= 1.0


def test_layer2_is_directional_containment_not_jaccard() -> None:
    """The correctness fix: containment_q with |q_ngrams| in the denominator."""
    layer = _layers_by_id()[2]
    assert layer["name"] == "containment"
    assert "containment_q" in layer["metric"]
    assert "|q_ngrams|" in layer["denominator"]
    assert "directional" in layer["denominator"]
    assert "jaccard" not in layer["metric"].lower()
    for forbidden in FORBIDDEN_LAYER2_DENOMINATORS:
        assert forbidden not in layer["denominator"], (
            f"layer 2 denominator must not be {forbidden!r}; that is the rejected metric"
        )


def test_layer3_declares_it_cannot_detect_containment() -> None:
    """Layer 3 estimates Jaccard, so it must never be read as a containment gate."""
    layer = _layers_by_id()[3]
    assert layer["name"] == "minhash"
    assert "containment" in layer["cannot_detect"].lower()
    assert "comparable" in layer["detects"].lower()
    assert layer["num_hashes"] == 128


def test_layer3_requires_python_int_signatures() -> None:
    """numpy uint64 would break isinstance(v, int) assertions downstream."""
    note = _layers_by_id()[3]["implementation_note"]
    assert "Python int" in note


def test_layer4_records_that_it_could_not_run_at_freeze() -> None:
    """The embedding service was measured unreachable; the policy must say so."""
    layer = _layers_by_id()[4]
    assert layer["name"] == "embedding"
    assert "UNREACHABLE" in layer["service_status_at_freeze"]
    assert "8009" in layer["service_status_at_freeze"]


# --------------------------------------------------------------------------- #
# The honesty gate
# --------------------------------------------------------------------------- #


def test_skipped_layer_rule_forbids_clean() -> None:
    rule = _policy()["skipped_layer_rule"]
    assert "MUST NOT emit clean" in rule["rule"]
    assert "clean" in rule["forbidden_output_when_incomplete"]
    assert "exit code 0" in rule["forbidden_output_when_incomplete"]
    assert "all four layers completed" in rule["clean_requires"]


def test_skipped_layer_rule_requires_visible_skips_and_a_manifest() -> None:
    required = _policy()["skipped_layer_rule"]["required_output_when_incomplete"]
    joined = " ".join(required).lower()
    assert "skipped layer" in joined
    assert "reason" in joined
    assert "incomplete manifest" in joined


def test_policy_records_the_locked_in_violation() -> None:
    """The pre-v1 dishonest behaviour was held in place by a passing test."""
    evidence = _policy()["skipped_layer_rule"]["violation_evidence"]
    assert "test_main_clean_exit_0" in evidence
    assert "returned 0" in evidence


def test_exit_codes_include_incomplete_as_distinct_from_clean() -> None:
    codes = _policy()["exit_codes"]
    assert codes["0"].startswith("CLEAN")
    assert codes["1"].startswith("BLOCKING")
    assert codes["3"].startswith("INCOMPLETE")
    assert "never clean" in codes["3"]
    assert "must treat 3 as a failure" in codes["contract_change_note"]


# --------------------------------------------------------------------------- #
# Measured evidence, not placeholders
# --------------------------------------------------------------------------- #


def test_blindness_evidence_records_the_reproduced_defect() -> None:
    """D1: a real query inserted verbatim was judged non-blocking."""
    evidence = _policy()["blindness_evidence"]
    assert evidence["verbatim_present"] is True
    assert evidence["old_verdict"]["blocking"] is False
    assert evidence["containment_q"] == 1.0
    assert evidence["jaccard"] < 0.8, "the whole point is that Jaccard missed it"
    assert evidence["minhash_estimate"] < 0.8
    assert evidence["intersection"] == evidence["query_ngrams"], "verbatim means full containment"


def test_rejected_metric_records_the_false_positives_it_produced() -> None:
    """D6: my own min() denominator produced 43 false positives; keep it recorded."""
    rejected = _policy()["rejected_metric"]
    assert "min(" in rejected["metric"]
    assert rejected["measured_hits_at_0_8"] == 43
    assert rejected["all_pairs_direction"] == "CHUNK_IN_Q"
    assert rejected["literal_substring_hits"] == 0
    assert rejected["max_containment_q_among_them"] < 0.8
    assert rejected["flagged_chunk_ngram_size_range"] == [1, 14]


def test_sla_is_frozen_with_a_measured_baseline() -> None:
    sla = _policy()["sla"]
    assert sla["max_wall_clock_seconds"] == 600
    assert sla["measured_baseline_seconds"] < sla["max_wall_clock_seconds"], (
        "an SLA below the measured baseline would be frozen already-failing"
    )
    assert sla["measured_old_implementation_seconds"] > sla["measured_baseline_seconds"]
    assert sla["peak_rss_measured_mb"] <= sla["peak_rss_mb"]
    assert sla["checkpoint_interval_chunks"] > 0


def test_data_scale_is_internally_consistent() -> None:
    """Arithmetic cross-checks catch a transcription typo in a frozen artifact."""
    scale = _policy()["data_scale"]
    assert scale["pair_space"] == scale["chunks"] * scale["queries"]
    census = _policy()["degenerate_fragment"]["measured_census"]
    assert census["corpus_total"] == scale["chunks"]
    assert census["lt_8"] < census["lt_16"] < census["lt_32"] < census["lt_64"]


def test_degenerate_fragment_census_percentages_match_the_counts() -> None:
    degenerate = _policy()["degenerate_fragment"]
    census = degenerate["measured_census"]
    pct = degenerate["measured_census_pct"]
    total = census["corpus_total"]
    for key in ("lt_8", "lt_16", "lt_32", "lt_64"):
        assert round(100.0 * census[key] / total, 2) == pct[key], f"{key} percentage disagrees with its count"


def test_degenerate_fragments_are_report_only() -> None:
    """Frozen by explicit user decision: report, never touch the data."""
    degenerate = _policy()["degenerate_fragment"]
    assert degenerate["handling"] == "report_only"
    assert degenerate["never_delete"] is True
    assert degenerate["never_silently_dropped"] is True
    assert degenerate["reported_bucket"] == "degenerate_fragment"


# --------------------------------------------------------------------------- #
# Failure semantics and manifest
# --------------------------------------------------------------------------- #


REQUIRED_FAILURE_CODES = {
    "POLICY_MISSING",
    "POLICY_HASH_MISMATCH",
    "POLICY_VERSION_UNKNOWN",
    "LAYER_SKIPPED_CLEAN_CLAIMED",
    "EMBEDDING_SERVICE_UNREACHABLE",
    "SLA_WALL_CLOCK_EXCEEDED",
    "DEGENERATE_FRAGMENT_DROPPED_SILENTLY",
    "CONTAMINATION_METRIC_SYMMETRIC",
    "MINHASH_SIGNATURE_NOT_INT",
    "MANIFEST_MISSING_FIELD",
    "MANIFEST_STALE",
}


def test_all_failure_codes_declared_with_enforceable_behaviour() -> None:
    entries = _policy()["failure_semantics"]
    codes = {entry["code"] for entry in entries}
    assert codes == REQUIRED_FAILURE_CODES
    for entry in entries:
        assert entry["behaviour"] in {"raise", "non_clean"}, (
            f"{entry['code']} must raise or force non_clean; a warning is not a gate"
        )
        assert entry["condition"].strip()


def test_metric_regression_guard_is_a_hard_failure() -> None:
    """Re-introducing the symmetric denominator must raise, not warn."""
    entries = {entry["code"]: entry for entry in _policy()["failure_semantics"]}
    assert entries["CONTAMINATION_METRIC_SYMMETRIC"]["behaviour"] == "raise"
    assert entries["LAYER_SKIPPED_CLEAN_CLAIMED"]["behaviour"] == "raise"
    assert entries["DEGENERATE_FRAGMENT_DROPPED_SILENTLY"]["behaviour"] == "raise"


def test_manifest_declares_required_fields_and_verdicts() -> None:
    manifest = _policy()["manifest"]
    assert manifest["format_version"] == "e4-contamination-manifest-v1"
    assert set(manifest["verdict_vocabulary"]) == {"CLEAN", "BLOCKING", "INCOMPLETE"}
    assert manifest["incomplete_manifest_on_interrupt"] is True
    for required in (
        "policy_sha256",
        "layers_completed",
        "layers_skipped",
        "verdict",
        "wall_clock_seconds",
        "containment_hits",
        "degenerate_fragments",
    ):
        assert required in manifest["required_fields"], f"manifest must carry {required}"


# --------------------------------------------------------------------------- #
# The honest corpus verdict
# --------------------------------------------------------------------------- #


def test_corpus_verdict_at_freeze_refuses_to_claim_clean() -> None:
    """0 text-layer hits with layer 4 never run is INCOMPLETE, not clean."""
    verdict = _policy()["corpus_verdict_at_freeze"]
    assert verdict["containment_at_0_8"]["pairs"] == 0
    assert verdict["verdict"] == "INCOMPLETE"
    assert "NEVER RUN" in verdict["embedding"]
    assert "NOT established as clean" in verdict["correct_phrasing"]
    assert verdict["forbidden_phrasing"] == "corpus is clean"
