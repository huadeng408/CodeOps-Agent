"""Tests for candidate selection.

The load-bearing test here is determinism under shuffling: the published
regression at sampling budget 8 was attributed partly to random tie-breaking, so
"same candidates, same winner, regardless of order" is the property that makes
best-of-N safe to turn on.
"""

from __future__ import annotations

import itertools
import random
from pathlib import Path

import pytest

from eval.harness.select import Candidate, rank, select_best, selection_record


def _diff(path: str, added_lines: int = 1) -> str:
    body = "".join(f"+    line_{i}\n" for i in range(added_lines))
    return (
        f"diff --git a/{path} b/{path}\n"
        f"--- a/{path}\n+++ b/{path}\n@@ -1,1 +1,{1 + added_lines} @@\n"
        " def f():\n" + body
    )


ONE_FILE = _diff("pkg/a.py")
ONE_FILE_BIG = _diff("pkg/a.py", added_lines=40)
TWO_FILES = _diff("pkg/a.py") + _diff("pkg/b.py")
TESTS_ONLY = _diff("pkg/tests/test_a.py")


@pytest.fixture()
def repo(tmp_path: Path) -> Path:
    (tmp_path / "pkg").mkdir()
    (tmp_path / "pkg" / "a.py").write_text("def f():\n    return 1\n", encoding="utf-8")
    (tmp_path / "pkg" / "b.py").write_text("def g():\n    return 2\n", encoding="utf-8")
    return tmp_path


# ------------------------------------------------------------------ basic order


def test_empty_candidate_list_has_no_winner(repo: Path):
    winner, ranked = select_best([], repo)
    assert winner is None
    assert ranked == []


def test_single_valid_candidate_wins(repo: Path):
    winner, ranked = select_best([ONE_FILE], repo)
    assert winner is not None
    assert winner.diff == ONE_FILE
    assert len(ranked) == 1


def test_empty_patch_never_wins(repo: Path):
    winner, _ = select_best([""], repo)
    assert winner is None, "an empty patch is a known failure, not a submission"


def test_valid_beats_empty(repo: Path):
    winner, ranked = select_best(["", ONE_FILE, ""], repo)
    assert winner is not None
    assert winner.diff == ONE_FILE
    assert ranked[0].diff == ONE_FILE
    assert all(c.disqualified for c in ranked[1:])


def test_tests_only_patch_never_wins(repo: Path):
    winner, _ = select_best([TESTS_ONLY], repo)
    assert winner is None


def test_valid_beats_tests_only(repo: Path):
    winner, _ = select_best([TESTS_ONLY, ONE_FILE], repo)
    assert winner is not None and winner.diff == ONE_FILE


def test_all_disqualified_returns_none_but_keeps_the_ranking(repo: Path):
    winner, ranked = select_best(["", TESTS_ONLY], repo)
    assert winner is None
    assert len(ranked) == 2


# -------------------------------------------------------------- heuristic rules


def test_fewer_files_preferred(repo: Path):
    winner, _ = select_best([TWO_FILES, ONE_FILE], repo)
    assert winner is not None and winner.diff == ONE_FILE


def test_smaller_diff_preferred_at_equal_file_count(repo: Path):
    winner, _ = select_best([ONE_FILE_BIG, ONE_FILE], repo)
    assert winner is not None and winner.diff == ONE_FILE


def test_ranking_never_rewards_touching_more_files(repo: Path):
    """Pins the trap the first ordering fell into.

    An earlier rule 2 preferred candidates with *more* weak-positive checks.
    check_syntax emits one per patched Python file, so a five-file patch
    outranked a one-file patch on the rule meant to express the opposite. Any
    future criterion that scales with surface area breaks this test.
    """
    winner, ranked = select_best([TWO_FILES, ONE_FILE], repo)
    assert winner is not None and winner.file_count == 1
    assert ranked[0].file_count <= ranked[1].file_count
    # And the corroboration count really is higher for the bigger patch, so the
    # test would pass vacuously if it did not assert the ordering above.
    two = next(c for c in ranked if c.file_count == 2)
    one = next(c for c in ranked if c.file_count == 1)
    assert two.weak_positives > one.weak_positives


# ------------------------------------------------------------------ determinism


def test_winner_is_independent_of_input_order(repo: Path):
    diffs = [ONE_FILE, TWO_FILES, "", ONE_FILE_BIG, TESTS_ONLY]
    winners = set()
    for permutation in itertools.permutations(diffs):
        winner, _ = select_best(list(permutation), repo)
        winners.add(winner.digest if winner else None)
    assert len(winners) == 1, "the winner must not depend on arrival order"


def test_identical_candidates_break_ties_deterministically(repo: Path):
    """Two distinct diffs of identical shape must still order stably."""
    a = _diff("pkg/a.py")
    b = _diff("pkg/b.py")
    first = [c.digest for c in rank(_candidates([a, b], repo))]
    for _ in range(20):
        shuffled = [a, b]
        random.shuffle(shuffled)
        assert [c.digest for c in rank(_candidates(shuffled, repo))] == first


def test_ranking_is_a_total_order(repo: Path):
    diffs = [ONE_FILE, TWO_FILES, ONE_FILE_BIG, "", TESTS_ONLY]
    ranked = rank(_candidates(diffs, repo))
    assert len(ranked) == len(diffs)
    assert len({c.digest for c in ranked}) == len({d for d in diffs})


def _candidates(diffs, repo):
    from eval.harness.validate import validate_patch

    out = []
    for index, diff in enumerate(diffs):
        candidate = Candidate(diff=diff or "", label=f"s{index}")
        candidate.report = validate_patch(candidate.diff, repo)
        out.append(candidate)
    return out


# --------------------------------------------------------------- candidate math


def test_digest_is_stable_and_content_addressed():
    assert Candidate(diff="x").digest == Candidate(diff="x").digest
    assert Candidate(diff="x").digest != Candidate(diff="y").digest


def test_file_count_and_size():
    candidate = Candidate(diff=TWO_FILES)
    assert candidate.file_count == 2
    assert candidate.size == len(TWO_FILES)


def test_candidate_without_a_report_is_not_disqualified():
    assert Candidate(diff=ONE_FILE).disqualified is False
    assert Candidate(diff=ONE_FILE).weak_positives == 0


# ------------------------------------------------------------------- the record


def test_selection_record_names_the_winner_and_the_tie_break(repo: Path):
    winner, ranked = select_best([ONE_FILE, TWO_FILES], repo, labels=["t0", "t1"])
    record = selection_record(winner, ranked)
    assert record["winner"] == "t0"
    assert record["candidates"] == 2
    assert "never random" in record["tie_break"]
    assert len(record["ranking"]) == 2


def test_selection_record_marks_the_all_disqualified_case(repo: Path):
    winner, ranked = select_best(["", ""], repo)
    record = selection_record(winner, ranked)
    assert record["winner"] is None
    assert record["all_disqualified"] is True


def test_selection_record_carries_temperatures(repo: Path):
    winner, ranked = select_best(
        [ONE_FILE, TWO_FILES], repo, temperatures=[0.0, 0.8]
    )
    record = selection_record(winner, ranked)
    temps = {row["temperature"] for row in record["ranking"]}
    assert temps == {0.0, 0.8}


# ------------------------------------------------------- anti-leakage guardrail


def test_select_source_never_mentions_the_grading_criteria():
    import inspect

    from eval.harness import select as S

    source = inspect.getsource(S)
    body = source.split('"""', 2)[-1]
    for field in ("test_patch", "FAIL_TO_PASS", "PASS_TO_PASS", "hints_text", "gold_patch"):
        assert field not in body
