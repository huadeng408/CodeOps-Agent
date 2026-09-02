"""Permanent dev lock, BLOCKED holdout, and the split manifest.

The versioned split policy records that all 180 techdocs queries are provably
*seen*: each appears in the denominator of three retrieval bake-off
reports and both Sol review passes.  Exposure is irreversible, so the policy
locks all 180 into ``dev`` PERMANENTLY and declares ``holdout`` BLOCKED at
size 0.

The honesty gate this file defends is narrow and specific: an empty holdout
must be **unmeasurable**, not measurable-as-zero.  A function that returns
0.0 (or 1.0, or an empty report) for a holdout metric is worse than one that
crashes, because 0.0 flows into a release table and reads like a measurement
of a set that does not exist.  Every test below asserts a raise, and then
additionally asserts that no plausible-looking number was produced.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path

import pytest

from orchestrator.eval.split import (
    EMPTY_SET_SHA256,
    dev_qids,
    generate_manifest,
    holdout_qids,
    load_policy,
    qid_set_hash,
    require_no_dev_holdout_overlap,
    require_holdout_measurable,
    require_dev_not_shrunk,
    set_holdout_status,
    validate_manifest,
)

REPO_ROOT = Path(__file__).resolve().parents[2]


def test_dev_is_all_180_and_locked_permanently() -> None:
    """dev holds every seen qid, and the lock is declared irreversible."""
    policy = load_policy()
    dev = policy["dev"]

    assert dev["size"] == 180
    assert dev["lock"] == "PERMANENT"
    assert dev["lock_is_irreversible"] is True
    assert dev["demotion_from_dev_allowed"] is False

    # The declared size is the real number of qids on disk, not a guess.
    qids = dev_qids()
    assert len(qids) == 180
    assert len(set(qids)) == 180
    assert qids == tuple(sorted(qids)), "dev_qids() must be deterministic (sorted)"

    # Bound to the real queries file, not a fixture.
    raw = (REPO_ROOT / "data" / "eval" / "techdocs" / "queries.text.jsonl").read_text(
        encoding="utf-8"
    )
    on_disk = {json.loads(line)["query_id"] for line in raw.splitlines() if line.strip()}
    assert set(qids) == on_disk


def test_holdout_is_empty_and_blocked() -> None:
    """holdout is size 0 / BLOCKED, and says so in machine-readable form."""
    policy = load_policy()
    holdout = policy["holdout"]

    assert holdout["size"] == 0
    assert holdout["status"] == "BLOCKED"
    assert holdout["metric_emission"] == "FORBIDDEN_WHILE_EMPTY"
    assert holdout_qids() == ()

    # BLOCKED is one of the four legal status words, not free prose.
    assert holdout["status"] in policy["status_vocabulary"]


def test_holdout_metric_request_raises_and_returns_no_number() -> None:
    """Asking for a holdout metric on an empty set must raise, never return.

    This is the core anti-false-marking gate of E3.  ``0.0`` in a release
    table is indistinguishable from a measured zero; a raise is not.
    """
    with pytest.raises(ValueError, match="HOLDOUT_EMPTY_METRIC_REQUESTED") as exc:
        require_holdout_measurable("ndcg@10")

    # The error must name the metric so the caller knows what was refused,
    # and must not hand back any of the forbidden fallbacks in its payload.
    message = str(exc.value)
    assert "ndcg@10" in message
    for forbidden in ("0.0", "1.0", "100%", "None"):
        assert forbidden not in message, (
            f"error message leaks a plausible measurement {forbidden!r}: {message!r}"
        )


def test_holdout_cannot_be_marked_verified_while_empty() -> None:
    """VERIFIED on an empty holdout is the exact false-marking §20 forbids."""
    with pytest.raises(ValueError, match="HOLDOUT_VERIFIED_CLAIMED"):
        set_holdout_status("VERIFIED", holdout_size=0)

    # IMPLEMENTED/DESIGNED are equally illegitimate for an empty set: there is
    # nothing implemented either.  Only BLOCKED is truthful at size 0.
    assert set_holdout_status("BLOCKED", holdout_size=0) == "BLOCKED"

    # With a real non-empty holdout, VERIFIED becomes a legal word again
    # (whether it is *earned* is a separate question this function cannot judge).
    assert set_holdout_status("VERIFIED", holdout_size=50) == "VERIFIED"


def test_policy_forbids_contamination_laundering_by_name() -> None:
    """The policy must enumerate the cheats, not just omit them."""
    holdout = load_policy()["holdout"]

    forbidden = holdout["explicitly_forbidden_constructions"]
    assert len(forbidden) >= 4
    blob = " ".join(forbidden).lower()
    assert "resampling" in blob
    assert "relabelling" in blob or "relabeling" in blob
    assert "paraphrasing" in blob
    assert "pure-negative" in blob
    assert "laundering" in holdout["blocked_reason"].lower()

    # And it must say what would actually unblock it.
    unblock = holdout["unblock_requirements"]
    assert len(unblock) >= 4
    assert "net-new" in " ".join(unblock).lower()


def test_dev_holdout_overlap_raises() -> None:
    """A qid in both splits is a leak, whatever the sizes say."""
    dev = dev_qids()

    # The real state: holdout empty → no overlap possible.
    require_no_dev_holdout_overlap(dev, ())

    # Any single shared qid must raise.
    with pytest.raises(ValueError, match="DEV_QID_LEAKED_TO_HOLDOUT") as exc:
        require_no_dev_holdout_overlap(dev, (dev[0],))
    assert dev[0] in str(exc.value), "the raise must name the leaked qid"


def test_dev_set_cannot_shrink() -> None:
    """Dropping a locked dev qid must raise, not quietly improve scores."""
    dev = dev_qids()

    require_dev_not_shrunk(dev)  # identity is fine
    require_dev_not_shrunk(dev + ("new-q001",))  # growth is fine

    with pytest.raises(ValueError, match="DEV_SET_SHRANK") as exc:
        require_dev_not_shrunk(dev[1:])
    assert dev[0] in str(exc.value), "the raise must name the dropped qid"


def test_manifest_carries_every_required_field_and_binds_the_policy(
    tmp_path: Path,
) -> None:
    """generate_manifest() emits all 10 declared fields, pinned to the policy."""
    policy = load_policy()
    target = tmp_path / "split-manifest.v1.json"

    path = generate_manifest(path=target)
    assert path == target
    manifest = json.loads(target.read_text(encoding="utf-8"))

    for field in policy["manifest"]["required_fields"]:
        assert field in manifest, f"manifest missing required field {field!r}"

    assert manifest["format_version"] == policy["manifest"]["format_version"]
    assert manifest["policy_id"] == policy["policy_id"]
    assert manifest["policy_version"] == policy["policy_version"]
    assert manifest["seed"] == policy["seed"] == 20260809

    assert manifest["dev_size"] == 180
    assert manifest["holdout_size"] == 0
    assert manifest["holdout_status"] == "BLOCKED"


def test_manifest_dev_hash_is_real_and_holdout_hash_is_the_empty_digest(
    tmp_path: Path,
) -> None:
    """The empty holdout hashes the empty byte string — and says BLOCKED too.

    Per the policy's ``empty_set_hash_rule`` an empty digest must never be
    mistakable for a computed set, so it is always accompanied by
    holdout_status=BLOCKED.
    """
    target = tmp_path / "m.json"
    generate_manifest(path=target)
    manifest = json.loads(target.read_text(encoding="utf-8"))

    assert EMPTY_SET_SHA256 == hashlib.sha256(b"").hexdigest()
    assert manifest["holdout_qids_sha256"] == EMPTY_SET_SHA256
    assert manifest["holdout_status"] == "BLOCKED"

    dev_hash = manifest["dev_qids_sha256"]
    assert dev_hash == qid_set_hash(dev_qids())
    assert dev_hash != EMPTY_SET_SHA256
    assert len(dev_hash) == 64 and dev_hash == dev_hash.lower()


def test_manifest_is_deterministic_at_the_fixed_seed(tmp_path: Path) -> None:
    """Two generations at seed 20260809 agree on every field but the timestamp."""
    a = json.loads(generate_manifest(path=tmp_path / "a.json").read_text(encoding="utf-8"))
    b = json.loads(generate_manifest(path=tmp_path / "b.json").read_text(encoding="utf-8"))

    a.pop("generated_utc")
    b.pop("generated_utc")
    assert a == b


def test_validate_manifest_rejects_missing_field_and_stale_policy_hash(
    tmp_path: Path,
) -> None:
    """Fail closed on an incomplete or out-of-date manifest."""
    target = tmp_path / "m.json"
    generate_manifest(path=target)
    good = json.loads(target.read_text(encoding="utf-8"))

    validate_manifest(good)  # the freshly generated one validates

    missing = dict(good)
    missing.pop("dev_qids_sha256")
    with pytest.raises(ValueError, match="MANIFEST_MISSING_FIELD") as exc:
        validate_manifest(missing)
    assert "dev_qids_sha256" in str(exc.value)

    stale = dict(good)
    stale["policy_sha256"] = "f" * 64
    with pytest.raises(ValueError, match="MANIFEST_STALE"):
        validate_manifest(stale)


def test_validate_manifest_rejects_forged_nonempty_v1_holdout(tmp_path: Path) -> None:
    target = tmp_path / "split-manifest.v1.json"
    generate_manifest(path=target)
    forged = json.loads(target.read_text(encoding="utf-8"))
    forged.update(
        {
            "holdout_size": 24,
            "holdout_status": "VERIFIED",
            "holdout_qids_sha256": "b" * 64,
        }
    )

    with pytest.raises(ValueError, match="MANIFEST_MEMBERSHIP_MISMATCH"):
        validate_manifest(forged)


def test_holdout_size_must_be_a_whole_number() -> None:
    """A fractional or non-numeric holdout size is nonsense and must refuse.

    Found by adversarial probing after the first all-green run: the gate only
    asked ``size == 0``, so ``holdout_size=0.4`` sailed through as a genuine
    non-empty holdout and returned VERIFIED.  No current caller can produce a
    fractional size — they all come from ``len()`` — so this was latent, not
    exploitable.  "Not currently reachable" is not the bar this project sets
    for the gate that decides whether a holdout claim is legitimate.
    """
    # Fractional sizes are not a holdout of any size.
    for bad in (0.4, 1.5, -0.5):
        with pytest.raises((TypeError, ValueError)):
            set_holdout_status("VERIFIED", holdout_size=bad)

    # Non-numeric sizes must refuse on type, never coerce.
    for bad in ("5", "0", None, [], {}):
        with pytest.raises(TypeError):
            set_holdout_status("VERIFIED", holdout_size=bad)

    # A negative size is not "non-empty" either.
    with pytest.raises(ValueError):
        set_holdout_status("VERIFIED", holdout_size=-1)

    # Whole numbers still work, including whole-valued floats.
    assert set_holdout_status("VERIFIED", holdout_size=50) == "VERIFIED"
    assert set_holdout_status("VERIFIED", holdout_size=50.0) == "VERIFIED"

    # The empty cases must still raise the *semantic* code, not a TypeError,
    # so the log line still says why the claim was refused.
    for empty in (0, 0.0, False):
        with pytest.raises(ValueError, match="HOLDOUT_VERIFIED_CLAIMED"):
            set_holdout_status("VERIFIED", holdout_size=empty)
