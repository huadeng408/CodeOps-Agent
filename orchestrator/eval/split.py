"""Dev/holdout split policy binding, dev lock, and the evaluation firewall.

The active Goal requires reproducible, leak-free evaluation. The techdocs
split is therefore *not* a code default and *not* whatever happens to be on
disk. It is decided by a committed, hashed policy artifact
(``data/eval/techdocs/split-policy.v1.json``) and this module is the only
sanctioned reader of it.

Three honesty properties are enforced here, in code rather than in prose:

1. **Policy binding.**  A missing policy, a policy whose bytes drifted from
   its sha256 sidecar, or an unknown ``policy_version`` all raise.  There is
   no implicit fallback split, because a split chosen by accident is not
   reproducible and would silently invalidate every number derived from it.

2. **An empty holdout is unmeasurable, not measurable-as-zero.**  All 180
   techdocs queries are provably seen (three retrieval bake-off reports and
   both Sol review passes carry the identical 180-qid denominator), so the
   policy locks all 180 into ``dev`` permanently and declares ``holdout``
   BLOCKED at size 0.  Asking for a holdout metric therefore *raises*.
   Returning ``0.0`` would be far worse than crashing: ``0.0`` flows into a
   release table and is indistinguishable from a measured zero.

3. **The firewall fails closed.**  An evaluated agent may read the question
   and the corpus; it must never reach the answer, the label, or any prior
   score.  A path matching no rule is DENIED, not allowed, and denial raises
   instead of returning blank content that would read like a real empty file.

The seed (``20260809``) is recorded rather than used for sampling: at policy
v1 the holdout is empty, so there is nothing to sample.  Pretending to draw a
random split here would be a fabricated procedure.  Sampling belongs to a
future policy v2 whose holdout is populated from net-new queries.
"""

from __future__ import annotations

import hashlib
import json
import re
from collections.abc import Iterable, Sequence
from datetime import UTC, datetime
from functools import lru_cache
from pathlib import Path

__all__ = [
    "EMPTY_SET_SHA256",
    "POLICY_PATH",
    "SUPPORTED_POLICY_VERSIONS",
    "check_path",
    "dev_qids",
    "firewall_decision",
    "generate_manifest",
    "holdout_qids",
    "load_policy",
    "normalize_repo_path",
    "policy_sha256",
    "qid_set_hash",
    "require_dev_not_shrunk",
    "require_holdout_measurable",
    "require_no_dev_holdout_overlap",
    "set_holdout_status",
    "validate_manifest",
]


def _repo_root() -> Path:
    """Repository root — ``orchestrator/eval/split.py`` is three levels down."""
    return Path(__file__).resolve().parents[2]


REPO_ROOT = _repo_root()
POLICY_PATH = REPO_ROOT / "data" / "eval" / "techdocs" / "split-policy.v1.json"
QUERIES_PATH = REPO_ROOT / "data" / "eval" / "techdocs" / "queries.text.jsonl"

SUPPORTED_POLICY_VERSIONS: tuple[str, ...] = ("v1",)

#: sha256 of the empty byte string.  Per the policy's ``empty_set_hash_rule``
#: an empty holdout hashes to this, and must always be accompanied by
#: ``holdout_status == "BLOCKED"`` so it can never be read as a computed set.
EMPTY_SET_SHA256 = hashlib.sha256(b"").hexdigest()

#: Which class of leak each denied glob represents.  The policy declares the
#: reason *codes* and their meanings; mapping a glob to a code is the
#: implementation's job.  A denied glob with no mapping raises rather than
#: being denied under an invented code, so editing the policy without editing
#: this table fails loudly instead of producing an undeclared reason.
_GLOB_REASONS: dict[str, str] = {
    "data/eval/techdocs/qrels*.jsonl": "QRELS",
    "data/eval/techdocs/reports/**": "REPORTS",
    "data/eval/techdocs/splits/holdout*": "HOLDOUT",
    "results/eval/**": "RESULTS_DIR",
    "eval_results/**": "RESULTS_DIR",
    "**/gold_patch*": "GOLD_PATCH",
    "**/*gold*.patch": "GOLD_PATCH",
    "eval/swebench_work/**/tests/**": "TESTS_SOURCE",
}


# --------------------------------------------------------------------------
# Policy binding
# --------------------------------------------------------------------------


def _sidecar_for(path: Path) -> Path:
    """``x.json`` -> ``x.json.sha256`` (the repo's checksum sidecar shape)."""
    return path.with_suffix(path.suffix + ".sha256")


def policy_sha256(path: Path | str | None = None) -> str:
    """sha256 of the policy bytes on disk."""
    target = Path(path) if path is not None else POLICY_PATH
    if not target.exists():
        raise ValueError(f"POLICY_MISSING: no split policy at {target}")
    return hashlib.sha256(target.read_bytes()).hexdigest()


def load_policy(path: Path | str | None = None) -> dict:
    """Read, hash-verify and version-check the split policy.

    Fail-closed by construction: every failure raises with the machine-readable
    code the policy itself declares in ``failure_semantics``, so a caller can
    never mistake a refusal for a permissive default.
    """
    target = Path(path) if path is not None else POLICY_PATH

    if not target.exists():
        raise ValueError(
            f"POLICY_MISSING: no split policy at {target}. "
            "Refusing to fall back to an implicit or default split."
        )

    sidecar = _sidecar_for(target)
    if not sidecar.exists():
        # An unverifiable policy is treated as no policy at all.
        raise ValueError(
            f"POLICY_MISSING: sha256 sidecar absent at {sidecar}; "
            "the policy bytes cannot be verified."
        )

    recorded = sidecar.read_text(encoding="utf-8").split()[0].strip().lower()
    actual = hashlib.sha256(target.read_bytes()).hexdigest()
    if recorded != actual:
        raise ValueError(
            f"POLICY_HASH_MISMATCH: {target} hashes to {actual} "
            f"but its sidecar records {recorded}."
        )

    policy = json.loads(target.read_text(encoding="utf-8"))

    version = policy.get("policy_version")
    if version not in SUPPORTED_POLICY_VERSIONS:
        raise ValueError(
            f"POLICY_VERSION_UNKNOWN: policy_version={version!r} is not one of "
            f"{list(SUPPORTED_POLICY_VERSIONS)}. Refusing to optimistically "
            "accept an unrecognised schema."
        )

    return policy


# --------------------------------------------------------------------------
# qid sets
# --------------------------------------------------------------------------


def _read_qids(path: Path) -> tuple[str, ...]:
    qids: list[str] = []
    with path.open(encoding="utf-8") as handle:
        for line in handle:
            if line.strip():
                qids.append(json.loads(line)["query_id"])
    return tuple(sorted(qids))


def dev_qids() -> tuple[str, ...]:
    """Every seen qid, sorted.  This is the permanently locked dev set."""
    if not QUERIES_PATH.exists():
        raise ValueError(f"POLICY_MISSING: queries file absent at {QUERIES_PATH}")
    return _read_qids(QUERIES_PATH)


def holdout_qids() -> tuple[str, ...]:
    """The holdout membership.  Empty while the policy declares it BLOCKED."""
    policy = load_policy()
    if policy["holdout"]["size"] == 0:
        return ()
    raise ValueError(
        "POLICY_VERSION_UNKNOWN: policy v1 declares an empty holdout but "
        f"holdout.size={policy['holdout']['size']}; a populated holdout "
        "requires a policy v2 with its own seed and manifest."
    )


def qid_set_hash(qids: Iterable[str]) -> str:
    """Hash a qid set per the policy's ``qid_hash_rule``.

    sha256 of newline-joined, lexicographically sorted qids with a single
    trailing newline, UTF-8 encoded.  The empty set hashes the empty byte
    string (``empty_set_hash_rule``).
    """
    items = sorted(set(qids))
    if not items:
        return EMPTY_SET_SHA256
    return hashlib.sha256(("\n".join(items) + "\n").encode("utf-8")).hexdigest()


# --------------------------------------------------------------------------
# Honesty gates
# --------------------------------------------------------------------------


def _whole_count(value: object, *, label: str) -> int:
    """Coerce a set size to a non-negative whole number, or refuse.

    A fractional or non-numeric size is not a set size.  Nothing in the repo
    can currently produce one — every size flows from ``len()`` — so this was
    a latent weakness rather than a reachable bug.  It is fixed anyway: the
    gate that decides whether a holdout claim is legitimate must refuse
    nonsense rather than guess at it.  ``holdout_size=0.4`` previously read as
    a genuine non-empty holdout and returned VERIFIED.
    """
    if isinstance(value, bool):
        count = int(value)
    elif isinstance(value, int):
        count = value
    elif isinstance(value, float):
        if not value.is_integer():
            raise TypeError(
                f"{label} must be a whole number of queries, got {value!r}; "
                "a fractional size is not a set size."
            )
        count = int(value)
    else:
        raise TypeError(
            f"{label} must be an int, got {type(value).__name__} ({value!r})."
        )

    if count < 0:
        raise ValueError(f"{label} cannot be negative, got {count}.")
    return count


def require_holdout_measurable(
    metric: str,
    *,
    holdout_size: int | None = None,
    policy: dict | None = None,
) -> None:
    """Raise unless a holdout metric is legitimately computable.

    This is the core anti-false-marking gate of E3.  The refusal deliberately
    carries no numeric payload: any plausible-looking value in the message
    could be copied into a report as if it were a measurement.
    """
    if holdout_size is None:
        policy = policy if policy is not None else load_policy()
        holdout_size = policy["holdout"]["size"]

    if _whole_count(holdout_size, label="holdout_size") == 0:
        raise ValueError(
            f"HOLDOUT_EMPTY_METRIC_REQUESTED: refusing to compute {metric!r} on an "
            "empty holdout (size 0, status BLOCKED). Policy e3-split-policy v1 sets "
            "holdout.metric_emission=FORBIDDEN_WHILE_EMPTY: an empty holdout is "
            "unmeasurable, not measurable-as-zero. See holdout.unblock_requirements "
            "in data/eval/techdocs/split-policy.v1.json."
        )


def set_holdout_status(status: str, *, holdout_size: int) -> str:
    """Gate the holdout status word against the holdout's actual size."""
    size = _whole_count(holdout_size, label="holdout_size")

    policy = load_policy()
    vocabulary = policy["status_vocabulary"]
    if status not in vocabulary:
        raise ValueError(
            f"holdout status {status!r} is not one of the four legal status "
            f"words {vocabulary}."
        )

    if size == 0 and status == "VERIFIED":
        raise ValueError(
            "HOLDOUT_VERIFIED_CLAIMED: cannot mark holdout VERIFIED while it holds "
            "no queries. Only BLOCKED is truthful at size 0."
        )

    return status


def require_no_dev_holdout_overlap(
    dev: Sequence[str], holdout: Sequence[str]
) -> None:
    """A qid in both splits is a leak regardless of what the sizes claim."""
    overlap = sorted(set(dev) & set(holdout))
    if overlap:
        raise ValueError(
            "DEV_QID_LEAKED_TO_HOLDOUT: "
            f"{len(overlap)} qid(s) appear in both dev and holdout: {overlap}"
        )


def require_dev_not_shrunk(candidate: Sequence[str]) -> None:
    """The dev lock is irreversible: a locked qid may never disappear."""
    locked = set(dev_qids())
    missing = sorted(locked - set(candidate))
    if missing:
        raise ValueError(
            "DEV_SET_SHRANK: "
            f"{len(missing)} locked dev qid(s) missing from the candidate split: "
            f"{missing}. Policy v1 declares demotion_from_dev_allowed=false."
        )


# --------------------------------------------------------------------------
# Split manifest
# --------------------------------------------------------------------------


def generate_manifest(*, path: Path | str | None = None, policy: dict | None = None) -> Path:
    """Write the split manifest declared by ``policy.manifest``.

    Deterministic at the fixed seed apart from ``generated_utc``.  The dev
    lock and overlap gates run here, so a manifest can never record a split
    that violates the policy it claims to implement.
    """
    policy = policy if policy is not None else load_policy()
    spec = policy["manifest"]

    target = Path(path) if path is not None else REPO_ROOT / spec["path"]

    dev = dev_qids()
    holdout = holdout_qids()
    require_dev_not_shrunk(dev)
    require_no_dev_holdout_overlap(dev, holdout)

    manifest = {
        "format_version": spec["format_version"],
        "policy_id": policy["policy_id"],
        "policy_version": policy["policy_version"],
        "policy_sha256": policy_sha256(),
        "seed": policy["seed"],
        "generated_utc": datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "dev_qids_sha256": qid_set_hash(dev),
        "dev_size": len(dev),
        "holdout_qids_sha256": qid_set_hash(holdout),
        "holdout_size": len(holdout),
        "holdout_status": set_holdout_status(
            policy["holdout"]["status"], holdout_size=len(holdout)
        ),
    }

    missing = [f for f in spec["required_fields"] if f not in manifest]
    if missing:
        raise ValueError(f"MANIFEST_MISSING_FIELD: {missing}")

    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(
        json.dumps(manifest, indent=2, sort_keys=True, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )
    return target


def validate_manifest(manifest: dict, *, policy: dict | None = None) -> None:
    """Fail closed on an incomplete or out-of-date manifest."""
    policy = policy if policy is not None else load_policy()

    for field in policy["manifest"]["required_fields"]:
        if field not in manifest:
            raise ValueError(f"MANIFEST_MISSING_FIELD: {field!r} absent from manifest")

    current = policy_sha256()
    if manifest["policy_sha256"] != current:
        raise ValueError(
            f"MANIFEST_STALE: manifest pins policy_sha256={manifest['policy_sha256']} "
            f"but the policy on disk hashes to {current}."
        )

    identity_expected = {
        "format_version": policy["manifest"]["format_version"],
        "policy_id": policy["policy_id"],
        "policy_version": policy["policy_version"],
        "seed": policy["seed"],
    }
    identity_mismatched = [
        key for key, value in identity_expected.items() if manifest.get(key) != value
    ]
    if identity_mismatched:
        raise ValueError(
            "MANIFEST_POLICY_IDENTITY_MISMATCH: split manifest identity does not "
            f"match the policy for {', '.join(identity_mismatched)}"
        )

    expected_dev = dev_qids()
    expected_holdout = holdout_qids()
    expected = {
        "dev_size": len(expected_dev),
        "dev_qids_sha256": qid_set_hash(expected_dev),
        "holdout_size": len(expected_holdout),
        "holdout_qids_sha256": qid_set_hash(expected_holdout),
        "holdout_status": set_holdout_status(
            policy["holdout"]["status"], holdout_size=len(expected_holdout)
        ),
    }
    mismatched = [key for key, value in expected.items() if manifest.get(key) != value]
    if mismatched:
        raise ValueError(
            "MANIFEST_MEMBERSHIP_MISMATCH: split manifest does not match the "
            f"policy-bound query membership for {', '.join(mismatched)}"
        )


# --------------------------------------------------------------------------
# Evaluation firewall
# --------------------------------------------------------------------------


def _glob_to_regex(pattern: str) -> str:
    """Translate a path glob to a regex with correct ``*`` / ``**`` semantics.

    ``fnmatch`` is unusable here because its ``*`` crosses ``/``, which would
    silently over-deny (and mask real allowlist entries).  ``*`` matches within
    one segment; ``**/`` matches zero or more whole segments; a trailing
    ``/**`` matches the subtree.
    """
    out = ["^"]
    i, n = 0, len(pattern)
    while i < n:
        if pattern.startswith("**/", i):
            out.append("(?:[^/]+/)*")
            i += 3
        elif pattern.startswith("/**", i) and i + 3 == n:
            out.append("(?:/.*)?")
            i += 3
        elif pattern.startswith("**", i):
            out.append(".*")
            i += 2
        elif pattern[i] == "*":
            out.append("[^/]*")
            i += 1
        elif pattern[i] == "?":
            out.append("[^/]")
            i += 1
        else:
            out.append(re.escape(pattern[i]))
            i += 1
    out.append("$")
    return "".join(out)


@lru_cache(maxsize=512)
def _compiled(pattern: str) -> re.Pattern[str]:
    return re.compile(_glob_to_regex(pattern))


def _canonical_segment(segment: str) -> str | None:
    """Reduce one path segment to what the filesystem would actually open.

    Windows discards trailing dots and spaces, and ``name:stream`` opens a
    stream of ``name`` — so ``qrels.text.jsonl.`` and ``qrels.text.jsonl::$DATA``
    both reach ``qrels.text.jsonl``.  Matching the literal spelling classified
    those as UNKNOWN.  Fail-closed already refused them, so this was not a live
    leak, but the *reason* was wrong and any future relaxation of the
    unknown-path rule would have turned them into real bypasses.  Canonicalising
    here keeps the denylist pointed at the file the OS would hand over.

    Leading dots are preserved — ``.gitignore`` is a legitimate name.  Returns
    ``None`` when nothing addressable remains (e.g. a bare ``...``).
    """
    # A colon is not legal in a relative segment except as a stream suffix,
    # so taking the part before it cannot mangle a real filename.
    base = segment.split(":", 1)[0]
    return base.rstrip(". ") or None


def _canonical_segment(segment: str) -> str | None:
    """Reduce one path segment to the name the filesystem will actually open.

    Windows silently discards trailing dots and spaces, and ``name:stream``
    opens a stream of ``name`` — so ``qrels.text.jsonl.`` and
    ``qrels.text.jsonl::$DATA`` both reach ``qrels.text.jsonl``.  Matching the
    literal spelling classified those as UNKNOWN.  Fail-closed already refused
    them, so this was never a live leak, but the recorded *reason* was wrong and
    any future softening of the unknown-path rule would have turned them into
    real bypasses.  Canonicalising here keeps the denylist pointed at the file
    the OS would hand over.

    Leading dots are preserved: ``.gitignore`` is a legitimate name.
    """
    # A colon is not legal in a relative segment except as a stream separator,
    # so taking the part before it cannot mangle a real filename.
    base = segment.split(":", 1)[0].rstrip(". ")
    return base or None


def normalize_repo_path(raw: str | Path) -> str | None:
    """Reduce a path to one canonical repo-relative POSIX spelling.

    Returns ``None`` when the path cannot be placed inside the repository —
    it escapes the root, or it is an absolute path elsewhere on the machine.
    ``None`` means "unclassifiable", which the caller must treat as refused.
    """
    text = str(raw).strip().replace("\\", "/")
    if not text:
        return None

    # Absolute with a drive letter: resolve against the real repo root.
    if len(text) >= 2 and text[1] == ":":
        try:
            resolved = Path(text).resolve()
        except OSError:
            return None
        try:
            rel = resolved.relative_to(REPO_ROOT)
        except ValueError:
            return None
        return rel.as_posix() if rel.parts else None

    # A single leading "/" is read as repo-root-anchored, not filesystem root.
    text = text.lstrip("/")

    parts: list[str] = []
    for segment in text.split("/"):
        if segment in ("", "."):
            continue
        if segment == "..":
            if not parts:
                return None  # climbs above the repo root
            parts.pop()
            continue
        canonical = _canonical_segment(segment)
        if canonical is None:
            return None
        parts.append(canonical)

    return "/".join(parts) if parts else None


def firewall_decision(
    path: str | Path, *, policy: dict | None = None
) -> tuple[str, str | None]:
    """Classify a path as ``ALLOWED`` / ``DENIED`` / ``UNKNOWN``.

    Deny is evaluated first and case-insensitively (deny broadly); allow is
    matched case-sensitively on the canonical spelling (allow narrowly).  On a
    case-insensitive filesystem the reverse asymmetry would let an attacker
    reach a file simply by respelling its case.
    """
    policy = policy if policy is not None else load_policy()
    firewall = policy["firewall"]

    normalized = normalize_repo_path(path)
    if normalized is None:
        return ("UNKNOWN", None)

    lowered = normalized.lower()
    for glob in firewall["denied_path_globs"]:
        if _compiled(glob.lower()).match(lowered):
            reason = _GLOB_REASONS.get(glob)
            if reason is None:
                raise ValueError(
                    f"denied glob {glob!r} has no reason-code mapping in "
                    "orchestrator.eval.split._GLOB_REASONS; refusing to deny "
                    "under an undeclared reason."
                )
            return ("DENIED", reason)

    for glob in firewall["allowed_path_globs"]:
        if _compiled(glob).match(normalized):
            return ("ALLOWED", None)

    return ("UNKNOWN", None)


def check_path(path: str | Path, *, policy: dict | None = None) -> str:
    """Fail-closed gate.  Returns ``"ALLOWED"`` or raises.

    Never returns empty content in place of a refusal: a blank file reads like
    a genuine empty answer key, which is the quiet-pass this project forbids.
    """
    decision, reason = firewall_decision(path, policy=policy)

    if decision == "DENIED":
        raise ValueError(
            f"FIREWALL_PATH_DENIED: {reason} — {path!r} is on the evaluation "
            "denylist and must not be read by an evaluated agent."
        )
    if decision == "UNKNOWN":
        raise ValueError(
            f"FIREWALL_PATH_UNKNOWN: {path!r} matches neither the allowlist nor "
            "the denylist. Enforcement is fail_closed, so it is refused."
        )
    return decision
