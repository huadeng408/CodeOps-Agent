"""Run manifest tests (plan Task 7.1)."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from eval.manifest import (
    DatasetPin,
    RunPin,
    check_path_overlap,
    git_dirty_hash,
    git_head,
    load_manifest,
    prompt_hash,
    write_manifest,
)


def valid_dataset() -> DatasetPin:
    return DatasetPin(
        name="evalplus",
        revision="0123456789abcdef0123456789abcdef01234567",
        sha256="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        license_spdx="CC-BY-4.0",
        scorer="official-evalplus",
    )


def valid_run(**overrides) -> RunPin:
    values = dict(
        dataset=valid_dataset(),
        git_sha="abcdef0",
        git_dirty_hash="0" * 64,
        model="deepseek-v4-pro",
        model_revision="deepseek-v4-pro@0123456",
        prompt_hash="0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        tool_policy="code-agent-default",
        budget_tokens=1_000_000,
        budget_cost=5.0,
    )
    values.update(overrides)
    return RunPin(**values)


def test_valid_run_passes() -> None:
    assert valid_run().validate() == []


def test_floating_dataset_revision_rejected() -> None:
    dataset = DatasetPin(
        name="evalplus",
        revision="main",
        sha256="0" * 64,
        license_spdx="CC-BY-4.0",
        scorer="official",
    )
    issues = dataset.validate()
    assert any("revision" in issue for issue in issues)


def test_missing_dataset_hash_rejected() -> None:
    dataset = DatasetPin(
        name="evalplus",
        revision="0123456789abcdef0123456789abcdef01234567",
        sha256="",
        license_spdx="CC-BY-4.0",
        scorer="official",
    )
    issues = dataset.validate()
    assert any("sha256" in issue for issue in issues)


def test_disallowed_license_rejected() -> None:
    dataset = DatasetPin(
        name="evalplus",
        revision="0123456789abcdef0123456789abcdef01234567",
        sha256="0" * 64,
        license_spdx="Proprietary-Unknown",
        scorer="official",
    )
    issues = dataset.validate()
    assert any("license" in issue for issue in issues)


def test_dirty_hash_required_when_dirty() -> None:
    run = valid_run(git_dirty_hash="not-a-hash")
    issues = run.validate()
    assert any("dirty_hash" in issue for issue in issues)


def test_zero_budget_rejected() -> None:
    run = valid_run(budget_tokens=0)
    issues = run.validate()
    assert any("budget_tokens" in issue for issue in issues)


def test_load_manifest_round_trip(tmp_path: Path) -> None:
    run = valid_run()
    path = tmp_path / "run-manifest.json"
    write_manifest(run, path)
    loaded = load_manifest(path)
    assert loaded.model == run.model
    assert loaded.dataset.scorer == run.dataset.scorer
    assert loaded.budget_tokens == run.budget_tokens


def test_load_manifest_rejects_invalid(tmp_path: Path) -> None:
    path = tmp_path / "bad.json"
    path.write_text(
        json.dumps(
            {
                "dataset": {
                    "name": "x",
                    "revision": "main",
                    "sha256": "short",
                    "license_spdx": "Nope",
                    "scorer": "",
                },
                "model": "",
            }
        ),
        encoding="utf-8",
    )
    with pytest.raises(ValueError) as exc:
        load_manifest(path)
    assert "revision" in str(exc.value)


def test_git_head_and_dirty_hash_from_repo() -> None:
    # This repo is a git repository; both must return well-formed values.
    head = git_head(".")
    assert len(head) >= 7
    dirty = git_dirty_hash(".")
    assert len(dirty) == 64


def test_prompt_hash_deterministic() -> None:
    assert prompt_hash("hello") == prompt_hash("hello")
    assert prompt_hash("hello") != prompt_hash("hello!")


def test_check_path_overlap_detects_nested() -> None:
    assert check_path_overlap("eval/data/bench", "eval/data") is True
    assert check_path_overlap("eval/data", "eval/data/bench") is True


def test_check_path_overlap_allows_separate() -> None:
    assert check_path_overlap("eval/benchmarks", "corpus/sources") is False
