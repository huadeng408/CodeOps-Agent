from __future__ import annotations

import os
import subprocess
import sys
from pathlib import Path

import pytest


REPO_ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture
def repository(tmp_path: Path) -> tuple[Path, str]:
    subprocess.run(["git", "init", "-q", str(tmp_path)], check=True)
    for key, value in (("user.name", "CI Test"), ("user.email", "ci@example.invalid")):
        subprocess.run(["git", "-C", str(tmp_path), "config", key, value], check=True)
    (tmp_path / "tracked.txt").write_text("baseline\n", encoding="utf-8")
    return tmp_path, _commit(tmp_path)


def _commit(repo: Path) -> str:
    subprocess.run(["git", "-C", str(repo), "add", "."], check=True)
    subprocess.run(["git", "-C", str(repo), "commit", "-qm", "test input"], check=True)
    return subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()


def _scope(repo: Path, base: str, head: str) -> str:
    env = {**os.environ, "CI_BASE_SHA": base, "CI_HEAD_SHA": head, "PYTHONPATH": str(REPO_ROOT)}
    result = subprocess.run(
        [sys.executable, "-B", "-m", "scripts.ci_scope"], cwd=repo, env=env,
        text=True, capture_output=True, check=True,
    )
    assert not result.stderr
    return result.stdout.strip()


@pytest.mark.parametrize(("path", "expected"), [
    ("README.md", "full=false"),
    ("docs/usage.md", "full=false"),
    ("AGENTS.md", "full=true"),
    ("docs/GOAL.md", "full=true"),
    ("docs/agents/rules.md", "full=true"),
    ("frontend/src/App.tsx", "full=true"),
    ("data/eval/workflow/receipts/new.json", "full=true"),
    ("unknown/new.file", "full=true"),
])
def test_ci_scope_classifies_the_actual_git_diff(repository: tuple[Path, str], path: str, expected: str) -> None:
    repo, base = repository
    target = repo / path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text("changed\n", encoding="utf-8")
    assert _scope(repo, base, _commit(repo)) == expected


@pytest.mark.parametrize("base", ["", "0" * 40, "b" * 40])
def test_ci_scope_defaults_to_full_without_confirmed_history(repository: tuple[Path, str], base: str) -> None:
    repo, head = repository
    assert _scope(repo, base, head) == "full=true"


def test_ci_scope_keeps_full_checks_for_an_instruction_link_target(repository: tuple[Path, str]) -> None:
    repo, _ = repository
    subprocess.run(["git", "-C", str(repo), "config", "core.symlinks", "false"], check=True)
    (repo / "README.md").write_text("original rules\n", encoding="utf-8")
    blob = subprocess.check_output(
        ["git", "-C", str(repo), "hash-object", "-w", "--stdin"], input=b"README.md",
    ).decode().strip()
    subprocess.run(["git", "-C", str(repo), "update-index", "--add", "--cacheinfo", f"120000,{blob},AGENTS.md"], check=True)
    subprocess.run(["git", "-C", str(repo), "checkout-index", "--force", "--", "AGENTS.md"], check=True)
    base = _commit(repo)
    (repo / "README.md").write_text("changed rules\n", encoding="utf-8")
    assert _scope(repo, base, _commit(repo)) == "full=true"


def test_ci_scope_does_not_exempt_executable_markdown(repository: tuple[Path, str]) -> None:
    repo, base = repository
    (repo / "README.md").write_text("executable input\n", encoding="utf-8")
    subprocess.run(["git", "-C", str(repo), "add", "README.md"], check=True)
    subprocess.run(["git", "-C", str(repo), "update-index", "--chmod=+x", "README.md"], check=True)
    subprocess.run(["git", "-C", str(repo), "commit", "-qm", "executable input"], check=True)
    head = subprocess.check_output(["git", "-C", str(repo), "rev-parse", "HEAD"], text=True).strip()
    assert _scope(repo, base, head) == "full=true"
