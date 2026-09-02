"""Evaluation firewall: fail-closed path gating.

Under the active Goal, an agent under evaluation may read the question and
the corpus, and must never reach the answer, the label, or any prior score.
The versioned policy artifact
``data/eval/techdocs/split-policy.v1.json`` declares
``firewall.enforcement == "fail_closed"``: a path matching no rule is DENIED,
not allowed, and a denial must *raise* rather than silently return empty
content (a blank file reads like a real empty answer key, which is exactly the
kind of quiet-pass this project forbids).

These tests exist because the interesting failures are all *spellings*.  A
denylist that only recognises the canonical path is defeated by ``./``,
``..``, a backslash, a doubled slash, an absolute path, or a change of case.
Every such spelling of a denied file must still be denied.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from orchestrator.eval.split import (
    firewall_decision,
    check_path,
    load_policy,
    normalize_repo_path,
)

# One concrete, really-existing-shaped example per denied glob, with the
# reason code the policy assigns to that class of leak.
DENIED_EXAMPLES = [
    ("data/eval/techdocs/qrels.text.jsonl", "QRELS"),
    ("data/eval/techdocs/qrels.sol-review-pass-a.jsonl", "QRELS"),
    ("data/eval/techdocs/reports/bm25-report.json", "REPORTS"),
    ("data/eval/techdocs/splits/holdout-qids.json", "HOLDOUT"),
    ("results/eval/run-1/summary.json", "RESULTS_DIR"),
    ("eval_results/run-1/predictions.jsonl", "RESULTS_DIR"),
    ("eval/swebench_work/django__django-1/gold_patch.diff", "GOLD_PATCH"),
    ("some/dir/fix_gold.patch", "GOLD_PATCH"),
    ("eval/swebench_work/repo-1/tests/test_models.py", "TESTS_SOURCE"),
]

ALLOWED_EXAMPLES = [
    "data/eval/techdocs/queries.text.jsonl",
    "data/eval/techdocs/README.md",
    "data/eval/techdocs/qrels.schema.json",
]

# Alternate spellings of ONE denied file.  Each must be denied.
BYPASS_SPELLINGS = [
    "./data/eval/techdocs/qrels.text.jsonl",
    "data/./eval/techdocs/qrels.text.jsonl",
    "data/eval/techdocs/../techdocs/qrels.text.jsonl",
    "data/eval/../eval/techdocs/qrels.text.jsonl",
    "data//eval//techdocs//qrels.text.jsonl",
    "data\\eval\\techdocs\\qrels.text.jsonl",
    ".\\data\\eval\\techdocs\\qrels.text.jsonl",
    "/data/eval/techdocs/qrels.text.jsonl",
    "DATA/EVAL/TECHDOCS/QRELS.TEXT.JSONL",
    "data/eval/techdocs/QRELS.text.jsonl",
    "data/eval/techdocs/./qrels.text.jsonl",
]


def test_policy_declares_fail_closed_enforcement() -> None:
    """The firewall contract itself must say fail-closed, in the artifact."""
    firewall = load_policy()["firewall"]
    assert firewall["enforcement"] == "fail_closed"
    note = firewall["enforcement_note"]
    assert "DENIED" in note
    assert "not allowed" in note
    # Denial must raise, not fake a blank file.
    assert "never silently return empty" in note


@pytest.mark.parametrize("path,reason", DENIED_EXAMPLES)
def test_denied_paths_are_denied_with_reason(path: str, reason: str) -> None:
    """Each denied class resolves to DENIED and names its reason code."""
    decision, code = firewall_decision(path)
    assert decision == "DENIED", f"{path} was not denied (got {decision})"
    assert code == reason, f"{path} denied for {code}, expected {reason}"

    # check_path must raise, and the message must carry both the failure
    # code and the class of leak so a log line is self-explanatory.
    with pytest.raises(ValueError, match="FIREWALL_PATH_DENIED") as excinfo:
        check_path(path)
    assert reason in str(excinfo.value)


@pytest.mark.parametrize("path", ALLOWED_EXAMPLES)
def test_allowed_paths_are_allowed(path: str) -> None:
    """The question text, the README and the qrels *schema* stay readable."""
    decision, code = firewall_decision(path)
    assert decision == "ALLOWED", f"{path} should be readable (got {decision})"
    assert code is None
    assert check_path(path) == "ALLOWED"


def test_qrels_schema_is_allowed_but_qrels_data_is_not() -> None:
    """The schema describes the label format; the .jsonl carries the labels.

    ``qrels.schema.json`` must not be swept up by the ``qrels*.jsonl`` deny
    glob, and ``qrels.text.jsonl`` must not be waved through by the schema
    allow entry.  This pair is the easiest place to get a firewall wrong.
    """
    assert firewall_decision("data/eval/techdocs/qrels.schema.json")[0] == "ALLOWED"
    assert firewall_decision("data/eval/techdocs/qrels.text.jsonl")[0] == "DENIED"


@pytest.mark.parametrize("path", BYPASS_SPELLINGS)
def test_bypass_spellings_of_a_denied_path_still_denied(path: str) -> None:
    """`./`, `..`, backslash, `//`, leading `/` and case must not open a hole."""
    decision, _ = firewall_decision(path)
    assert decision == "DENIED", f"bypass succeeded via {path!r} (got {decision})"
    with pytest.raises(ValueError, match="FIREWALL_PATH_DENIED"):
        check_path(path)


def test_unknown_path_is_denied_not_allowed() -> None:
    """Fail-closed: no matching rule means refused, and it must raise."""
    decision, code = firewall_decision("some/random/file.txt")
    assert decision == "UNKNOWN"
    assert code is None
    with pytest.raises(ValueError, match="FIREWALL_PATH_UNKNOWN"):
        check_path("some/random/file.txt")


def test_traversal_escaping_the_repo_root_is_refused() -> None:
    """A path that climbs above the root cannot be classified, so it is denied."""
    for path in ("../outside.txt", "../../etc/passwd", "data/../../escape"):
        assert normalize_repo_path(path) is None, path
        decision, _ = firewall_decision(path)
        assert decision == "UNKNOWN", path
        with pytest.raises(ValueError, match="FIREWALL_PATH_UNKNOWN"):
            check_path(path)


def test_absolute_path_inside_repo_is_normalised_then_denied() -> None:
    """An absolute spelling of a denied file is still the denied file."""
    root = Path(__file__).resolve().parents[2]
    absolute = root / "data" / "eval" / "techdocs" / "qrels.text.jsonl"

    assert normalize_repo_path(str(absolute)) == "data/eval/techdocs/qrels.text.jsonl"
    decision, code = firewall_decision(str(absolute))
    assert decision == "DENIED"
    assert code == "QRELS"


def test_absolute_path_outside_repo_is_refused() -> None:
    """Outside the repo there is no rule that can apply, so refuse."""
    outside = "C:/Windows/System32/drivers/etc/hosts"
    assert normalize_repo_path(outside) is None
    with pytest.raises(ValueError, match="FIREWALL_PATH_UNKNOWN"):
        check_path(outside)


def test_case_variant_of_an_allowed_path_is_refused_not_allowed() -> None:
    """Allow matching is case-sensitive; deny matching is not.

    On a case-insensitive filesystem, matching the allowlist case-insensitively
    would let an attacker reach a file by respelling it.  The safe asymmetry is
    to deny broadly and allow narrowly, so an odd-cased spelling of an allowed
    file falls through to UNKNOWN and is refused rather than guessed at.
    """
    decision, _ = firewall_decision("data/eval/techdocs/QUERIES.TEXT.JSONL")
    assert decision == "UNKNOWN"
    with pytest.raises(ValueError, match="FIREWALL_PATH_UNKNOWN"):
        check_path("data/eval/techdocs/QUERIES.TEXT.JSONL")


def test_no_allowed_glob_is_also_denied_in_the_real_policy() -> None:
    """Policy self-consistency: the allowlist must not name a denied file.

    Guards against a future policy edit that opens a leak by adding an allow
    entry that a deny glob already covers.  With deny-precedence such an entry
    would be dead, silently misleading whoever reads the policy.
    """
    policy = load_policy()
    for allowed in policy["firewall"]["allowed_path_globs"]:
        decision, code = firewall_decision(allowed)
        assert decision == "ALLOWED", (
            f"allowlist entry {allowed!r} is shadowed by deny rule {code}"
        )


def test_every_declared_reason_code_is_reachable() -> None:
    """No dead reason codes: each documented code is produced by some glob."""
    policy = load_policy()
    declared = set(policy["firewall"]["denied_reason_codes"])
    produced = set()
    for path, _ in DENIED_EXAMPLES:
        _, code = firewall_decision(path)
        produced.add(code)
    assert declared == produced, (
        f"declared-but-unreachable: {declared - produced}; "
        f"produced-but-undeclared: {produced - declared}"
    )


def test_deny_takes_precedence_over_allow(tmp_path: Path) -> None:
    """When a path matches both lists, deny wins.

    The real policy has no such overlap (asserted above); this pins the
    resolution order so a future overlap fails safe instead of leaking.
    """
    policy = json.loads(Path(load_policy.__globals__["POLICY_PATH"]).read_text(encoding="utf-8"))
    policy["firewall"]["allowed_path_globs"] = ["data/eval/techdocs/qrels.text.jsonl"]

    decision, code = firewall_decision("data/eval/techdocs/qrels.text.jsonl", policy=policy)
    assert decision == "DENIED"
    assert code == "QRELS"


def test_denial_never_returns_empty_content() -> None:
    """The failure semantics forbid returning blank content instead of raising."""
    policy = load_policy()
    semantics = {entry["code"]: entry for entry in policy["failure_semantics"]}

    denied = semantics["FIREWALL_PATH_DENIED"]
    assert denied["behaviour"] == "raise"
    assert "empty content" in denied["forbidden_fallback"]

    unknown = semantics["FIREWALL_PATH_UNKNOWN"]
    assert unknown["behaviour"] == "raise"

    # And the implementation must actually raise rather than return "".
    result = firewall_decision("data/eval/techdocs/qrels.text.jsonl")
    assert result != ("ALLOWED", None)
    assert result[0] != ""


# Spellings that Windows itself resolves to the SAME denied file: NTFS strips
# trailing dots and spaces from a name, and ``name::$DATA`` opens the default
# data stream of ``name``.  Fail-closed already refuses these as UNKNOWN, so
# there is no live leak — but UNKNOWN is the wrong *reason*.  These paths are
# the answer key, and classifying them "no rule matched" means a future
# relaxation of the unknown-path rule (or any caller that logs UNKNOWN and
# continues) turns them into real bypasses.  Found by adversarial probing
# after the first all-green run.
WINDOWS_EQUIVALENT_SPELLINGS = [
    "data/eval/techdocs/qrels.text.jsonl.",
    "data/eval/techdocs/qrels.text.jsonl...",
    "data/eval/techdocs/qrels.text.jsonl ",
    "data/eval/techdocs/qrels.text.jsonl::$DATA",
    "data/eval/techdocs/qrels.text.jsonl:$DATA",
]


@pytest.mark.parametrize("path", WINDOWS_EQUIVALENT_SPELLINGS)
def test_windows_equivalent_spellings_denied_with_the_right_reason(path: str) -> None:
    """A filesystem-equivalent spelling of the answer key is the answer key."""
    decision, code = firewall_decision(path)
    assert decision == "DENIED", f"{path!r} classified {decision}, expected DENIED"
    assert code == "QRELS"
    with pytest.raises(ValueError, match="FIREWALL_PATH_DENIED"):
        check_path(path)


def test_trailing_strip_does_not_mangle_dotfiles() -> None:
    """Stripping is *trailing* only — a leading dot is part of the name."""
    assert normalize_repo_path(".gitignore") == ".gitignore"
    assert normalize_repo_path("data/.keep") == "data/.keep"
    assert normalize_repo_path("data/eval/techdocs/README.md") == (
        "data/eval/techdocs/README.md"
    )
