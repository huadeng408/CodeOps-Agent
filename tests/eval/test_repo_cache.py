"""Repo mirror cache: correctness first, speed second.

The cache exists to make an N-instance run affordable, but a cache that changes
what the workspace *is* would corrupt every measurement taken through it.  So
these tests check the workspace is a standalone repository at the right commit
before they check anything about reuse, and they check that every failure mode
degrades to a plain clone rather than failing the run.

Everything runs against real local git repositories over ``file://`` — no
network, no GitHub.  A mocked git would prove nothing here, because the whole
question is what git actually does with ``--reference --dissociate``.
"""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

import pytest

from eval.harness import repo_cache
from eval.harness.repo_cache import (
    CloneOutcome,
    cache_root,
    clone_at_commit,
    ensure_mirror,
)


def _git(*argv: str, cwd: str | Path | None = None) -> str:
    result = subprocess.run(
        ["git", *argv],
        cwd=str(cwd) if cwd else None,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        check=True,
    )
    return result.stdout.strip()


@pytest.fixture()
def origin_repo(tmp_path: Path) -> tuple[Path, str, str]:
    """A real local repo with two commits; returns (path, first_sha, second_sha)."""
    origin = tmp_path / "origin"
    origin.mkdir()
    _git("init", "-q", "-b", "main", cwd=origin)
    _git("config", "user.email", "t@example.com", cwd=origin)
    _git("config", "user.name", "Test", cwd=origin)

    (origin / "alpha.py").write_text("VALUE = 1\n", encoding="utf-8")
    _git("add", "-A", cwd=origin)
    _git("commit", "-q", "-m", "first", cwd=origin)
    first = _git("rev-parse", "HEAD", cwd=origin)

    (origin / "beta.py").write_text("VALUE = 2\n", encoding="utf-8")
    _git("add", "-A", cwd=origin)
    _git("commit", "-q", "-m", "second", cwd=origin)
    second = _git("rev-parse", "HEAD", cwd=origin)

    return origin, first, second


@pytest.fixture()
def local_github(monkeypatch, origin_repo):
    """Redirect ``https://github.com/<repo>.git`` to the local origin.

    The module builds the URL itself, which is correct for production but means
    a test must intercept it.  Rewriting argv in ``_run`` keeps the real git
    invocation intact — flags, ``--reference``, exit codes — while pointing it
    at a local path.
    """
    origin, _, _ = origin_repo
    real_run = repo_cache._run

    def rewriting_run(argv, **kwargs):
        argv = [
            str(origin) if a == "https://github.com/owner/name.git" else a
            for a in argv
        ]
        return real_run(argv, **kwargs)

    monkeypatch.setattr(repo_cache, "_run", rewriting_run)
    return origin


# ---------------------------------------------------------------------------
# the workspace must be indistinguishable from a full clone
# ---------------------------------------------------------------------------


def test_workspace_is_at_the_requested_commit(tmp_path, local_github, origin_repo):
    _, first, _ = origin_repo
    ws = tmp_path / "ws"
    outcome = clone_at_commit(
        "owner/name", first, ws, cache_dir=tmp_path / "cache"
    )
    assert outcome.used_mirror, outcome.fallback_reason
    assert _git("rev-parse", "HEAD", cwd=ws) == first
    # The second commit's file must NOT be present at the first commit.
    assert (ws / "alpha.py").is_file()
    assert not (ws / "beta.py").exists()


def test_workspace_survives_deleting_the_mirror(tmp_path, local_github, origin_repo):
    """``--dissociate`` must make the workspace standalone.

    If objects were merely borrowed, archiving the artifact and deleting the
    cache would silently corrupt history — and ``git diff HEAD``, which is how
    the patch is captured, would stop working.
    """
    import shutil

    _, first, _ = origin_repo
    cache = tmp_path / "cache"
    ws = tmp_path / "ws"
    clone_at_commit("owner/name", first, ws, cache_dir=cache)
    shutil.rmtree(cache)
    # Still a working repository with intact history.
    assert _git("rev-parse", "HEAD", cwd=ws) == first
    assert _git("log", "--oneline", cwd=ws)
    assert _git("status", "--porcelain", cwd=ws) == ""


def test_git_diff_head_works_in_the_workspace(tmp_path, local_github, origin_repo):
    """The patch capture path must behave exactly as with a full clone."""
    _, first, _ = origin_repo
    ws = tmp_path / "ws"
    clone_at_commit("owner/name", first, ws, cache_dir=tmp_path / "cache")
    (ws / "alpha.py").write_text("VALUE = 99\n", encoding="utf-8")
    diff = _git("diff", "HEAD", cwd=ws)
    assert "VALUE = 99" in diff


# ---------------------------------------------------------------------------
# reuse
# ---------------------------------------------------------------------------


def test_mirror_is_created_once_then_reused(tmp_path, local_github, origin_repo):
    _, first, second = origin_repo
    cache = tmp_path / "cache"
    first_outcome = clone_at_commit(
        "owner/name", first, tmp_path / "ws1", cache_dir=cache
    )
    second_outcome = clone_at_commit(
        "owner/name", second, tmp_path / "ws2", cache_dir=cache
    )
    assert first_outcome.mirror_created is True
    assert second_outcome.mirror_created is False, "mirror must be reused"
    assert second_outcome.used_mirror is True


def test_two_workspaces_can_sit_at_different_commits(
    tmp_path, local_github, origin_repo
):
    """Per-instance isolation: sharing a mirror must not couple checkouts."""
    _, first, second = origin_repo
    cache = tmp_path / "cache"
    ws1, ws2 = tmp_path / "ws1", tmp_path / "ws2"
    clone_at_commit("owner/name", first, ws1, cache_dir=cache)
    clone_at_commit("owner/name", second, ws2, cache_dir=cache)
    assert _git("rev-parse", "HEAD", cwd=ws1) == first
    assert _git("rev-parse", "HEAD", cwd=ws2) == second


def test_ensure_mirror_is_idempotent(tmp_path, local_github):
    cache = tmp_path / "cache"
    m1, created1, err1 = ensure_mirror("owner/name", cache_dir=cache)
    m2, created2, err2 = ensure_mirror("owner/name", cache_dir=cache)
    assert err1 == "" and err2 == ""
    assert m1 == m2
    assert created1 is True and created2 is False


# ---------------------------------------------------------------------------
# every failure must degrade, never fail the run
# ---------------------------------------------------------------------------


def test_unusable_cache_dir_falls_back_to_a_working_clone(
    tmp_path, local_github, origin_repo, monkeypatch
):
    """A cache that cannot be created must not cost the run its result."""
    _, first, _ = origin_repo

    def broken_mirror(*_args, **_kwargs):
        return None, False, "simulated cache failure"

    monkeypatch.setattr(repo_cache, "ensure_mirror", broken_mirror)
    ws = tmp_path / "ws"
    outcome = clone_at_commit("owner/name", first, ws, cache_dir=tmp_path / "cache")
    assert outcome.degraded is True
    assert "simulated cache failure" in outcome.fallback_reason
    # The point of degrading: the workspace is still correct.
    assert _git("rev-parse", "HEAD", cwd=ws) == first


def test_use_cache_false_still_produces_a_correct_workspace(
    tmp_path, local_github, origin_repo
):
    _, first, _ = origin_repo
    ws = tmp_path / "ws"
    outcome = clone_at_commit(
        "owner/name", first, ws, cache_dir=tmp_path / "cache", use_cache=False
    )
    assert outcome.used_mirror is False
    assert _git("rev-parse", "HEAD", cwd=ws) == first


def test_unreachable_commit_raises_rather_than_leaving_an_empty_workspace(
    tmp_path, local_github
):
    """An empty workspace is what made every captured patch empty before.

    So a genuinely impossible checkout must raise, not return quietly.
    """
    with pytest.raises(RuntimeError):
        clone_at_commit(
            "owner/name",
            "0" * 40,
            tmp_path / "ws",
            cache_dir=tmp_path / "cache",
        )


def test_partial_mirror_is_not_published(tmp_path, local_github, monkeypatch):
    """A killed mirror clone must not leave something later runs trust."""
    cache = tmp_path / "cache"
    real_run = repo_cache._run

    def failing_mirror_clone(argv, **kwargs):
        if "--mirror" in argv:
            return subprocess.CompletedProcess(argv, 1, "", "simulated interrupt")
        return real_run(argv, **kwargs)

    monkeypatch.setattr(repo_cache, "_run", failing_mirror_clone)
    mirror, created, reason = ensure_mirror("owner/name", cache_dir=cache)
    assert mirror is None
    assert created is False
    assert reason
    published = list(cache.glob("*.git"))
    assert published == [], f"a failed mirror was published: {published}"


# ---------------------------------------------------------------------------
# configuration
# ---------------------------------------------------------------------------


def test_cache_root_prefers_explicit_then_env_then_default(tmp_path, monkeypatch):
    explicit = tmp_path / "explicit"
    assert cache_root(explicit) == explicit

    monkeypatch.setenv(repo_cache.DEFAULT_CACHE_ENV, str(tmp_path / "from-env"))
    assert cache_root() == tmp_path / "from-env"

    monkeypatch.delenv(repo_cache.DEFAULT_CACHE_ENV, raising=False)
    assert cache_root() == repo_cache.DEFAULT_CACHE_DIR


def test_default_cache_is_outside_the_repository():
    """A ``git clean`` in the project tree must not delete gigabytes of mirrors."""
    repo_root = Path(__file__).resolve().parents[2]
    assert repo_root not in repo_cache.DEFAULT_CACHE_DIR.parents
    assert repo_cache.DEFAULT_CACHE_DIR != repo_root


def test_mirror_dir_is_flat_and_unambiguous(tmp_path):
    path = repo_cache._mirror_dir(tmp_path, "astropy/astropy")
    assert path.parent == tmp_path, "cache must stay one flat level"
    assert path.name == "astropy__astropy.git"


def test_outcome_reports_degradation_explicitly():
    assert CloneOutcome("ws", used_mirror=True, mirror_created=False).degraded is False
    assert CloneOutcome("ws", used_mirror=False, mirror_created=False).degraded is True
