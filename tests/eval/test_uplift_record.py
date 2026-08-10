"""Tests for the localization record that ``augment_with_record`` returns.

This record is the only artifact that can distinguish "the score went up" from
"the score went up because the agent was pointed at the right file", so these
tests hold it to two properties beyond its contents: it must survive
``json.dumps`` (the runner serialises it while recording the instance, and an
exception there would fail the instance for a reporting concern), and an absent
ranking must stay distinguishable from a ranking that missed.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.harness.uplift import (
    UPLIFT_ENV,
    augment_task_description,
    augment_with_record,
    uplift_config,
)


TASK = "Separability matrix is wrong for nested CompoundModels"


@pytest.fixture()
def repo(tmp_path: Path) -> Path:
    pkg = tmp_path / "pkg"
    pkg.mkdir()
    (pkg / "__init__.py").write_text("", encoding="utf-8")
    (pkg / "separable.py").write_text(
        '"""Separability matrix computation for compound models."""\n'
        "\n"
        "def separability_matrix(transform):\n"
        '    """Compute the separability matrix of a CompoundModel."""\n'
        "    return _coord_matrix(transform)\n"
        "\n"
        "def _coord_matrix(model):\n"
        "    return None\n",
        encoding="utf-8",
    )
    (pkg / "unrelated.py").write_text(
        '"""Date parsing helpers."""\n\ndef parse_date(text):\n    return text\n',
        encoding="utf-8",
    )
    return tmp_path


@pytest.fixture()
def uplift_on(monkeypatch):
    monkeypatch.setenv(UPLIFT_ENV, "1")
    return uplift_config()


# ------------------------------------------------------------------- off state


def test_record_is_empty_when_uplift_is_off(repo: Path, monkeypatch):
    monkeypatch.delenv(UPLIFT_ENV, raising=False)
    text, record = augment_with_record(TASK, str(repo))
    assert text == TASK
    assert record == {}


def test_baseline_prompt_is_identical_not_merely_equivalent(repo: Path, monkeypatch):
    monkeypatch.delenv(UPLIFT_ENV, raising=False)
    text, _ = augment_with_record(TASK, str(repo))
    assert text == TASK


def test_record_is_empty_when_localization_is_ablated(repo: Path, monkeypatch):
    monkeypatch.setenv(UPLIFT_ENV, "1")
    monkeypatch.setenv("SWEBENCH_UPLIFT_LOCALIZATION", "0")
    text, record = augment_with_record(TASK, str(repo))
    assert record == {}
    # The edit mandate is a separate component and must still be applied.
    assert text != TASK


# ---------------------------------------------------------------- record shape


def test_record_lists_the_ranked_files(repo: Path, uplift_on):
    _, record = augment_with_record(TASK, str(repo), config=uplift_on)
    assert "pkg/separable.py" in record["files"]


def test_ranking_puts_the_relevant_file_first(repo: Path, uplift_on):
    _, record = augment_with_record(TASK, str(repo), config=uplift_on)
    assert record["files"][0] == "pkg/separable.py"


def test_record_carries_scores_for_every_ranked_file(repo: Path, uplift_on):
    _, record = augment_with_record(TASK, str(repo), config=uplift_on)
    assert set(record["file_scores"]) == set(record["files"])
    assert all(isinstance(v, float) for v in record["file_scores"].values())


def test_function_sites_carry_line_spans(repo: Path, uplift_on):
    _, record = augment_with_record(TASK, str(repo), config=uplift_on)
    assert record["functions"], "expected at least one function site"
    site = record["functions"][0]
    assert set(site) == {"path", "qualname", "lineno", "end_lineno", "score"}
    assert site["lineno"] >= 1
    assert site["end_lineno"] >= site["lineno"]


def test_record_reports_how_many_files_were_scanned(repo: Path, uplift_on):
    _, record = augment_with_record(TASK, str(repo), config=uplift_on)
    assert record["scanned_files"] >= 2


def test_healthy_record_has_no_degraded_reason(repo: Path, uplift_on):
    _, record = augment_with_record(TASK, str(repo), config=uplift_on)
    # The key is always present so a reader can tell "no reason" from "no record";
    # its empty value is the sentinel LocalizationResult uses.
    assert "degraded_reason" in record
    assert not record["degraded_reason"]


# ------------------------------------------------------------- serialisability


def test_record_survives_json_round_trip(repo: Path, uplift_on):
    """The runner json.dumps() this while recording the instance."""
    _, record = augment_with_record(TASK, str(repo), config=uplift_on)
    restored = json.loads(json.dumps({"localization": record}, ensure_ascii=False))
    assert restored["localization"]["files"] == record["files"]


def test_record_contains_only_primitives(repo: Path, uplift_on):
    _, record = augment_with_record(TASK, str(repo), config=uplift_on)

    def check(value):
        assert isinstance(value, (str, int, float, bool, type(None), list, dict))
        if isinstance(value, dict):
            for key, item in value.items():
                assert isinstance(key, str)
                check(item)
        elif isinstance(value, list):
            for item in value:
                check(item)

    check(record)


# ------------------------------------------------------------------- degrading


def test_missing_repo_degrades_instead_of_raising(tmp_path: Path, uplift_on):
    missing = tmp_path / "not-a-repo"
    text, record = augment_with_record(TASK, str(missing), config=uplift_on)
    assert isinstance(text, str) and text
    assert record.get("degraded_reason")


def test_degraded_record_is_distinguishable_from_no_record(tmp_path: Path, uplift_on):
    """An empty record means "no ranking"; a degraded one means "tried, failed"."""
    _, degraded = augment_with_record(TASK, str(tmp_path / "gone"), config=uplift_on)
    assert degraded != {}
    assert degraded.get("degraded_reason")


def test_repo_with_no_python_still_returns_a_usable_task(tmp_path: Path, uplift_on):
    (tmp_path / "README.md").write_text("no code here", encoding="utf-8")
    text, record = augment_with_record(TASK, str(tmp_path), config=uplift_on)
    assert TASK in text
    assert record.get("files") == [] or record.get("degraded_reason")


def test_unparseable_source_does_not_break_the_record(tmp_path: Path, uplift_on):
    (tmp_path / "broken.py").write_text("def (((: syntax error", encoding="utf-8")
    (tmp_path / "ok.py").write_text(
        '"""Separability matrix."""\n\ndef separability_matrix(t):\n    return t\n',
        encoding="utf-8",
    )
    _, record = augment_with_record(TASK, str(tmp_path), config=uplift_on)
    assert "ok.py" in record["files"]


# -------------------------------------------------------------- wrapper parity


def test_wrapper_returns_the_same_text_as_the_record_version(repo: Path, uplift_on):
    text_a = augment_task_description(TASK, str(repo), config=uplift_on)
    text_b, _ = augment_with_record(TASK, str(repo), config=uplift_on)
    assert text_a == text_b


def test_augmented_task_still_contains_the_original_problem(repo: Path, uplift_on):
    text, _ = augment_with_record(TASK, str(repo), config=uplift_on)
    assert TASK in text


def test_record_does_not_leak_into_the_prompt_as_json(repo: Path, uplift_on):
    """The agent reads prose candidates, not a serialised record."""
    text, record = augment_with_record(TASK, str(repo), config=uplift_on)
    assert json.dumps(record["file_scores"]) not in text
