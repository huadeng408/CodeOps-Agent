"""Tests for hierarchical BM25 localization.

The interesting assertions here are the negative ones. A localizer is only
useful if it can be trusted to fail quietly, so most of these tests hand it
broken input and check that it returns a degraded result instead of raising.
"""

from __future__ import annotations

import inspect
from pathlib import Path

import pytest

from eval.harness import localize as loc


# ---------------------------------------------------------------- tokenisation


def test_tokenise_splits_snake_case():
    tokens = loc.tokenise("separability_matrix")
    assert "separability_matrix" in tokens
    assert "separability" in tokens
    assert "matrix" in tokens


def test_tokenise_splits_camel_case():
    tokens = loc.tokenise("CompoundModel")
    assert "compoundmodel" in tokens
    assert "compound" in tokens
    assert "model" in tokens


def test_tokenise_drops_punctuation_and_numbers_alone():
    assert loc.tokenise("a == b; 42") == ["a", "b"]


def test_tokenise_empty_text_is_empty():
    assert loc.tokenise("") == []
    assert loc.tokenise("!!! ??? ...") == []


# ------------------------------------------------------------------------ BM25


def test_bm25_ranks_the_matching_document_first():
    docs = [
        loc._Document(key="a", tokens=loc.tokenise("unrelated helper utility")),
        loc._Document(key="b", tokens=loc.tokenise("separability matrix compound model")),
    ]
    ranked = loc.BM25(docs).score(loc.tokenise("separability_matrix"))
    assert ranked
    assert ranked[0][1].key == "b"


def test_bm25_omits_documents_that_score_zero():
    docs = [
        loc._Document(key="a", tokens=loc.tokenise("nothing in common here")),
        loc._Document(key="b", tokens=loc.tokenise("separability")),
    ]
    ranked = loc.BM25(docs).score(loc.tokenise("separability"))
    assert [doc.key for _, doc in ranked] == ["b"]


def test_bm25_idf_is_never_negative_and_ranks_rare_tokens_higher():
    # The unsmoothed Okapi IDF goes negative once a token appears in more than
    # half the corpus, which would let a boilerplate token actively push a
    # relevant file down the ranking. The log(1 + ...) form used here stays
    # non-negative, and a token in every document must be worth far less than
    # one in a single document.
    docs = [
        loc._Document(key=str(i), tokens=["common", f"unique{i}"]) for i in range(5)
    ]
    bm25 = loc.BM25(docs)
    assert all(value >= 0.0 for value in bm25._idf.values())
    assert bm25._idf["common"] < bm25._idf["unique0"] / 10


def test_bm25_is_deterministic_for_tied_scores():
    docs = [
        loc._Document(key="z_file", tokens=["target"]),
        loc._Document(key="a_file", tokens=["target"]),
    ]
    first = [doc.key for _, doc in loc.BM25(docs).score(["target"])]
    second = [doc.key for _, doc in loc.BM25(docs).score(["target"])]
    assert first == second == ["a_file", "z_file"]


def test_bm25_handles_no_documents():
    assert loc.BM25([]).score(["anything"]) == []


# ---------------------------------------------------------------- localization


@pytest.fixture()
def fake_repo(tmp_path: Path) -> Path:
    (tmp_path / "pkg").mkdir()
    (tmp_path / "pkg" / "__init__.py").write_text("", encoding="utf-8")
    (tmp_path / "pkg" / "separable.py").write_text(
        '''"""Model separability utilities."""


def separability_matrix(transform):
    """Compute the correlation between outputs and inputs."""
    return _coord_matrix(transform)


def _coord_matrix(model):
    return [[1, 0], [0, 1]]
''',
        encoding="utf-8",
    )
    (tmp_path / "pkg" / "unrelated.py").write_text(
        '''"""Colour table parsing helpers."""


def parse_palette(data):
    """Read a palette from bytes."""
    return data
''',
        encoding="utf-8",
    )
    skipped = tmp_path / "build" / "pkg"
    skipped.mkdir(parents=True)
    (skipped / "separable.py").write_text("def separability_matrix(): pass\n", encoding="utf-8")
    return tmp_path


def test_localize_finds_the_relevant_file(fake_repo: Path):
    result = loc.localize(
        "separability_matrix computes the wrong value for nested CompoundModels",
        fake_repo,
    )
    assert result.files
    assert result.files[0] == "pkg/separable.py"
    assert not result.degraded_reason


def test_localize_finds_the_relevant_function(fake_repo: Path):
    result = loc.localize("separability_matrix is wrong for nested models", fake_repo)
    names = [site.qualname for site in result.functions]
    assert "separability_matrix" in names


def test_localize_function_sites_carry_usable_line_ranges(fake_repo: Path):
    result = loc.localize("separability_matrix nested models", fake_repo)
    site = next(s for s in result.functions if s.qualname == "separability_matrix")
    assert site.lineno >= 1
    assert site.end_lineno >= site.lineno
    assert site.location == f"pkg/separable.py:{site.lineno}-{site.end_lineno}"


def test_localize_skips_build_directories(fake_repo: Path):
    result = loc.localize("separability_matrix", fake_repo)
    assert all(not path.startswith("build/") for path in result.files)


def test_localize_respects_top_k_limits(fake_repo: Path):
    result = loc.localize("separability matrix palette parse", fake_repo, top_files=1, top_functions=1)
    assert len(result.files) <= 1
    assert len(result.functions) <= 1


def test_localize_is_deterministic(fake_repo: Path):
    issue = "separability_matrix wrong for nested CompoundModels"
    a = loc.localize(issue, fake_repo)
    b = loc.localize(issue, fake_repo)
    assert a.files == b.files
    assert [s.qualname for s in a.functions] == [s.qualname for s in b.functions]


# ---------------------------------------------------- degradation, never raise


def test_localize_degrades_on_missing_directory(tmp_path: Path):
    result = loc.localize("anything", tmp_path / "does-not-exist")
    assert result.empty
    assert "not a directory" in result.degraded_reason


def test_localize_degrades_on_empty_issue_text(fake_repo: Path):
    result = loc.localize("   ", fake_repo)
    assert result.empty
    assert "empty issue" in result.degraded_reason


def test_localize_degrades_when_no_python_files(tmp_path: Path):
    (tmp_path / "README.md").write_text("hello", encoding="utf-8")
    result = loc.localize("anything", tmp_path)
    assert result.empty
    assert "no python files" in result.degraded_reason


def test_localize_degrades_when_issue_has_no_tokens(fake_repo: Path):
    result = loc.localize("!!! ??? 123", fake_repo)
    assert result.empty
    assert result.degraded_reason


def test_localize_degrades_when_nothing_matches(fake_repo: Path):
    result = loc.localize("zzzqqqxxx nonexistent identifier", fake_repo)
    assert result.empty
    assert "scored above zero" in result.degraded_reason


def test_localize_survives_unparseable_python(tmp_path: Path):
    (tmp_path / "broken.py").write_text("def (((:\n", encoding="utf-8")
    (tmp_path / "good.py").write_text(
        "def separability_matrix():\n    return 1\n", encoding="utf-8"
    )
    result = loc.localize("separability_matrix", tmp_path)
    assert "good.py" in result.files


def test_localize_survives_non_utf8_bytes(tmp_path: Path):
    (tmp_path / "latin.py").write_text("", encoding="utf-8")
    (tmp_path / "latin.py").write_bytes(b"# caf\xe9\ndef separability_matrix():\n    return 1\n")
    result = loc.localize("separability_matrix", tmp_path)
    assert not result.degraded_reason


# ------------------------------------------------------------------- rendering


def test_render_block_is_empty_for_empty_result():
    assert loc.render_localization_block(loc.LocalizationResult()) == ""


def test_render_block_marks_results_as_candidates_not_a_diagnosis(fake_repo: Path):
    block = loc.render_localization_block(loc.localize("separability_matrix", fake_repo))
    assert "candidates, not a diagnosis" in block
    assert "search elsewhere" in block


def test_render_block_lists_files_and_functions(fake_repo: Path):
    block = loc.render_localization_block(loc.localize("separability_matrix", fake_repo))
    assert "pkg/separable.py" in block
    assert "separability_matrix" in block
    assert "Most likely files" in block
    assert "Most likely functions" in block


def test_render_block_includes_line_ranges(fake_repo: Path):
    block = loc.render_localization_block(loc.localize("separability_matrix", fake_repo))
    assert "lines " in block


# ------------------------------------------------------- structural anti-leakage


def test_localize_source_never_mentions_answer_fields():
    """Structural guard: the localizer must not learn to read the answers.

    Complements ``tests/eval/test_no_answer_leakage.py`` by binding this module
    specifically, since it is the one component whose whole job is to guess
    where the fix goes.
    """
    source = inspect.getsource(loc)
    body = source.split('"""', 2)[-1]  # skip the module docstring, which names them
    for field in ("test_patch", "FAIL_TO_PASS", "PASS_TO_PASS", "hints_text", "gold_patch"):
        assert field not in body, f"{field} must not be reachable from the localizer"


def test_localize_signature_accepts_only_issue_text_and_repo():
    params = list(inspect.signature(loc.localize).parameters)
    assert params[:2] == ["issue_text", "repo_root"]
