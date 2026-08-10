"""H3 — manifest pins + artifact checksums (plan Task 6).

Per DESIGN-MAP-2026-08-07 §20.7 the run manifest must bind mandatory pins
(git_sha, model, ...) and every run tree must end with a verifiable
``checksums.sha256``.  A run missing mandatory pins must fail closed —
no manifest is emitted, so a non-reproducible run is never reported as
reproducible.
"""

from __future__ import annotations

import hashlib
import json
import re
from pathlib import Path

import pytest

from eval.adapter import EvalInstance, EvalResult
from eval.harness.artifacts import RunArtifacts
from eval.harness.runner import HarnessRun

SHA256_RE = re.compile(r"^[0-9a-f]{64}$")


def _pinned_config(**overrides: object) -> dict[str, object]:
    config: dict[str, object] = {
        "git_sha": "a1b2c3d",
        "dirty_hash": "0" * 64,
        "model": "test-model",
        "prompt_hash": "0" * 64,
    }
    config.update(overrides)
    return config


class FakeAdapter:
    """Deterministic fake adapter for harness tests."""

    def solve_instance(self, instance: EvalInstance, working_dir: str, **kwargs) -> EvalResult:
        return EvalResult(
            instance_id=instance.instance_id,
            answer="ok",
            cost=0.01,
            tokens_in=100,
            tokens_out=50,
            wall_time_s=0.5,
            trace_id=f"trace-{instance.instance_id}",
        )


def test_manifest_requires_git_sha_and_dirty_hash(tmp_path: Path) -> None:
    """HarnessRun with an empty config must fail closed at _build_manifest.

    A run without pinned git/model identity is not reproducible; writing a
    manifest anyway would fake a pin that does not exist (design map §20.7).
    """
    instances = [EvalInstance(instance_id="i1", task_description="")]

    # Empty config → ValueError naming git_sha
    artifacts = RunArtifacts("run-empty", tmp_path)
    harness = HarnessRun(run_id="run-empty", artifacts=artifacts, adapter=FakeAdapter())
    with pytest.raises(ValueError, match="git_sha"):
        harness.run(instances)

    # git_sha present but model missing → ValueError naming model
    artifacts2 = RunArtifacts("run-no-model", tmp_path)
    harness2 = HarnessRun(
        run_id="run-no-model", artifacts=artifacts2, adapter=FakeAdapter(),
        config={"git_sha": "a1b2c3d"},
    )
    with pytest.raises(ValueError, match="model"):
        harness2.run(instances)

    # model present but git_sha missing → ValueError naming git_sha
    artifacts3 = RunArtifacts("run-no-git", tmp_path)
    harness3 = HarnessRun(
        run_id="run-no-git", artifacts=artifacts3, adapter=FakeAdapter(),
        config={"model": "test-model"},
    )
    with pytest.raises(ValueError, match="git_sha"):
        harness3.run(instances)

    # Fail-closed: no manifest may exist for any aborted run
    for root in (artifacts.root, artifacts2.root, artifacts3.root):
        assert not (root / "run-manifest.json").exists(), root
        assert not (root / "checksums.sha256").exists(), root

    # Fully pinned config succeeds
    artifacts_ok = RunArtifacts("run-ok", tmp_path)
    harness_ok = HarnessRun(
        run_id="run-ok", artifacts=artifacts_ok, adapter=FakeAdapter(),
        config=_pinned_config(),
    )
    result = harness_ok.run(instances)
    assert result["summary"]["completed"] == 1
    assert (artifacts_ok.root / "run-manifest.json").exists()


def test_checksums_generated_after_run(tmp_path: Path) -> None:
    """write_checksums() pins every artifact file with its SHA-256 digest."""
    artifacts = RunArtifacts("run-1", tmp_path)
    artifacts.record_instance({"instance_id": "i1"})
    artifacts.record_prediction({"instance_id": "i1", "answer": "x"})
    artifacts.write_summary({"total": 1, "completed": 1, "failed": 0, "skipped": 0})

    path = artifacts.write_checksums()
    assert path.name == "checksums.sha256"
    assert path.exists()

    content = path.read_text(encoding="utf-8")
    lines = [line for line in content.splitlines() if line.strip()]
    assert len(lines) >= 3  # instances.jsonl + predictions.jsonl + summary.json

    entries: dict[str, str] = {}
    for line in lines:
        digest, filename = line.split("  ", 1)
        assert SHA256_RE.match(digest), f"not a sha-256 hex digest: {line!r}"
        assert filename not in entries, f"duplicate entry for {filename!r}"
        entries[filename] = digest

    # Every digest matches the actual file bytes
    for name, digest in entries.items():
        actual = hashlib.sha256((artifacts.root / name).read_bytes()).hexdigest()
        assert digest == actual, f"checksum mismatch for {name}"

    # checksums.sha256 must not list itself, and must be sorted by filename
    assert "checksums.sha256" not in entries
    assert list(entries) == sorted(entries)

    # Re-running on an unchanged tree produces the identical checksum file
    assert artifacts.write_checksums().read_text(encoding="utf-8") == content


def test_finalize_writes_all_expected_files(tmp_path: Path) -> None:
    """A full HarnessRun run emits manifest, summary, environment, checksums."""
    run_id = "run-finalize"
    artifacts = RunArtifacts(run_id, tmp_path)
    harness = HarnessRun(
        run_id=run_id,
        artifacts=artifacts,
        adapter=FakeAdapter(),
        config=_pinned_config(),
    )

    result = harness.run([EvalInstance(instance_id="i1", task_description="task")])

    assert result["summary"]["completed"] == 1
    assert result["summary_path"] == str(artifacts.root / "summary.json")

    root = artifacts.root
    assert (root / "run-manifest.json").exists()
    assert (root / "summary.json").exists()
    assert (root / "environment.txt").exists()
    assert (root / "checksums.sha256").exists()

    # Manifest carries the mandatory pins
    manifest = json.loads((root / "run-manifest.json").read_text(encoding="utf-8"))
    assert manifest["git_sha"] == "a1b2c3d"
    assert manifest["dirty_hash"] == "0" * 64
    assert manifest["model"] == "test-model"

    # Checksums cover every final artifact and verify against the tree
    checksum_text = (root / "checksums.sha256").read_text(encoding="utf-8")
    for required in ("run-manifest.json", "summary.json", "environment.txt"):
        assert required in checksum_text, f"checksums.sha256 missing {required}"
    for line in checksum_text.splitlines():
        if not line.strip():
            continue
        digest, name = line.split("  ", 1)
        assert digest == hashlib.sha256((root / name).read_bytes()).hexdigest()
