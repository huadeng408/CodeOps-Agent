from __future__ import annotations

import subprocess

from orchestrator.context import load_git_diff_snapshot


def test_load_git_diff_snapshot_reports_status_and_changed_files(tmp_path) -> None:
    subprocess.run(["git", "init"], cwd=tmp_path, check=True, capture_output=True)
    subprocess.run(
        ["git", "config", "user.email", "agent@example.test"],
        cwd=tmp_path,
        check=True,
        capture_output=True,
    )
    subprocess.run(
        ["git", "config", "user.name", "Agent Test"],
        cwd=tmp_path,
        check=True,
        capture_output=True,
    )
    (tmp_path / "tracked.txt").write_text("before\n", encoding="utf-8")
    subprocess.run(["git", "add", "tracked.txt"], cwd=tmp_path, check=True, capture_output=True)
    subprocess.run(["git", "commit", "-m", "initial"], cwd=tmp_path, check=True, capture_output=True)

    (tmp_path / "tracked.txt").write_text("after\n", encoding="utf-8")
    (tmp_path / "new.txt").write_text("new\n", encoding="utf-8")

    snapshot = load_git_diff_snapshot(tmp_path, tmp_path)

    assert "tracked.txt" in snapshot.status
    assert "new.txt" in snapshot.status
    assert "tracked.txt" in snapshot.changed_files
    assert "tracked.txt" in snapshot.diff_stat
