"""The set of instance fields that constitute the answer, and a sanitiser.

SWE-bench instance metadata carries fields that *are* the answer or a direct
pointer to it:

``test_patch``
    The tests the real fix added.  Reading it tells you exactly which behaviour
    must change, and often exactly where.
``patch``
    The developer's own fix.
``FAIL_TO_PASS`` / ``PASS_TO_PASS``
    The official grading lists.  Reading them turns "solve the issue" into
    "make these named tests pass".
``hints_text``
    Maintainer discussion that frequently names the file or the fix.

None of these are available to a developer looking at a freshly filed issue, so
none of them may reach the agent path.  ``test_patch`` and ``hints_text`` are
already present in the metadata that ``eval/benchmarks/swebench.py`` builds —
nothing reads them today, which is exactly the situation in which someone later
adds a "small improvement" that does.  This module exists so that the boundary
is a named, testable object rather than a habit.

What a legitimate optimisation may read: the issue text, and the repository at
``base_commit`` — including the tests that already existed at that commit, which
any developer would also have.
"""

from __future__ import annotations

from typing import Any, Mapping

#: Instance fields that leak the answer.  Names, not values — this tuple is
#: itself asserted against the source of the optimisation modules.
ANSWER_FIELDS: tuple[str, ...] = (
    "test_patch",
    "patch",
    "gold_patch",
    "FAIL_TO_PASS",
    "PASS_TO_PASS",
    "hints_text",
    "hints_to_generated_patch",
)

#: Metadata keys the optimisation path legitimately needs.
ALLOWED_METADATA_KEYS: frozenset[str] = frozenset(
    {
        "repo",
        "base_commit",
        "version",
        "synthetic",
        "environment_setup_commit",
    }
)


class AnswerLeakageError(RuntimeError):
    """Raised when an answer field reaches code that must not see it."""


def sanitise_metadata(metadata: Mapping[str, Any]) -> dict[str, Any]:
    """Return *metadata* with every answer field removed.

    Allow-list rather than deny-list: a field added to the dataset loader in
    future is excluded by default instead of silently flowing through.  The cost
    of that choice is that a genuinely needed new field must be added to
    :data:`ALLOWED_METADATA_KEYS` on purpose, which is the point.
    """
    return {
        key: value
        for key, value in metadata.items()
        if key in ALLOWED_METADATA_KEYS
    }


def assert_no_answer_fields(metadata: Mapping[str, Any], *, where: str) -> None:
    """Raise if *metadata* still carries an answer field.

    Called at the entry of each optimisation stage so a leak fails loudly at the
    boundary it crossed, rather than quietly improving the score.
    """
    present = [field for field in ANSWER_FIELDS if metadata.get(field)]
    if present:
        raise AnswerLeakageError(
            f"{where} received answer field(s) {present}. These are not "
            "available to a developer reading a fresh issue, so a score "
            "obtained with them is not a score. Pass metadata through "
            "sanitise_metadata() first."
        )
