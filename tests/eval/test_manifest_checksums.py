"""Manifest pins and artifact checksums for the active Goal's audit contract.

The run manifest must bind mandatory pins (git_sha, model, ...) and every run
tree must end with a verifiable
``checksums.sha256``.  A run missing mandatory pins must fail closed —
no manifest is emitted, so a non-reproducible run is never reported as
reproducible.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
from pathlib import Path

import pytest

from eval.adapter import EvalInstance, EvalResult
from eval.harness.artifacts import RunArtifacts
from eval.harness.runner import HarnessRun, _mark_workspace_preserved

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
    manifest anyway would fake a pin that does not exist.
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


@pytest.mark.parametrize("run_id", ["../outside", "nested/run", r"nested\run"])
def test_run_artifacts_rejects_run_ids_outside_one_run_directory(
    tmp_path: Path, run_id: str
) -> None:
    """A run identifier cannot select a sibling or nested artifact root."""
    with pytest.raises(ValueError, match="run_id"):
        RunArtifacts(run_id, tmp_path)


@pytest.mark.parametrize(
    ("writer", "name"),
    [
        ("write", "../outside.json"),
        ("append_line", "../outside.jsonl"),
        ("record_scorer_output", "../../outside.txt"),
        ("record_trace", "../../outside.json"),
    ],
)
def test_artifact_writers_reject_paths_outside_the_run_root(
    tmp_path: Path, writer: str, name: str
) -> None:
    """Caller-controlled artifact names cannot escape the run directory."""
    artifacts = RunArtifacts("run-safe", tmp_path)

    with pytest.raises(ValueError, match="artifact path"):
        if writer == "write":
            artifacts.write(name, {"ok": True})
        elif writer == "append_line":
            artifacts.append_line(name, {"ok": True})
        elif writer == "record_scorer_output":
            artifacts.record_scorer_output(name, "output")
        else:
            artifacts.record_trace(name, {"ok": True})

    assert not (tmp_path / "outside.json").exists()
    assert not (tmp_path / "outside.jsonl").exists()
    assert not (tmp_path / "outside.txt").exists()


def test_checksum_verification_rejects_paths_outside_the_run_root(tmp_path: Path) -> None:
    """A malicious checksum manifest cannot make verification read a sibling file."""
    outside = tmp_path / "outside.txt"
    outside.write_text("external", encoding="utf-8")
    artifacts = RunArtifacts("run-safe", tmp_path)
    digest = hashlib.sha256(outside.read_bytes()).hexdigest()
    (artifacts.root / "checksums.sha256").write_text(
        f"{digest}  ../outside.txt\n", encoding="utf-8"
    )

    assert artifacts.verify_checksums() == ["unsafe:../outside.txt"]


def test_checksum_manifest_symlink_cannot_read_or_overwrite_outside_run_root(
    tmp_path: Path,
) -> None:
    """The reserved checksum filename cannot redirect finalization outside the run."""
    artifacts = RunArtifacts("run-safe", tmp_path)
    outside = tmp_path / "outside.txt"
    outside.write_text("sentinel", encoding="utf-8")
    manifest = artifacts.root / "checksums.sha256"
    try:
        manifest.symlink_to(outside)
    except OSError as exc:  # pragma: no cover - host policy, not product behavior
        pytest.skip(f"file symlinks unavailable: {exc}")

    assert artifacts.verify_checksums() == ["unsafe:checksums.sha256"]
    with pytest.raises(ValueError, match="checksum manifest"):
        artifacts.write_checksums()
    assert outside.read_text(encoding="utf-8") == "sentinel"


def test_checksum_finalization_replaces_existing_link_without_mutating_target(
    tmp_path: Path,
) -> None:
    """Atomic finalization must not truncate another path sharing the target file."""
    artifacts = RunArtifacts("run-safe", tmp_path)
    artifacts.write("artifact.json", {"value": 1})
    outside = tmp_path / "outside.txt"
    outside.write_text("sentinel", encoding="utf-8")
    manifest = artifacts.root / "checksums.sha256"
    try:
        manifest.hardlink_to(outside)
    except OSError as exc:  # pragma: no cover - host filesystem policy
        pytest.skip(f"hard links unavailable: {exc}")

    artifacts.write_checksums()

    assert outside.read_text(encoding="utf-8") == "sentinel"
    assert "artifact.json" in manifest.read_text(encoding="utf-8")


@pytest.mark.parametrize(
    ("writer", "name"),
    [
        ("write", "artifact.json"),
        ("record_scorer_output", "official.log"),
        ("record_trace", "trace.json"),
    ],
)
def test_overwrite_artifact_writers_replace_hardlinks_without_mutating_target(
    tmp_path: Path, writer: str, name: str
) -> None:
    """Overwrite writers must replace a shared directory entry, not its inode."""
    artifacts = RunArtifacts("run-safe", tmp_path)
    outside = tmp_path / "outside.txt"
    outside.write_text("sentinel", encoding="utf-8")
    subtree = {
        "write": artifacts.root,
        "record_scorer_output": artifacts.root / "scorer",
        "record_trace": artifacts.root / "traces",
    }[writer]
    subtree.mkdir(parents=True, exist_ok=True)
    artifact_path = subtree / name
    try:
        artifact_path.hardlink_to(outside)
    except OSError as exc:  # pragma: no cover - host filesystem policy
        pytest.skip(f"hard links unavailable: {exc}")

    if writer == "write":
        artifacts.write(name, {"value": 1})
    elif writer == "record_scorer_output":
        artifacts.record_scorer_output(name, "official output")
    else:
        artifacts.record_trace(name, {"trace_id": "trace-1"})

    assert outside.read_text(encoding="utf-8") == "sentinel"
    assert artifact_path.read_text(encoding="utf-8") != "sentinel"


def test_append_line_rejects_preexisting_hardlink_without_mutating_target(
    tmp_path: Path,
) -> None:
    """Appending must fail before writing when the file has another hard link."""
    artifacts = RunArtifacts("run-safe", tmp_path)
    outside = tmp_path / "outside.txt"
    outside.write_text("sentinel", encoding="utf-8")
    artifact_path = artifacts.root / "events.jsonl"
    try:
        artifact_path.hardlink_to(outside)
    except OSError as exc:  # pragma: no cover - host filesystem policy
        pytest.skip(f"hard links unavailable: {exc}")

    with pytest.raises(ValueError, match="hard link"):
        artifacts.append_line("events.jsonl", {"status": "started"})

    assert outside.read_text(encoding="utf-8") == "sentinel"


def test_append_line_does_not_mutate_hardlink_created_after_link_check(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A raced hard link must retain the bytes that existed before append."""
    artifacts = RunArtifacts("run-safe", tmp_path)
    artifact_path = artifacts.root / "events.jsonl"
    artifact_path.write_text("existing\n", encoding="utf-8")
    raced_link = tmp_path / "raced.txt"
    real_fstat = os.fstat

    def fstat_then_link(fd: int) -> os.stat_result:
        result = real_fstat(fd)
        if not raced_link.exists():
            raced_link.hardlink_to(artifact_path)
        return result

    monkeypatch.setattr(os, "fstat", fstat_then_link)

    artifacts.append_line("events.jsonl", {"status": "started"})

    assert raced_link.read_text(encoding="utf-8") == "existing\n"
    assert artifact_path.read_text(encoding="utf-8") == (
        'existing\n{"status": "started"}\n'
    )


def test_checksum_verification_rejects_duplicate_manifest_paths(tmp_path: Path) -> None:
    """A checksum manifest has one unambiguous digest per artifact path."""
    artifacts = RunArtifacts("run-safe", tmp_path)
    target = artifacts.write("a.txt", {"value": 1})
    digest = hashlib.sha256(target.read_bytes()).hexdigest()
    (artifacts.root / "checksums.sha256").write_text(
        f"{digest}  a.txt\n{digest}  a.txt\n", encoding="utf-8"
    )

    assert artifacts.verify_checksums() == ["duplicate:a.txt"]


def test_checksum_verification_rejects_canonical_path_aliases(tmp_path: Path) -> None:
    """Lexical aliases cannot pin the same artifact more than once."""
    artifacts = RunArtifacts("run-safe", tmp_path)
    target = artifacts.write("a.txt", {"value": 1})
    digest = hashlib.sha256(target.read_bytes()).hexdigest()
    (artifacts.root / "checksums.sha256").write_text(
        f"{digest}  a.txt\n{digest}  ./a.txt\n", encoding="utf-8"
    )

    assert artifacts.verify_checksums() == ["duplicate:a.txt"]


def test_checksum_verification_rejects_non_sha256_digests(tmp_path: Path) -> None:
    """Only canonical lowercase SHA-256 values are accepted as evidence pins."""
    artifacts = RunArtifacts("run-safe", tmp_path)
    artifacts.write("a.txt", {"value": 1})
    (artifacts.root / "checksums.sha256").write_text(
        f"{'g' * 64}  a.txt\n", encoding="utf-8"
    )

    assert artifacts.verify_checksums() == [
        "malformed-digest:a.txt",
        "unpinned:a.txt",
    ]


def test_raw_scorer_output_redacts_credential_shapes(tmp_path: Path) -> None:
    """Official raw output remains auditable without persisting bearer tokens."""
    artifacts = RunArtifacts("run-safe", tmp_path)
    token = "sk-testonly0123456789"

    path = artifacts.record_scorer_output(
        "official.log", f"authorization=Bearer {token}\ndirect={token}\n"
    )

    content = path.read_text(encoding="utf-8")
    assert token not in content
    assert "Bearer <redacted>" in content
    assert "direct=<redacted>" in content


def test_all_structured_artifact_writers_redact_credential_shapes(
    tmp_path: Path,
) -> None:
    """Failures, events, predictions, manifests, traces and summaries are safe."""
    artifacts = RunArtifacts("run-safe", tmp_path)
    token = "sk-structured0123456789"
    bearer = f"Bearer {token}"

    artifacts.record_failure("i-1", "ERROR_AGENT", bearer)
    artifacts.record_event("i-1", "failed", bearer)
    artifacts.record_prediction({"instance_id": "i-1", "nested": [token]})
    artifacts.write_manifest({"endpoint_error": bearer})
    artifacts.record_trace("trace.json", {"authorization": bearer})
    artifacts.write_summary({"failure": {"message": token}})
    _mark_workspace_preserved(tmp_path, "ERROR_AGENT", bearer)

    persisted = "\n".join(
        path.read_text(encoding="utf-8")
        for path in artifacts.root.rglob("*")
        if path.is_file()
    )
    marker = (tmp_path / "WORKSPACE_PRESERVED").read_text(encoding="utf-8")
    assert token not in persisted + marker
    assert "<redacted>" in persisted
    assert "<redacted>" in marker


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


def test_manifest_persists_the_prompt_hash_used_for_pin_validation(tmp_path: Path) -> None:
    """An auto-resolved prompt pin must not become an empty manifest field."""
    artifacts = RunArtifacts("run-auto-prompt-pin", tmp_path)
    config = _pinned_config()
    config.pop("prompt_hash")
    harness = HarnessRun(
        run_id="run-auto-prompt-pin",
        artifacts=artifacts,
        adapter=FakeAdapter(),
        config=config,
    )

    harness.run([EvalInstance(instance_id="i1", task_description="task")])

    manifest = json.loads(
        (artifacts.root / "run-manifest.json").read_text(encoding="utf-8")
    )
    assert SHA256_RE.fullmatch(manifest["prompt_hash"])
