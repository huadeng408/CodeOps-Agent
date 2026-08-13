"""Contract tests for the pinned tau2-bench official CLI runner."""

from __future__ import annotations

import json
from pathlib import Path

import pytest


def _write_checkout(root: Path) -> None:
    (root / "data" / "simulations").mkdir(parents=True)
    (root / "pyproject.toml").write_text("[project]\nname = 'tau2'\n", encoding="utf-8")
    (root / "LICENSE").write_text("MIT License\n", encoding="utf-8")


def test_tau2_official_runner_builds_pinned_single_concurrency_command(tmp_path: Path) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(
            checkout=checkout,
            source_commit="fc0055dc4e0a316c3f83133267fbd6faaa770992",
            data_tree_sha256="a" * 64,
            model="openai/gpt-5.6-sol",
            seed=42,
            max_concurrency=1,
        )
    )

    command = runner.command("receipt-001")

    assert command == [
        "uv", "run", "tau2", "run", "--domain", "mock",
        "--agent-llm", "openai/gpt-5.6-sol",
        "--user-llm", "openai/gpt-5.6-sol",
        "--num-trials", "1", "--num-tasks", "1",
        "--max-concurrency", "1", "--seed", "42",
        "--save-to", "receipt-001",
    ]
    assert runner.pins["dataset_revision"] == "fc0055dc4e0a316c3f83133267fbd6faaa770992"
    assert runner.validate() == []


@pytest.mark.parametrize("model", ["gpt-5.6-sol", "deepseek-v4-pro"])
def test_tau2_official_runner_rejects_model_without_provider_prefix(tmp_path: Path, model: str) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    runner = Tau2OfficialRunner(Tau2OfficialConfig(checkout, "f" * 40, "a" * 64, model))

    assert "model must include a LiteLLM provider prefix" in runner.validate()


def test_tau2_official_runner_copies_and_hashes_raw_upstream_result(tmp_path: Path) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    run_name = "receipt-002"
    raw = checkout / "data" / "simulations" / run_name / "results.json"
    raw.parent.mkdir(parents=True)
    raw.write_text(json.dumps({"info": {"git_commit": "f" * 40}, "simulations": []}), encoding="utf-8")
    runner = Tau2OfficialRunner(Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol"))

    receipt = runner.collect_receipt(run_name, tmp_path / "artifact")

    copied = tmp_path / "artifact" / "scorer" / "tau2-results.json"
    assert copied.read_bytes() == raw.read_bytes()
    assert receipt["official_output_sha256"]
    assert receipt["source_commit"] == "f" * 40
    assert receipt["status"] == "OFFICIAL_FAILURE"
