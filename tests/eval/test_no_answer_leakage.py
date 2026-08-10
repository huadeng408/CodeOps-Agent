"""The optimisation path must never read the answer.

Every claim of the form "the harness scored higher after I changed X" rests
entirely on the optimisation not having seen the answer.  A score obtained by
reading ``test_patch`` or ``FAIL_TO_PASS`` is not a score, and a reviewer has no
way to tell the two apart from the number alone.  So the guarantee is asserted
three ways:

1. **Structurally** (AST): the optimisation modules do not mention the answer
   fields at all.
2. **Behaviourally**: an instance carrying answer fields produces byte-identical
   output to one without them.
3. **Against itself**: the detector is shown to actually catch a planted leak.

(3) is not optional.  A structural check that greps the wrong place passes
vacuously, and a vacuous anti-cheating check is worse than none — it converts
"we did not verify this" into "we verified this".
"""

from __future__ import annotations

import ast
from pathlib import Path

import pytest

from eval.harness.leakage import (
    ALLOWED_METADATA_KEYS,
    ANSWER_FIELDS,
    AnswerLeakageError,
    assert_no_answer_fields,
    sanitise_metadata,
)

REPO_ROOT = Path(__file__).resolve().parents[2]

#: Modules that make up the optimisation path.  Listed explicitly so adding a
#: new stage without adding it here is caught by
#: ``test_module_list_covers_every_optimisation_module``.
OPTIMISATION_MODULES: tuple[str, ...] = (
    "eval/harness/localize.py",
    "eval/harness/uplift.py",
    "eval/harness/validate.py",
    "eval/harness/select.py",
)

FULL_METADATA = {
    "repo": "astropy/astropy",
    "base_commit": "d16bfe05a744909de4b27f5875fe0d4ed41ce607",
    "version": "5.1",
    "synthetic": False,
    "test_patch": "diff --git a/astropy/modeling/tests/test_separable.py ...",
    "hints_text": "the bug is in _cstack, see line 242",
    "FAIL_TO_PASS": ["astropy/modeling/tests/test_separable.py::test_separable"],
    "PASS_TO_PASS": ["astropy/modeling/tests/test_separable.py::test_coord_matrix"],
}


def _existing_modules() -> list[Path]:
    return [REPO_ROOT / rel for rel in OPTIMISATION_MODULES if (REPO_ROOT / rel).is_file()]


def _leaked_names(source: str) -> set[str]:
    """Every answer-field name appearing anywhere in *source*'s AST.

    Checks attribute access (``inst.test_patch``), subscripts and any string
    constant (``meta["test_patch"]``, ``meta.get("FAIL_TO_PASS")``), because the
    realistic way to read these is by string key.
    """
    tree = ast.parse(source)
    found: set[str] = set()
    for node in ast.walk(tree):
        if isinstance(node, ast.Attribute) and node.attr in ANSWER_FIELDS:
            found.add(node.attr)
        elif isinstance(node, ast.Name) and node.id in ANSWER_FIELDS:
            found.add(node.id)
        elif isinstance(node, ast.Constant) and isinstance(node.value, str):
            if node.value in ANSWER_FIELDS:
                found.add(node.value)
    return found


# ---------------------------------------------------------------------------
# (3) first: the detector must work
# ---------------------------------------------------------------------------


def test_detector_catches_a_planted_attribute_leak() -> None:
    assert _leaked_names("x = instance.test_patch\n") == {"test_patch"}


def test_detector_catches_a_planted_string_key_leak() -> None:
    assert _leaked_names('x = meta["FAIL_TO_PASS"]\n') == {"FAIL_TO_PASS"}


def test_detector_catches_a_planted_get_leak() -> None:
    assert _leaked_names('x = meta.get("test_patch", "")\n') == {"test_patch"}


def test_detector_is_quiet_on_legitimate_source() -> None:
    clean = 'issue = meta["base_commit"]\nrepo = meta.get("repo", "")\n'
    assert _leaked_names(clean) == set()


def test_answer_field_list_is_not_empty() -> None:
    """A vacuous field list would make every structural assertion pass."""
    assert len(ANSWER_FIELDS) >= 5
    assert "test_patch" in ANSWER_FIELDS
    assert "FAIL_TO_PASS" in ANSWER_FIELDS


# ---------------------------------------------------------------------------
# (1) structural
# ---------------------------------------------------------------------------


def test_optimisation_modules_never_mention_an_answer_field() -> None:
    offenders: dict[str, set[str]] = {}
    for path in _existing_modules():
        leaked = _leaked_names(path.read_text(encoding="utf-8"))
        if leaked:
            offenders[str(path.relative_to(REPO_ROOT))] = leaked
    assert not offenders, (
        "optimisation modules reference answer fields, so any score they "
        f"produce is unusable: {offenders}"
    )


def test_module_list_covers_every_optimisation_module() -> None:
    """Guard against a new stage escaping the structural check.

    Any ``eval/harness`` module whose name marks it as part of the uplift work
    must be listed in OPTIMISATION_MODULES.
    """
    marked = {
        f"eval/harness/{p.name}"
        for p in (REPO_ROOT / "eval" / "harness").glob("*.py")
        if p.stem in {"localize", "validate", "select", "uplift"}
    }
    unlisted = marked - set(OPTIMISATION_MODULES)
    assert not unlisted, f"optimisation modules not covered by the leak check: {unlisted}"


# ---------------------------------------------------------------------------
# (2) behavioural
# ---------------------------------------------------------------------------


def test_sanitise_strips_every_answer_field() -> None:
    clean = sanitise_metadata(FULL_METADATA)
    for field in ANSWER_FIELDS:
        assert field not in clean, f"{field} survived sanitisation"


def test_sanitise_keeps_what_the_optimisation_legitimately_needs() -> None:
    clean = sanitise_metadata(FULL_METADATA)
    assert clean["repo"] == "astropy/astropy"
    assert clean["base_commit"].startswith("d16bfe05")


def test_sanitise_is_an_allow_list_not_a_deny_list() -> None:
    """A field nobody has thought about yet must not flow through.

    Deny-lists fail open: the next dataset column that happens to leak the
    answer would pass until someone noticed.
    """
    clean = sanitise_metadata({**FULL_METADATA, "some_future_answer_column": "x"})
    assert "some_future_answer_column" not in clean


def test_sanitised_metadata_passes_the_boundary_assertion() -> None:
    assert_no_answer_fields(sanitise_metadata(FULL_METADATA), where="test")


def test_unsanitised_metadata_fails_the_boundary_assertion() -> None:
    with pytest.raises(AnswerLeakageError) as excinfo:
        assert_no_answer_fields(FULL_METADATA, where="localize")
    message = str(excinfo.value)
    assert "localize" in message, "the error must name the boundary that was crossed"
    assert "test_patch" in message


def test_empty_answer_fields_are_not_treated_as_leaks() -> None:
    """The dataset stores ``""`` for absent hints; that is not a leak."""
    assert_no_answer_fields(
        {"repo": "a/b", "base_commit": "abc", "test_patch": "", "hints_text": ""},
        where="test",
    )


def test_allowed_keys_contain_no_answer_field() -> None:
    """The two lists must not contradict each other."""
    assert not (ALLOWED_METADATA_KEYS & set(ANSWER_FIELDS))
