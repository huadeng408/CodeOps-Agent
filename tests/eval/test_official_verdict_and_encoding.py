"""An evaluation error is not a verdict, and a diff is not locale text.

Both defects here were found by the first H5 run in which the agent produced a
real patch (``eval_results/h5-smoke-20260810-123749``): the agent emitted the
correct 506-byte fix for ``astropy__astropy-12907``, and the official report
recorded ``error_instances: 1`` / ``error_ids: [astropy__astropy-12907]`` with
``unresolved_instances: 0``.

**I — an errored instance was reported as ``resolved=False``.**
``_read_official_resolution`` collapsed three different outcomes into one
boolean::

    for key in ("unresolved_ids", "error_ids", "empty_patch_ids"):
        if instance_id in summary.get(key, []):
            return False

``unresolved_ids`` means the tests ran and the patch did not fix the bug — a
real measurement.  ``error_ids`` means the harness crashed before any test ran,
so there is no verdict at all.  Reporting the second as ``resolved=False``
states that the agent's patch failed when the truth is that the evaluation
never happened: the same conflation as the scorer-availability defect, one
layer deeper.

**H — the diff was decoded with the locale codec.**  ``_capture_git_diff``
passed ``text=True`` with no ``encoding``, so on a zh-CN Windows the UTF-8 diff
was decoded as gbk.  Byte ``0x93`` raised ``UnicodeDecodeError`` in the
subprocess reader thread, and the bare ``except`` returned ``""`` — an
encoding bug that is indistinguishable from an agent that produced no patch.
"""

from __future__ import annotations

import json
import subprocess
from pathlib import Path

import pytest

from eval.benchmarks import swebench as mod


# ---------------------------------------------------------------------------
# I: error_ids carries no verdict
# ---------------------------------------------------------------------------


def _write_summary(tmp_path: Path, run_id: str, model: str, **buckets) -> Path:
    payload = {
        "resolved_ids": [],
        "unresolved_ids": [],
        "error_ids": [],
        "empty_patch_ids": [],
    }
    payload.update(buckets)
    path = tmp_path / f"{model}.{run_id}.json"
    path.write_text(json.dumps(payload), encoding="utf-8")
    return path


def test_errored_instance_yields_no_verdict(tmp_path: Path, monkeypatch) -> None:
    """An instance in error_ids must not read as an honest resolved=False."""
    monkeypatch.chdir(tmp_path)
    _write_summary(tmp_path, "run-1", "m", error_ids=["inst-1"])

    with pytest.raises(mod.OfficialScorerUnavailable) as exc:
        mod._read_official_resolution(
            instance_id="inst-1", run_id="run-1", model_name="m"
        )
    assert "error" in str(exc.value).lower()


def test_unresolved_instance_is_a_real_false(tmp_path: Path, monkeypatch) -> None:
    """Tests ran and the patch did not fix it: that IS a measurement."""
    monkeypatch.chdir(tmp_path)
    _write_summary(tmp_path, "run-1", "m", unresolved_ids=["inst-1"])
    assert (
        mod._read_official_resolution(
            instance_id="inst-1", run_id="run-1", model_name="m"
        )
        is False
    )


def test_resolved_instance_is_true(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.chdir(tmp_path)
    _write_summary(tmp_path, "run-1", "m", resolved_ids=["inst-1"])
    assert (
        mod._read_official_resolution(
            instance_id="inst-1", run_id="run-1", model_name="m"
        )
        is True
    )


def test_empty_patch_instance_is_a_real_false(tmp_path: Path, monkeypatch) -> None:
    """An empty patch is the agent's own miss, so False is honest here."""
    monkeypatch.chdir(tmp_path)
    _write_summary(tmp_path, "run-1", "m", empty_patch_ids=["inst-1"])
    assert (
        mod._read_official_resolution(
            instance_id="inst-1", run_id="run-1", model_name="m"
        )
        is False
    )


def test_absent_instance_returns_none(tmp_path: Path, monkeypatch) -> None:
    monkeypatch.chdir(tmp_path)
    _write_summary(tmp_path, "run-1", "m")
    assert (
        mod._read_official_resolution(
            instance_id="inst-1", run_id="run-1", model_name="m"
        )
        is None
    )


def test_score_surfaces_evaluation_error_as_scorer_failure(
    tmp_path: Path, monkeypatch
) -> None:
    """End-to-end: a build failure must reach the harness as ERROR_SCORER."""
    from eval.adapter import EvalInstance, EvalResult

    monkeypatch.setattr(mod, "_can_score_official", lambda: (True, "available"))
    monkeypatch.setattr(mod, "_run_official_scoring", lambda *a, **k: (True, "done"))
    monkeypatch.chdir(tmp_path)
    _write_summary(
        tmp_path, "swebench-inst-1", "code-agent-default", error_ids=["inst-1"]
    )

    adapter = mod.SWEBenchAdapter()
    with pytest.raises(mod.OfficialScorerUnavailable):
        adapter.score(
            EvalResult(instance_id="inst-1", model_patch="diff --git a/x b/x\n"),
            EvalInstance(instance_id="inst-1", task_description="t", metadata={}),
            tmp_path,
        )


# ---------------------------------------------------------------------------
# H: the diff must be decoded as UTF-8, whatever the locale is
# ---------------------------------------------------------------------------


def test_capture_git_diff_decodes_utf8_not_locale(tmp_path: Path, monkeypatch) -> None:
    """A diff carrying non-gbk bytes must survive on a zh-CN Windows."""
    captured: dict = {}
    # 0x93 is a Windows-1252 smart quote: invalid gbk, valid inside UTF-8 text.
    payload = "diff --git a/x b/x\n+ “smart quotes” and — dashes\n"

    class FakeCompleted:
        returncode = 0
        stdout = payload
        stderr = ""

    def fake_run(cmd, **kwargs):
        captured.update(kwargs)
        # Prove the call cannot fall back to the locale codec.
        assert kwargs.get("encoding") == "utf-8", (
            "text mode without an explicit encoding decodes git output with the "
            "locale codec (gbk here) and raises UnicodeDecodeError"
        )
        return FakeCompleted()

    monkeypatch.setattr(mod.subprocess, "run", fake_run)
    out = mod._capture_git_diff(str(tmp_path))

    assert "smart quotes" in out
    assert captured.get("errors") == "replace", (
        "an undecodable byte must degrade one character, never lose the patch"
    )


def test_capture_git_diff_source_pins_explicit_encoding() -> None:
    import inspect

    source = inspect.getsource(mod._capture_git_diff)
    assert 'encoding="utf-8"' in source
    assert 'errors="replace"' in source


def test_capture_git_diff_on_a_real_repo_with_non_ascii(tmp_path: Path) -> None:
    """Integration: a real git diff containing CJK and smart quotes survives."""
    subprocess.run(["git", "init", "-q", str(tmp_path)], check=True)
    subprocess.run(
        ["git", "-C", str(tmp_path), "config", "user.email", "t@t"], check=True
    )
    subprocess.run(["git", "-C", str(tmp_path), "config", "user.name", "t"], check=True)
    target = tmp_path / "f.txt"
    target.write_text("base\n", encoding="utf-8")
    subprocess.run(["git", "-C", str(tmp_path), "add", "-A"], check=True)
    subprocess.run(
        ["git", "-C", str(tmp_path), "commit", "-qm", "init"], check=True
    )

    target.write_text("base\n中文 “smart” — dash\n", encoding="utf-8")
    diff = mod._capture_git_diff(str(tmp_path))

    assert diff.strip(), "a non-ASCII diff must not come back empty"
    assert "中文" in diff
