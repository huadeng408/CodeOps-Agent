"""Contract tests for the pinned tau2-bench official CLI runner."""

from __future__ import annotations

import json
import subprocess
from pathlib import Path

import pytest


def _collect_fresh_receipt(runner: object, run_name: str, artifact_root: Path):
    import eval.benchmarks.tau2official as tau2_module

    checkout = runner.config.checkout  # type: ignore[attr-defined]
    raw = checkout / "data" / "simulations" / run_name / "results.json"
    staged = raw.with_name("results.fixture.json")
    raw.replace(staged)
    original_run = tau2_module.subprocess.run

    def replay_fixture(command, *, cwd, env, check):
        assert cwd == checkout
        assert check is False
        staged.replace(raw)
        return subprocess.CompletedProcess(command, 0)

    tau2_module.subprocess.run = replay_fixture
    try:
        execution = runner.prepare_run(run_name)  # type: ignore[attr-defined]
        execution.run(env={})
        return runner.collect_receipt(execution, artifact_root)  # type: ignore[attr-defined]
    finally:
        tau2_module.subprocess.run = original_run
        if staged.exists() and not raw.exists():
            staged.replace(raw)


def _write_checkout(root: Path) -> None:
    (root / "data" / "simulations").mkdir(parents=True)
    (root / "data" / "source.json").write_text('{"task": "source"}\n', encoding="utf-8")
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
        "--agent", "llm_agent", "--user", "user_simulator",
        "--agent-llm", "openai/gpt-5.6-sol",
        "--user-llm", "openai/gpt-5.6-sol",
        "--num-trials", "1", "--num-tasks", "1",
        "--max-concurrency", "1", "--seed", "42",
        "--save-to", "receipt-001",
    ]
    assert runner.pins["dataset_revision"] == "fc0055dc4e0a316c3f83133267fbd6faaa770992"
    assert runner.pins["agent"] == "llm_agent"
    assert runner.pins["user"] == "user_simulator"
    assert runner.validate() == []


def test_tau2_official_runner_allows_solo_agent_only_with_dummy_user(tmp_path: Path) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(
            checkout=checkout,
            source_commit="f" * 40,
            data_tree_sha256="a" * 64,
            model="openai/gpt-5.6-sol",
            agent="llm_agent_solo",
            user="dummy_user",
        )
    )

    assert "--agent" in runner.command("solo-receipt")
    assert "llm_agent_solo" in runner.command("solo-receipt")
    assert "dummy_user" in runner.command("solo-receipt")
    invalid = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "a" * 64, "openai/gpt-5.6-sol", agent="llm_agent_solo")
    )
    assert "llm_agent_solo requires dummy_user" in invalid.validate()


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

    receipt = _collect_fresh_receipt(runner, run_name, tmp_path / "artifact")

    copied = tmp_path / "artifact" / "scorer" / "tau2-results.json"
    assert copied.read_bytes() == raw.read_bytes()
    assert receipt["official_output_sha256"]
    assert receipt["source_commit"] == "f" * 40
    assert receipt["status"] == "OFFICIAL_INFRA_FAILURE"
    assert receipt["simulation_schema_valid"] is False
    assert receipt["root_schema_error"]
    assert json.loads((tmp_path / "artifact" / "receipt.json").read_text(encoding="utf-8")) == receipt


def test_tau2_official_runner_records_null_reward_info_as_infrastructure_failure(tmp_path: Path) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    run_name = "infra-error"
    raw = checkout / "data" / "simulations" / run_name / "results.json"
    raw.parent.mkdir(parents=True)
    raw.write_text(
        json.dumps(
            {
                "info": {"git_commit": "f" * 40},
                "simulations": [
                    {
                        "termination_reason": "infrastructure_error",
                        "reward_info": None,
                        "info": {"error_type": "TypeError"},
                    }
                ],
            }
        ),
        encoding="utf-8",
    )
    runner = Tau2OfficialRunner(Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol"))

    receipt = _collect_fresh_receipt(runner, run_name, tmp_path / "artifact")

    assert receipt["status"] == "OFFICIAL_INFRA_FAILURE"
    assert receipt["rewards"] == []
    assert receipt["infrastructure_error_count"] == 1
    assert receipt["official_output_sha256"]
    assert (tmp_path / "artifact" / "scorer" / "tau2-results.json").read_bytes() == raw.read_bytes()


def test_tau2_official_runner_rejects_incomplete_simulation_even_when_reward_is_one(
    tmp_path: Path,
) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    raw = checkout / "data" / "simulations" / "incomplete" / "results.json"
    raw.parent.mkdir(parents=True)
    raw.write_text(
        json.dumps(
            {
                "info": {"git_commit": "f" * 40},
                "simulations": [{"reward_info": {"reward": 1.0}}],
            }
        ),
        encoding="utf-8",
    )
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol")
    )

    receipt = _collect_fresh_receipt(runner, "incomplete", tmp_path / "artifact")

    assert receipt["status"] == "OFFICIAL_INFRA_FAILURE"
    assert receipt["malformed_simulation_count"] == 1
    assert receipt["schema_error_count"] >= 1
    assert any(".id must be" in error for error in receipt["schema_errors"])
    assert any(".task_id must be" in error for error in receipt["schema_errors"])
    assert any(".termination_reason" in error for error in receipt["schema_errors"])


def test_tau2_official_runner_accepts_complete_successful_simulation(tmp_path: Path) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    raw = checkout / "data" / "simulations" / "complete" / "results.json"
    raw.parent.mkdir(parents=True)
    raw.write_text(
        json.dumps(
            {
                "info": {"git_commit": "f" * 40},
                "simulations": [
                    {
                        "id": "simulation-1",
                        "task_id": "task-1",
                        "termination_reason": "user_stop",
                        "reward_info": {"reward": 1.0},
                    }
                ],
            }
        ),
        encoding="utf-8",
    )
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol")
    )

    receipt = _collect_fresh_receipt(runner, "complete", tmp_path / "artifact")

    assert receipt["status"] == "OFFICIAL_PASS"
    assert receipt["simulation_schema_valid"] is True
    assert receipt["schema_error_count"] == 0


@pytest.mark.parametrize(
    "reward", [None, True, "1.0", float("nan"), float("inf"), 10**400]
)
def test_tau2_official_runner_rejects_non_finite_or_non_numeric_reward(
    tmp_path: Path, reward: object
) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    raw = checkout / "data" / "simulations" / "invalid-reward" / "results.json"
    raw.parent.mkdir(parents=True)
    raw.write_text(
        json.dumps(
            {
                "info": {"git_commit": "f" * 40},
                "simulations": [
                    {
                        "id": "simulation-1",
                        "task_id": "task-1",
                        "termination_reason": "agent_stop",
                        "reward_info": {"reward": reward},
                    }
                ],
            }
        ),
        encoding="utf-8",
    )
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol")
    )

    receipt = _collect_fresh_receipt(runner, "invalid-reward", tmp_path / "artifact")

    assert receipt["status"] == "OFFICIAL_INFRA_FAILURE"
    assert receipt["invalid_reward_count"] == 1
    assert receipt["reward_count"] == 0


def test_tau2_official_runner_preserves_simulation_denominator_when_reward_is_missing(tmp_path: Path) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    raw = checkout / "data" / "simulations" / "missing-reward" / "results.json"
    raw.parent.mkdir(parents=True)
    raw.write_text(
        json.dumps(
            {
                "info": {"git_commit": "f" * 40},
                "simulations": [{"termination_reason": "model_error", "info": {"error_type": "TimeoutError"}}],
            }
        ),
        encoding="utf-8",
    )
    runner = Tau2OfficialRunner(Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol"))

    receipt = _collect_fresh_receipt(runner, "missing-reward", tmp_path / "artifact")

    assert receipt["status"] == "OFFICIAL_INFRA_FAILURE"
    assert receipt["simulation_count"] == 1
    assert receipt["reward_count"] == 0
    assert receipt["missing_reward_info_count"] == 1


@pytest.mark.parametrize("simulations", [None, {"trial": 1}, []])
def test_tau2_official_runner_rejects_missing_or_empty_simulation_root(
    tmp_path: Path, simulations: object
) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    raw = checkout / "data" / "simulations" / "malformed-root" / "results.json"
    raw.parent.mkdir(parents=True)
    raw.write_text(
        json.dumps({"info": {"git_commit": "f" * 40}, "simulations": simulations}),
        encoding="utf-8",
    )
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol")
    )

    receipt = _collect_fresh_receipt(runner, "malformed-root", tmp_path / "artifact")

    assert receipt["status"] == "OFFICIAL_INFRA_FAILURE"
    assert receipt["simulation_count"] == 0
    assert receipt["simulation_schema_valid"] is False
    assert receipt["root_schema_error"]


def test_tau2_receipt_script_uses_pinned_isolated_checkout_and_phoenix_readback() -> None:
    script = (
        Path(__file__).resolve().parents[2]
        / "scripts"
        / "run-tau2-official-receipt.ps1"
    ).read_text(encoding="utf-8")

    assert '[string]$CheckoutPath' in script
    assert 'fc0055dc4e0a316c3f83133267fbd6faaa770992' in script
    assert 'uv", "run", "tau2", "run"' in script
    assert 'OfficialReceiptTrace' in script
    assert 'TraceCapture' in script
    assert 'read_run_spans' in script
    assert 'write_official_receipt_trace(' in script
    assert 'refresh_receipt_checksums' in script
    assert 'max_concurrency=1' in script
    assert 'TAU2_V101_DATA_TREE_SHA256' in script
    assert 'expected_data_tree_sha256=TAU2_V101_DATA_TREE_SHA256' in script
    assert 'require_clean_checkout=True' in script
    assert 'execution = runner.prepare_run(run_name)' in script
    assert 'completed = execution.run(command' in script
    assert 'runner.collect_receipt(execution, receipt_root)' in script
    assert 'process_returncode=completed.returncode' not in script
    assert 'result_existed_before=' not in script


def test_tau2_receipt_driver_fails_closed_for_a_nonpassing_trace_verdict() -> None:
    script = (
        Path(__file__).resolve().parents[2]
        / "scripts"
        / "run-tau2-official-receipt.ps1"
    ).read_text(encoding="utf-8")

    driver = script.split("@'\n", maxsplit=1)[1].split(
        "\n'@ | Set-Content -LiteralPath $driverPath", maxsplit=1
    )[0]
    assert 'if trace_report["verdict"] != "PASS":' in driver
    assert "raise SystemExit(1)" in driver


def test_tau2_source_data_hash_excludes_untracked_simulation_output(tmp_path: Path) -> None:
    from eval.benchmarks.tau2official import source_data_tree_sha256

    checkout = tmp_path / "tau2"
    (checkout / "data").mkdir(parents=True)
    (checkout / "data" / "mock.json").write_text('{"task": "source"}\n', encoding="utf-8")
    subprocess.run(["git", "init"], cwd=checkout, check=True, capture_output=True)
    subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=checkout, check=True)
    subprocess.run(["git", "config", "user.name", "Test"], cwd=checkout, check=True)
    subprocess.run(["git", "add", "data"], cwd=checkout, check=True)
    subprocess.run(["git", "commit", "-m", "source"], cwd=checkout, check=True, capture_output=True)

    before = source_data_tree_sha256(checkout)
    generated = checkout / "data" / "simulations" / "run" / "results.json"
    generated.parent.mkdir(parents=True)
    generated.write_text('{"reward": 0}\n', encoding="utf-8")

    assert source_data_tree_sha256(checkout) == before


def _init_git_checkout(root: Path) -> str:
    _write_checkout(root)
    subprocess.run(["git", "init", "-q"], cwd=root, check=True)
    subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=root, check=True)
    subprocess.run(["git", "config", "user.name", "Test"], cwd=root, check=True)
    subprocess.run(["git", "add", "."], cwd=root, check=True)
    subprocess.run(["git", "commit", "-qm", "source"], cwd=root, check=True)
    return subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip()


def test_tau2_official_runner_checks_independent_data_pin(tmp_path: Path) -> None:
    from eval.benchmarks.tau2official import (
        Tau2OfficialConfig,
        Tau2OfficialRunner,
        source_data_tree_sha256,
    )

    checkout = tmp_path / "tau2"
    commit = _init_git_checkout(checkout)
    actual_pin = source_data_tree_sha256(checkout)
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(
            checkout,
            commit,
            actual_pin,
            "openai/gpt-5.6-sol",
            expected_data_tree_sha256="0" * 64,
            require_clean_checkout=True,
        )
    )

    assert "data tree does not match expected pin" in runner.validate()


def test_tau2_official_runner_rejects_dirty_checkout_when_strict_pins_enabled(
    tmp_path: Path,
) -> None:
    from eval.benchmarks.tau2official import (
        Tau2OfficialConfig,
        Tau2OfficialRunner,
        source_data_tree_sha256,
    )

    checkout = tmp_path / "tau2"
    commit = _init_git_checkout(checkout)
    (checkout / "pyproject.toml").write_text("dirty\n", encoding="utf-8")
    actual_pin = source_data_tree_sha256(checkout)
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(
            checkout,
            commit,
            actual_pin,
            "openai/gpt-5.6-sol",
            expected_data_tree_sha256=actual_pin,
            require_clean_checkout=True,
        )
    )

    assert "checkout has uncommitted changes" in runner.validate()


def test_tau2_fresh_result_gate_rejects_failed_process_and_existing_output(
    tmp_path: Path,
) -> None:
    from eval.benchmarks.tau2official import assert_fresh_run_result

    result_path = tmp_path / "data" / "simulations" / "run" / "results.json"
    result_path.parent.mkdir(parents=True)
    result_path.write_text("stale", encoding="utf-8")

    with pytest.raises(RuntimeError, match="already existed"):
        assert_fresh_run_result(result_path, returncode=0, existed_before=True)
    result_path.unlink()
    with pytest.raises(RuntimeError, match="exited with code 7"):
        assert_fresh_run_result(result_path, returncode=7, existed_before=False)


def test_tau2_fresh_result_gate_requires_output_from_successful_process(
    tmp_path: Path,
) -> None:
    from eval.benchmarks.tau2official import assert_fresh_run_result

    result_path = tmp_path / "data" / "simulations" / "run" / "results.json"
    with pytest.raises(RuntimeError, match="produced no result file"):
        assert_fresh_run_result(result_path, returncode=0, existed_before=False)

    result_path.parent.mkdir(parents=True)
    result_path.write_text('{"simulations": []}\n', encoding="utf-8")

    assert_fresh_run_result(result_path, returncode=0, existed_before=False)


def test_tau2_receipt_collection_requires_fresh_process_proof(tmp_path: Path) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    run_name = "stale-result"
    raw = checkout / "data" / "simulations" / run_name / "results.json"
    raw.parent.mkdir(parents=True)
    raw.write_text(
        json.dumps({"info": {"git_commit": "f" * 40}, "simulations": []}),
        encoding="utf-8",
    )
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol")
    )

    with pytest.raises(TypeError, match="process_returncode"):
        runner.collect_receipt(run_name, tmp_path / "artifact")


@pytest.mark.parametrize("run_name_kind", ["parent", "absolute"])
def test_tau2_official_runner_rejects_run_name_path_escape(
    tmp_path: Path, run_name_kind: str
) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    run_name = "../escape" if run_name_kind == "parent" else str(tmp_path / "outside-run")
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol")
    )

    with pytest.raises(ValueError, match="run_name"):
        runner.command(run_name)
    with pytest.raises(ValueError, match="run_name"):
        runner.collect_receipt(
            run_name,
            tmp_path / "artifact",
            process_returncode=0,
            result_existed_before=False,
        )


def test_tau2_official_runner_accepts_the_script_generated_run_name_length(
    tmp_path: Path,
) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    run_name = "localcode-" + ("a" * 64)
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol")
    )

    assert runner.command(run_name)[-1] == run_name


def test_tau2_official_runner_rejects_caller_claimed_process_evidence(
    tmp_path: Path,
) -> None:
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    run_name = "caller-claimed"
    raw = checkout / "data" / "simulations" / run_name / "results.json"
    raw.parent.mkdir(parents=True)
    raw.write_text(
        json.dumps(
            {
                "info": {"git_commit": "f" * 40},
                "simulations": [
                    {
                        "id": "simulation-1",
                        "task_id": "task-1",
                        "termination_reason": "user_stop",
                        "reward_info": {"reward": 1.0},
                    }
                ],
            }
        ),
        encoding="utf-8",
    )
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol")
    )

    with pytest.raises(ValueError, match="runner-issued execution"):
        runner.collect_receipt(
            run_name,
            tmp_path / "artifact",
            process_returncode=0,
            result_existed_before=False,
        )
    assert not (tmp_path / "artifact" / "receipt.json").exists()


def test_tau2_official_runner_collects_output_only_after_its_execution_session(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    import eval.benchmarks.tau2official as tau2_module
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    run_name = "runner-observed"
    raw = checkout / "data" / "simulations" / run_name / "results.json"
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol")
    )

    def fake_run(command, *, cwd, env, check):
        assert cwd == checkout
        assert check is False
        raw.parent.mkdir(parents=True)
        raw.write_text(
            json.dumps(
                {
                    "info": {"git_commit": "f" * 40},
                    "simulations": [
                        {
                            "id": "simulation-1",
                            "task_id": "task-1",
                            "termination_reason": "user_stop",
                            "reward_info": {"reward": 1.0},
                        }
                    ],
                }
            ),
            encoding="utf-8",
        )
        return subprocess.CompletedProcess(command, 0)

    monkeypatch.setattr(tau2_module.subprocess, "run", fake_run)
    execution = runner.prepare_run(run_name)
    completed = execution.run(env={})
    receipt = runner.collect_receipt(execution, tmp_path / "artifact")

    assert completed.returncode == 0
    assert receipt["status"] == "OFFICIAL_PASS"


def test_tau2_official_runner_records_nonzero_process_as_infra_failure(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    import eval.benchmarks.tau2official as tau2_module
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol")
    )

    def fake_run(command, *, cwd, env, check):
        assert cwd == checkout
        assert check is False
        return subprocess.CompletedProcess(command, 23)

    monkeypatch.setattr(tau2_module.subprocess, "run", fake_run)
    execution = runner.prepare_run("process-failure")

    completed = execution.run(env={})
    receipt = runner.collect_receipt(execution, tmp_path / "artifact")

    assert completed.returncode == 23
    assert receipt["status"] == "OFFICIAL_INFRA_FAILURE"
    assert receipt["process_exit_code"] == 23
    assert receipt["expected_simulation_count"] == 1
    assert receipt["simulation_count"] == 0
    assert receipt["missing_required_outputs"] == ["process-failure/results.json"]
    assert receipt["official_output"] is None
    assert json.loads(
        (tmp_path / "artifact" / "receipt.json").read_text(encoding="utf-8")
    ) == receipt


def test_tau2_receipt_uses_execution_snapshot_if_output_changes_after_evidence(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    import eval.benchmarks.tau2official as tau2_module
    from eval.benchmarks.tau2official import Tau2OfficialConfig, Tau2OfficialRunner

    checkout = tmp_path / "tau2"
    _write_checkout(checkout)
    run_name = "snapshot-race"
    raw = checkout / "data" / "simulations" / run_name / "results.json"
    runner = Tau2OfficialRunner(
        Tau2OfficialConfig(checkout, "f" * 40, "b" * 64, "openai/gpt-5.6-sol")
    )

    def fake_run(command, *, cwd, env, check):
        raw.parent.mkdir(parents=True)
        raw.write_text(
            json.dumps(
                {
                    "info": {"git_commit": "f" * 40},
                    "simulations": [
                        {
                            "id": "simulation-1",
                            "task_id": "task-1",
                            "termination_reason": "user_stop",
                            "reward_info": {"reward": 1.0},
                        }
                    ],
                }
            ),
            encoding="utf-8",
        )
        return subprocess.CompletedProcess(command, 0)

    monkeypatch.setattr(tau2_module.subprocess, "run", fake_run)
    execution = runner.prepare_run(run_name)
    execution.run(env={})
    original_evidence = execution.evidence

    def evidence_then_tamper(*, owner: object, token: object):
        evidence = original_evidence(owner=owner, token=token)
        raw.write_text(
            json.dumps(
                {
                    "info": {"git_commit": "f" * 40},
                    "simulations": [
                        {
                            "id": "simulation-1",
                            "task_id": "task-1",
                            "termination_reason": "user_stop",
                            "reward_info": {"reward": 0.0},
                        }
                    ],
                }
            ),
            encoding="utf-8",
        )
        return evidence

    monkeypatch.setattr(execution, "evidence", evidence_then_tamper)
    receipt = runner.collect_receipt(execution, tmp_path / "artifact")
    copied = json.loads(
        (tmp_path / "artifact" / "scorer" / "tau2-results.json").read_text(
            encoding="utf-8"
        )
    )

    assert receipt["status"] == "OFFICIAL_PASS"
    assert copied["simulations"][0]["reward_info"]["reward"] == 1.0
