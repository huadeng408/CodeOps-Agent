"""Pinned CLI boundary for official tau2-bench text smoke runs.

This module intentionally does not replace the historical ``tau_bench``
adapter.  It wraps a separately checked out, pinned tau2-bench release and
preserves its raw result bytes as the official evidence.
"""

from __future__ import annotations

import hashlib
import json
import math
import re
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from eval.benchmarks._official_execution import OfficialRunExecution

# tau2-bench is a tool-use benchmark over its own simulator, not a retrieval
# benchmark; explicitly waive RAG-only pins in the manifest contract.
TRACE_CAPABILITIES: tuple[str, ...] = ()


TAU2_V101_COMMIT = "fc0055dc4e0a316c3f83133267fbd6faaa770992"
TAU2_V101_DATA_TREE_SHA256 = (
    "df29afa3d8fbce072dae983c75b166548e1cea48c5e58737e0d5b29349c0441d"
)
TAU2_V101_TAG = "v1.0.1"
TAU2_REPOSITORY = "https://github.com/sierra-research/tau2-bench"
TAU2_V101_TERMINATION_REASONS = frozenset(
    {
        "user_stop",
        "agent_stop",
        "max_steps",
        "timeout",
        "too_many_errors",
        "agent_error",
        "user_error",
        "infrastructure_error",
        "context_window_exceeded",
        "unexpected_error",
    }
)
_RUN_NAME = re.compile(r"[A-Za-z0-9][A-Za-z0-9._-]{0,127}\Z")


def source_data_tree_sha256(checkout: Path) -> str:
    """Hash tracked ``HEAD:data`` entries, excluding generated simulations.

    tau2 writes official run outputs below ``data/simulations``.  Hashing the
    live filesystem would therefore turn a benchmark input pin into an output-
    dependent value.  Git's tree listing provides the immutable source-data
    boundary for an already pinned checkout.
    """
    result = subprocess.run(
        ["git", "ls-tree", "-r", "HEAD", "data"],
        cwd=checkout,
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    if result.returncode != 0 or not result.stdout.strip():
        raise RuntimeError("cannot read tracked tau2 data tree")
    return hashlib.sha256(result.stdout.encode("utf-8")).hexdigest()


def assert_fresh_run_result(
    result_path: Path, *, returncode: int, existed_before: bool
) -> None:
    """Fail closed unless this invocation produced a successful result file."""
    if returncode != 0:
        raise RuntimeError(f"tau2 process exited with code {returncode}")
    if existed_before:
        raise RuntimeError(f"tau2 result already existed before this run: {result_path}")
    if not result_path.is_file():
        raise RuntimeError(f"tau2 process produced no result file: {result_path}")


def _validate_run_name(run_name: str) -> str:
    if not isinstance(run_name, str) or _RUN_NAME.fullmatch(run_name) is None:
        raise ValueError(
            "run_name must contain 1-128 ASCII letters, digits, dots, underscores, or hyphens"
        )
    return run_name


def _is_lower_hex(value: str | None, length: int) -> bool:
    return (
        isinstance(value, str)
        and len(value) == length
        and all(character in "0123456789abcdef" for character in value)
    )


def _finite_number(value: Any) -> float | None:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    try:
        number = float(value)
    except (OverflowError, ValueError):
        return None
    return number if math.isfinite(number) else None


def _git_output(checkout: Path, *arguments: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["git", *arguments],
        cwd=checkout,
        check=False,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )


def _simulation_summary(simulations: Any) -> dict[str, Any]:
    entries = simulations if isinstance(simulations, list) else []
    root_schema_error = (
        None
        if isinstance(simulations, list) and simulations
        else "results.simulations must be a non-empty list"
    )
    schema_errors = [root_schema_error] if root_schema_error else []
    rewards: list[float] = []
    malformed_simulation_count = 0
    missing_reward_info_count = 0
    invalid_reward_count = 0
    infrastructure_error_count = 0

    for index, entry in enumerate(entries):
        item_errors: list[str] = []
        if not isinstance(entry, dict):
            schema_errors.append(f"simulations[{index}] must be an object")
            malformed_simulation_count += 1
            continue

        for field in ("id", "task_id"):
            value = entry.get(field)
            if not isinstance(value, str) or not value.strip():
                item_errors.append(f"simulations[{index}].{field} must be a non-empty string")

        termination_reason = entry.get("termination_reason")
        if termination_reason not in TAU2_V101_TERMINATION_REASONS:
            item_errors.append(
                f"simulations[{index}].termination_reason is not valid for tau2 v1.0.1"
            )
        is_infrastructure_error = termination_reason == "infrastructure_error"
        if is_infrastructure_error:
            infrastructure_error_count += 1

        reward_info = entry.get("reward_info")
        if reward_info is None and is_infrastructure_error:
            pass
        elif not isinstance(reward_info, dict) or "reward" not in reward_info:
            missing_reward_info_count += 1
            item_errors.append(
                f"simulations[{index}].reward_info.reward is required"
            )
        else:
            reward = reward_info["reward"]
            numeric_reward = _finite_number(reward)
            if numeric_reward is None:
                invalid_reward_count += 1
                item_errors.append(
                    f"simulations[{index}].reward_info.reward must be a finite number"
                )
            else:
                rewards.append(numeric_reward)

        if item_errors:
            malformed_simulation_count += 1
            schema_errors.extend(item_errors)

    return {
        "rewards": rewards,
        "simulation_count": len(entries),
        "simulation_schema_valid": not schema_errors,
        "root_schema_error": root_schema_error,
        "schema_errors": schema_errors,
        "schema_error_count": len(schema_errors),
        "reward_count": len(rewards),
        "missing_reward_info_count": missing_reward_info_count,
        "invalid_reward_count": invalid_reward_count,
        "malformed_simulation_count": malformed_simulation_count,
        "infrastructure_error_count": infrastructure_error_count,
    }


@dataclass(frozen=True)
class Tau2OfficialConfig:
    checkout: Path
    source_commit: str
    data_tree_sha256: str
    model: str
    seed: int = 42
    max_concurrency: int = 1
    domain: str = "mock"
    agent: str = "llm_agent"
    user: str = "user_simulator"
    expected_data_tree_sha256: str | None = None
    require_clean_checkout: bool = False


class Tau2OfficialRunner:
    """Create commands and receipts for a fixed official tau2 CLI release."""

    def __init__(self, config: Tau2OfficialConfig) -> None:
        self.config = config
        self._execution_token = object()

    @property
    def pins(self) -> dict[str, str]:
        return {
            "benchmark": "tau2-bench",
            "dataset_name": "tau2-bench-v1.0.1-bundled-data",
            "dataset_revision": self.config.source_commit,
            "scorer_name": "tau2 CLI results.json",
            "agent": self.config.agent,
            "user": self.config.user,
        }

    def validate(self) -> list[str]:
        problems: list[str] = []
        checkout_exists = self.config.checkout.is_dir()
        if not checkout_exists:
            problems.append("checkout does not exist")
        if not _is_lower_hex(self.config.source_commit, 40):
            problems.append("source_commit must be a lowercase 40-character SHA-1")
        if not _is_lower_hex(self.config.data_tree_sha256, 64):
            problems.append("data_tree_sha256 must be a lowercase 64-character SHA-256")
        expected_data_pin = self.config.expected_data_tree_sha256
        if expected_data_pin is not None and not _is_lower_hex(expected_data_pin, 64):
            problems.append(
                "expected_data_tree_sha256 must be a lowercase 64-character SHA-256"
            )
        if self.config.require_clean_checkout and expected_data_pin is None:
            problems.append(
                "expected_data_tree_sha256 is required when require_clean_checkout is enabled"
            )
        if "/" not in self.config.model:
            problems.append("model must include a LiteLLM provider prefix")
        if not 1 <= self.config.max_concurrency <= 10:
            problems.append("max_concurrency must be between 1 and 10")
        if self.config.domain != "mock":
            problems.append("official smoke domain must be mock")
        if self.config.agent not in {"llm_agent", "llm_agent_solo"}:
            problems.append("official smoke agent must be llm_agent or llm_agent_solo")
        if self.config.agent == "llm_agent_solo" and self.config.user != "dummy_user":
            problems.append("llm_agent_solo requires dummy_user")
        if self.config.agent == "llm_agent" and self.config.user != "user_simulator":
            problems.append("llm_agent requires user_simulator")

        strict_pins_enabled = (
            expected_data_pin is not None or self.config.require_clean_checkout
        )
        if checkout_exists and strict_pins_enabled:
            head = _git_output(self.config.checkout, "rev-parse", "HEAD")
            if head.returncode != 0:
                problems.append("cannot read checkout HEAD")
            elif head.stdout.strip() != self.config.source_commit:
                problems.append("checkout HEAD does not match source_commit")

            try:
                actual_data_pin = source_data_tree_sha256(self.config.checkout)
            except RuntimeError:
                problems.append("cannot validate checkout data tree")
            else:
                if actual_data_pin != self.config.data_tree_sha256:
                    problems.append("data_tree_sha256 does not match checkout HEAD")
                if (
                    _is_lower_hex(expected_data_pin, 64)
                    and actual_data_pin != expected_data_pin
                ):
                    problems.append("data tree does not match expected pin")

            if self.config.require_clean_checkout:
                status = _git_output(
                    self.config.checkout,
                    "status",
                    "--porcelain=v1",
                    "--untracked-files=all",
                )
                if status.returncode != 0:
                    problems.append("cannot inspect checkout worktree")
                elif status.stdout.strip():
                    problems.append("checkout has uncommitted changes")
        return problems

    def _result_path(self, run_name: str) -> tuple[Path, Path]:
        """Return a result path that is contained by the pinned checkout."""
        _validate_run_name(run_name)
        checkout = self.config.checkout.resolve(strict=False)
        simulations_root = (checkout / "data" / "simulations").resolve(strict=False)
        try:
            simulations_root.relative_to(checkout)
        except ValueError as exc:
            raise ValueError("tau2 simulations root escapes the pinned checkout") from exc
        raw = simulations_root / run_name / "results.json"
        try:
            raw.resolve(strict=False).relative_to(simulations_root)
        except ValueError as exc:
            raise ValueError("tau2 run_name escapes the simulations root") from exc
        return raw, simulations_root

    def command(self, run_name: str) -> list[str]:
        _validate_run_name(run_name)
        problems = self.validate()
        if problems:
            raise ValueError("; ".join(problems))
        return [
            "uv", "run", "tau2", "run", "--domain", self.config.domain,
            "--agent", self.config.agent, "--user", self.config.user,
            "--agent-llm", self.config.model, "--user-llm", self.config.model,
            "--num-trials", "1", "--num-tasks", "1",
            "--max-concurrency", str(self.config.max_concurrency),
            "--seed", str(self.config.seed), "--save-to", run_name,
        ]

    def prepare_run(self, run_name: str) -> OfficialRunExecution:
        """Create a single-use session that observes the pinned tau2 command."""
        command = self.command(run_name)
        raw, simulations_root = self._result_path(run_name)
        return OfficialRunExecution(
            owner=self,
            token=self._execution_token,
            output_path=raw,
            output_root=simulations_root,
            default_command=command,
            default_cwd=self.config.checkout,
            process_runner=subprocess.run,
        )

    def collect_receipt(
        self,
        execution: object,
        artifact_root: Path,
        **legacy_evidence: object,
    ) -> dict[str, Any]:
        """Copy runner-observed official output into a minimal receipt."""
        if isinstance(execution, str):
            self._result_path(execution)
        if legacy_evidence:
            if legacy_evidence.get("result_existed_before") is True:
                raise RuntimeError("official output already existed before this run")
            claimed_returncode = legacy_evidence.get("process_returncode")
            if (
                isinstance(claimed_returncode, int)
                and not isinstance(claimed_returncode, bool)
                and claimed_returncode != 0
            ):
                raise RuntimeError(f"official process exited with code {claimed_returncode}")
            raise ValueError(
                "caller-provided process evidence is not accepted; use runner-issued execution"
            )
        if type(execution) is not OfficialRunExecution:
            raise TypeError(
                "receipt requires runner-issued execution session; process_returncode is not accepted"
            )
        problems = self.validate()
        if problems:
            raise ValueError("; ".join(problems))
        evidence = execution.evidence(owner=self, token=self._execution_token)
        raw = evidence.output_path
        simulations_root = (
            self.config.checkout.resolve(strict=False) / "data" / "simulations"
        ).resolve(strict=False)
        try:
            relative_raw = raw.relative_to(simulations_root)
        except ValueError as exc:
            raise ValueError("runner-issued result is outside the simulations root") from exc
        if len(relative_raw.parts) != 2 or relative_raw.name != "results.json":
            raise ValueError("runner-issued result path has an invalid tau2 shape")
        _validate_run_name(relative_raw.parts[0])
        receipt_base = {
            "benchmark": "tau2-bench",
            "upstream_repository": TAU2_REPOSITORY,
            "upstream_tag": TAU2_V101_TAG,
            "source_commit": self.config.source_commit,
            "data_tree_sha256": self.config.data_tree_sha256,
            "expected_data_tree_sha256": self.config.expected_data_tree_sha256,
            "seed": self.config.seed,
            "model": self.config.model,
            "max_concurrency": self.config.max_concurrency,
            "agent": self.config.agent,
            "user": self.config.user,
            "expected_simulation_count": 1,
        }
        if evidence.returncode != 0 or evidence.missing_required_outputs:
            destination: Path | None = None
            if evidence.output_bytes is not None:
                destination = artifact_root / "scorer" / "tau2-results.json"
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_bytes(evidence.output_bytes)
            summary = _simulation_summary(None)
            summary["infrastructure_error_count"] = 1
            receipt = {
                **receipt_base,
                "process_exit_code": evidence.returncode,
                "process_status": "failed",
                "process_failure_count": 1,
                "failure_mode": (
                    "OFFICIAL_PROCESS_EXIT_NONZERO"
                    if evidence.returncode != 0
                    else "OFFICIAL_REQUIRED_OUTPUT_MISSING"
                ),
                "status": "OFFICIAL_INFRA_FAILURE",
                **summary,
                "missing_required_outputs": list(evidence.missing_required_outputs),
                "observed_required_output_sha256": dict(evidence.required_output_sha256),
                "official_output_sha256": evidence.output_sha256,
                "official_output": destination.as_posix() if destination else None,
            }
            (artifact_root / "receipt.json").parent.mkdir(parents=True, exist_ok=True)
            (artifact_root / "receipt.json").write_text(
                json.dumps(receipt, indent=2, ensure_ascii=False) + "\n",
                encoding="utf-8",
            )
            return receipt
        if evidence.output_bytes is None or evidence.output_sha256 is None:
            raise RuntimeError("official execution output evidence is incomplete")
        try:
            parsed = json.loads(evidence.output_bytes)
        except (UnicodeDecodeError, json.JSONDecodeError) as exc:
            raise ValueError("official tau2 result is not valid JSON") from exc
        info = parsed.get("info") if isinstance(parsed, dict) else None
        if not isinstance(info, dict) or info.get("git_commit") != self.config.source_commit:
            raise ValueError("official tau2 result source commit does not match pin")
        destination = artifact_root / "scorer" / "tau2-results.json"
        destination.parent.mkdir(parents=True, exist_ok=True)
        destination.write_bytes(evidence.output_bytes)
        summary = _simulation_summary(
            parsed.get("simulations") if isinstance(parsed, dict) else None
        )
        rewards = summary["rewards"]
        if not summary["simulation_schema_valid"] or summary["infrastructure_error_count"]:
            status = "OFFICIAL_INFRA_FAILURE"
        elif rewards and all(reward == 1.0 for reward in rewards):
            status = "OFFICIAL_PASS"
        else:
            status = "OFFICIAL_FAILURE"
        receipt = {
            **receipt_base,
            "process_exit_code": evidence.returncode,
            "process_status": "completed",
            "process_failure_count": 0,
            "status": status,
            **summary,
            "missing_required_outputs": [],
            "observed_required_output_sha256": dict(evidence.required_output_sha256),
            "official_output_sha256": evidence.output_sha256,
            "official_output": destination.as_posix(),
        }
        (artifact_root / "receipt.json").write_text(
            json.dumps(receipt, indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
        )
        return receipt
