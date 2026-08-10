"""A verdict must belong to the run that asked for it.

Regression tests for the defect where ``run_id`` for the official scorer was
``f"swebench-{instance_id}"`` — stable across runs — so every run of an instance
wrote into the same ``logs/run_evaluation`` tree. A run whose agent produced a
zero-byte patch read an earlier run's ``report.json`` and recorded
``resolved=True``, while the official summary in the very same record said
``empty_patch_ids: [that instance]``.

Two independent guards are tested here, because either one alone degrades
quietly: path isolation stops protecting anything if someone reintroduces a
stable run_id, and the freshness check stops protecting anything if the clock or
the report layout changes.
"""

from __future__ import annotations

import json
import time
from pathlib import Path

from eval.benchmarks import swebench


INSTANCE = "astropy__astropy-12907"
MODEL = "code-agent-default"


def _write_report(
    root: Path, run_id: str, instance_id: str, *, resolved: bool, mtime: float | None
) -> Path:
    path = root / "logs" / "run_evaluation" / run_id / MODEL / instance_id
    path.mkdir(parents=True, exist_ok=True)
    report = path / "report.json"
    report.write_text(
        json.dumps({instance_id: {"resolved": resolved, "patch_exists": True}}),
        encoding="utf-8",
    )
    if mtime is not None:
        import os

        os.utime(report, (mtime, mtime))
    return report


def _write_summary(
    root: Path, run_id: str, *, resolved_ids: list[str], empty_patch_ids: list[str],
    mtime: float | None = None,
) -> Path:
    path = root / f"{MODEL}.{run_id}.json"
    path.write_text(
        json.dumps(
            {
                "resolved_ids": resolved_ids,
                "unresolved_ids": [],
                "empty_patch_ids": empty_patch_ids,
                "error_ids": [],
            }
        ),
        encoding="utf-8",
    )
    if mtime is not None:
        import os

        os.utime(path, (mtime, mtime))
    return path


# ------------------------------------------------------------ run_id isolation


def test_scoring_run_id_includes_a_per_process_token():
    run_id = swebench._scoring_run_id(INSTANCE)
    assert run_id.startswith(f"swebench-{INSTANCE}-")
    assert run_id != f"swebench-{INSTANCE}"
    assert swebench._SCORING_SESSION in run_id


def test_scoring_run_id_is_stable_within_a_process():
    # Two instances scored by the same process share the session token, so a
    # single run's artifacts stay together and are findable.
    a = swebench._scoring_run_id(INSTANCE)
    b = swebench._scoring_run_id(INSTANCE)
    assert a == b
    other = swebench._scoring_run_id("astropy__astropy-13033")
    assert other.endswith(swebench._SCORING_SESSION)


def test_scoring_run_id_sanitises_slashes():
    assert "/" not in swebench._scoring_run_id("owner/repo-1")


# ------------------------------------------------------------ freshness guard


def test_stale_report_is_not_read_as_this_runs_verdict(tmp_path, monkeypatch):
    """The exact defect: an old resolved=True must not answer today's request."""
    monkeypatch.chdir(tmp_path)
    run_id = f"swebench-{INSTANCE}"
    stale = time.time() - 6 * 3600
    _write_report(tmp_path, run_id, INSTANCE, resolved=True, mtime=stale)

    verdict = swebench._read_official_resolution(
        instance_id=INSTANCE, run_id=run_id, model_name=MODEL, not_before=time.time()
    )
    assert verdict is None, "a report from hours ago is not evidence about this run"


def test_fresh_report_is_read(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    run_id = swebench._scoring_run_id(INSTANCE)
    requested_at = time.time()
    _write_report(tmp_path, run_id, INSTANCE, resolved=True, mtime=None)

    verdict = swebench._read_official_resolution(
        instance_id=INSTANCE, run_id=run_id, model_name=MODEL, not_before=requested_at
    )
    assert verdict is True


def test_fresh_report_reporting_false_is_read_as_false(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    run_id = swebench._scoring_run_id(INSTANCE)
    requested_at = time.time()
    _write_report(tmp_path, run_id, INSTANCE, resolved=False, mtime=None)

    assert (
        swebench._read_official_resolution(
            instance_id=INSTANCE, run_id=run_id, model_name=MODEL,
            not_before=requested_at,
        )
        is False
    )


def test_stale_run_summary_is_not_read(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    run_id = f"swebench-{INSTANCE}"
    _write_summary(
        tmp_path, run_id, resolved_ids=[INSTANCE], empty_patch_ids=[],
        mtime=time.time() - 6 * 3600,
    )
    assert (
        swebench._read_official_resolution(
            instance_id=INSTANCE, run_id=run_id, model_name=MODEL,
            not_before=time.time(),
        )
        is None
    )


def test_fresh_empty_patch_summary_reports_false(tmp_path, monkeypatch):
    """The verdict the real run should have recorded for a zero-byte patch."""
    monkeypatch.chdir(tmp_path)
    run_id = swebench._scoring_run_id(INSTANCE)
    requested_at = time.time()
    _write_summary(tmp_path, run_id, resolved_ids=[], empty_patch_ids=[INSTANCE])
    assert (
        swebench._read_official_resolution(
            instance_id=INSTANCE, run_id=run_id, model_name=MODEL,
            not_before=requested_at,
        )
        is False
    )


def test_omitting_not_before_keeps_legacy_behaviour(tmp_path, monkeypatch):
    """Callers that pass nothing still read whatever is on disk.

    Kept deliberately permissive so unrelated callers and older tests do not
    change meaning; the production path in ``score()`` always passes
    ``not_before``.
    """
    monkeypatch.chdir(tmp_path)
    run_id = f"swebench-{INSTANCE}"
    _write_report(tmp_path, run_id, INSTANCE, resolved=True, mtime=time.time() - 9999)
    assert (
        swebench._read_official_resolution(
            instance_id=INSTANCE, run_id=run_id, model_name=MODEL
        )
        is True
    )


def test_missing_evidence_still_returns_none(tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    assert (
        swebench._read_official_resolution(
            instance_id=INSTANCE,
            run_id=swebench._scoring_run_id(INSTANCE),
            model_name=MODEL,
            not_before=time.time(),
        )
        is None
    )


def test_error_ids_still_raise_rather_than_reporting_false(tmp_path, monkeypatch):
    """A crashed evaluation measured nothing; that must stay an exception."""
    monkeypatch.chdir(tmp_path)
    run_id = swebench._scoring_run_id(INSTANCE)
    path = tmp_path / f"{MODEL}.{run_id}.json"
    path.write_text(
        json.dumps({"resolved_ids": [], "error_ids": [INSTANCE]}), encoding="utf-8"
    )
    try:
        swebench._read_official_resolution(
            instance_id=INSTANCE, run_id=run_id, model_name=MODEL,
            not_before=time.time() - 1,
        )
    except swebench.OfficialScorerUnavailable as exc:
        assert INSTANCE in str(exc)
    else:  # pragma: no cover - the guard regressed
        raise AssertionError("error_ids must not be reported as a verdict")
