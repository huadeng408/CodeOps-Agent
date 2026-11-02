"""Pinned product experiments; model and tool execution stays in the Go Harness."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import queue
import re
import subprocess
import sys
import threading
import time
from collections.abc import Callable
from datetime import datetime
from itertools import product
from pathlib import Path, PurePosixPath, PureWindowsPath
from typing import Any

from eval.harness.artifacts import RunArtifacts
from eval.harness.process_tree import OwnedProcessTree as _OwnedProcessTree
from eval.harness.redaction import redact_credential_value
from eval.harness.source_pin import source_pin

REPOSITORY = Path(__file__).resolve().parents[1]
DEFAULT_POLICY = {
    "context_strategy": "metadata_on_demand",
    "memory_read": False,
    "frozen_memory": [],
    "execution_strategy": "single",
    "max_tool_rounds": 16,
    "context_max_bytes": 12000,
}
USAGE_FIELDS = ("input_tokens", "output_tokens", "confirmed_tokens", "reserved_tokens", "unknown_calls", "call_count")


def _sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _known_usage(usage: Any) -> bool:
    return isinstance(usage, dict) and usage.get("state") == "known" and all(type(usage.get(field)) is int and usage[field] >= 0 for field in USAGE_FIELDS) and usage["confirmed_tokens"] == usage["input_tokens"] + usage["output_tokens"]


def _verified_retry(strategy: dict[str, Any], receipt: dict[str, Any]) -> bool:
    retry = strategy.get("retry_evidence")
    if not isinstance(retry, dict) or retry.get("bound_enforced") is not True:
        return False
    count, attempts = retry.get("attempt_count"), retry.get("attempts")
    if type(count) is not int or count not in (1, 2) or not isinstance(attempts, list) or len(attempts) != count or strategy.get("visible_verification_count") != count or retry.get("retry_observed") is not (count == 2):
        return False
    references: set[str] = set()
    calls: set[str] = set()
    for attempt in attempts:
        if not isinstance(attempt, dict) or attempt.get("session_id") != receipt.get("session_id") or attempt.get("run_id") != receipt.get("product_run_id"):
            return False
        call, code, state = attempt.get("tool_call_id"), attempt.get("exit_code"), attempt.get("state")
        if not isinstance(call, str) or not call or call in calls or type(code) is not int or state != ("passed" if code == 0 else "failed"):
            return False
        calls.add(call)
        for field, width in (("command_sha256", 64), ("pending_event_id", 32), ("pending_checksum", 64), ("dispatch_event_id", 32), ("dispatch_checksum", 64), ("result_event_id", 32), ("result_checksum", 64)):
            value = attempt.get(field)
            if not isinstance(value, str) or re.fullmatch(r"[0-9a-f]{" + str(width) + "}", value) is None:
                return False
            if field.endswith("event_id"):
                if value in references:
                    return False
                references.add(value)
    return attempts[0]["state"] == ("passed" if count == 1 else "failed")


def _known_model_identity_consistent(receipt: dict[str, Any]) -> bool:
    identity = receipt.get("model_identity")
    if identity is None:
        return True
    runtime = receipt.get("product_runtime")
    profile = runtime.get("model_profile") if isinstance(runtime, dict) else None
    if not isinstance(identity, dict) or not isinstance(profile, dict):
        return False
    expected = profile.get("expected_reported_model")
    models, profiles = identity.get("reported_models"), identity.get("effective_request_profiles")
    return (
        isinstance(expected, str) and bool(expected)
        and identity.get("requested_model") == receipt.get("model")
        and identity.get("expected_reported_model") == expected
        and isinstance(models, list) and all(model == expected for model in models)
        and isinstance(profiles, list) and all(observed == profile for observed in profiles)
    )


def _join_id(*parts: str) -> str:
    value = "--".join(parts)
    return value if len(value) <= 96 else value[:30] + "--" + hashlib.sha256(value.encode("utf-8")).hexdigest()


class _PinsChanged(ValueError):
    pass


class _TrialBudgetExceeded(ValueError):
    pass


class _ProductionContractError(ValueError):
    pass


class _ProductionGraderUnavailable(ValueError):
    pass


class _ProductionGradeError(ValueError):
    pass


class _CleanupError(ValueError):
    def __init__(self, message: str, stage: str, code: str, receipt: dict[str, Any]) -> None:
        super().__init__(message)
        self.details = {"stage": stage, "error_code": code, "receipt": receipt}


def _read_config(path: Path) -> dict[str, Any]:
    config = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(config, dict) or type(config.get("schema_version")) is not int or config["schema_version"] != 1:
        raise ValueError("config requires schema_version 1")
    for key in ("run_id", "model", "experiment"):
        if not isinstance(config.get(key), str) or not config[key].strip():
            raise ValueError(f"config requires {key}")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}", config["run_id"]):
        raise ValueError("run_id must be a safe directory name")
    if config["experiment"] not in ("product_trial", "context_memory", "budget", "recovery"):
        raise ValueError("unsupported experiment")
    if config.get("evidence_kind", "unknown") not in ("provider", "fixture", "unknown"):
        raise ValueError("evidence_kind must be provider, fixture or unknown")
    contract = config.get("runtime_contract", "legacy")
    if contract not in ("legacy", "production_opensandbox"):
        raise ValueError("runtime_contract must be legacy or production_opensandbox")
    config["runtime_contract"] = contract
    if contract == "production_opensandbox":
        root = config.get("approved_workspace_root")
        if not isinstance(root, str) or not root.strip():
            raise ValueError("production_opensandbox requires approved_workspace_root")
        approved = (path.parent / root).resolve(strict=True)
        if not approved.is_dir():
            raise ValueError("approved_workspace_root must be an existing directory")
        config["approved_workspace_root"] = str(approved)
    repeats = config.get("repeats", 1)
    if type(repeats) is not int or repeats < 1:
        raise ValueError("repeats must be a positive integer")
    config["repeats"] = repeats
    config["policy"] = {**DEFAULT_POLICY, **config.get("policy", {})}
    if config["policy"].get("force_compaction") is False:
        config["policy"].pop("force_compaction")
    budget = config.get("budget", {})
    if type(budget.get("max_tokens")) is not int or budget["max_tokens"] <= 0:
        raise ValueError("budget.max_tokens must be a positive integer")
    seconds = budget.get("wall_clock_seconds")
    if type(seconds) not in (int, float) or not math.isfinite(seconds) or seconds <= 0:
        raise ValueError("budget.wall_clock_seconds must be positive and finite")
    if config["experiment"] == "budget":
        points = config.get("budget_points")
        if not isinstance(points, list) or len(points) < 2 or any(type(value) is not int or not 0 < value <= 100_000_000 for value in points) or len(set(points)) != len(points):
            raise ValueError("budget experiment requires two or more distinct positive budget_points")
    if config["experiment"] == "recovery":
        windows = config.get("failure_windows", ["before_effect", "after_effect", "after_record"])
        if not isinstance(windows, list) or not windows or any(value not in {"before_effect", "after_effect", "after_record"} for value in windows) or len(set(windows)) != len(windows):
            raise ValueError("recovery requires distinct supported failure_windows")
        config["failure_windows"] = windows
    if not isinstance(config.get("tasks"), list) or not config["tasks"]:
        raise ValueError("config requires tasks")
    task_ids: set[str] = set()
    for task in config["tasks"]:
        task_id = task.get("task_id", "")
        if not isinstance(task_id, str) or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,95}", task_id):
            raise ValueError("task_id must be a safe identifier")
        if task_id in task_ids:
            raise ValueError("duplicate task_id")
        task_ids.add(task_id)
        if ("message" in task) == ("messages" in task):
            raise ValueError("task requires either message or messages")
        messages = task.get("messages", [task.get("message")])
        if not isinstance(messages, list) or not messages or any(not isinstance(message, str) or not message.strip() for message in messages):
            raise ValueError("task requires nonempty user messages")
        task["messages"], task["message"] = messages, messages[0]
        minimum = task.get("min_root_compactions", 0)
        if type(minimum) is not int or minimum < 0:
            raise ValueError("min_root_compactions must be a nonnegative integer")
        task["min_root_compactions"] = minimum
        workspace = (path.parent / task["workspace"]).resolve(strict=True)
        checks = (path.parent / task["checks_file"]).resolve(strict=True)
        if checks.is_relative_to(workspace):
            raise ValueError("hidden checks must be outside the Agent workspace")
        if contract == "production_opensandbox" and checks.is_relative_to(Path(config["approved_workspace_root"])):
            raise ValueError("production hidden checks must be outside the approved runtime root")
        assertions = _read_checks(checks)
        if contract == "production_opensandbox" and len(assertions["files"]) > 256:
            raise ValueError("production grader supports at most 256 frozen file assertions")
        task["workspace"] = str(workspace)
        task["checks_file"] = str(checks)
    return config


def _read_checks(path: Path) -> dict[str, Any]:
    checks = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(checks, dict) or type(checks.get("schema_version")) is not int or checks["schema_version"] != 1 or not isinstance(checks.get("files"), list) or not checks["files"]:
        raise ValueError("hidden checks require schema_version 1 and file assertions")
    for check in checks["files"]:
        if not isinstance(check, dict) or set(check) not in ({"path", "contains"}, {"path", "json_equals"}, {"path", "sha256"}):
            raise ValueError("hidden checks support only contains, json_equals or sha256 file assertions")
        value = check["path"]
        if not isinstance(value, str) or not value:
            raise ValueError("hidden checks require relative file paths")
        relative, windows = PurePosixPath(value.replace("\\", "/")), PureWindowsPath(value)
        if relative.is_absolute() or windows.drive or windows.is_reserved() or ".." in relative.parts or any(":" in part for part in relative.parts):
            raise ValueError("hidden checks require safe relative file paths")
        if "contains" in check and (not isinstance(check["contains"], str) or not check["contains"]):
            raise ValueError("hidden checks require a nonempty contains assertion")
        if "json_equals" in check:
            json.dumps(check["json_equals"], allow_nan=False)
        if "sha256" in check and (not isinstance(check["sha256"], str) or re.fullmatch(r"[0-9a-fA-F]{64}", check["sha256"]) is None):
            raise ValueError("hidden checks require a hexadecimal SHA-256 pin")
    return checks


def _build_plan(config: dict[str, Any], config_path: Path) -> dict[str, Any]:
    tasks = []
    trials = []
    arms = [("single", config["policy"])]
    budgets = [config["budget"]]
    if config["experiment"] == "context_memory":
        arms = [
            (f"{name}_memory_{'on' if memory_read else 'off'}", {**config["policy"], "context_strategy": strategy, "memory_read": memory_read})
            for name, strategy in (("retrieval", "retrieval_pre_read"), ("on_demand", "metadata_on_demand"))
            for memory_read in (False, True)
        ]
    elif config["experiment"] == "budget":
        arms = [(strategy, {**config["policy"], "execution_strategy": strategy}) for strategy in ("single", "verify_retry", "subagent")]
        budgets = [{**config["budget"], "max_tokens": ceiling} for ceiling in config["budget_points"]]
    elif config["experiment"] == "recovery":
        policy = {key: value for key, value in config["policy"].items() if key not in {"fault_window", "fault_tool"}}
        arms = [("no_fault", policy)] + [
            (f"{mode}_{window}", {**policy, "fault_window": window, "fault_tool": config.get("fault_tool", "Write")})
            for mode in ("continue", "restart") for window in config["failure_windows"]
        ]
    for task in config["tasks"]:
        task_pin = {
            "task_id": task["task_id"],
            "message_sha256": hashlib.sha256(task["message"].encode("utf-8")).hexdigest(),
            "messages_sha256": hashlib.sha256(json.dumps(task["messages"], ensure_ascii=False).encode("utf-8")).hexdigest(),
            "min_root_compactions": task["min_root_compactions"],
            "workspace_pin": source_pin(task["workspace"]),
            "checks_sha256": _sha256(Path(task["checks_file"])),
        }
        task_sha256 = hashlib.sha256(json.dumps(task_pin, sort_keys=True).encode("utf-8")).hexdigest()
        tasks.append({**task_pin, "task_sha256": task_sha256})
        for repeat, (arm, policy), budget in product(range(config["repeats"]), arms, budgets):
            parts = [task["task_id"], f"{repeat:03d}"]
            if config["experiment"] != "product_trial":
                parts.append(arm)
            if config["experiment"] == "budget":
                parts.append(str(budget["max_tokens"]))
            trial_id = _join_id(*parts)
            trials.append({
                "trial_id": trial_id,
                "product_request_id": _join_id(config["run_id"], trial_id),
                "restart_request_id": _join_id(config["run_id"], trial_id, "restart") if arm.startswith("restart_") else None,
                "task_id": task["task_id"],
                "experiment": config["experiment"],
                "runtime_contract": config["runtime_contract"],
                "arm": arm,
                "policy": policy,
                "restart_policy": {key: value for key, value in policy.items() if key not in {"fault_window", "fault_tool"}} if arm.startswith("restart_") else None,
                "context_strategy": policy["context_strategy"],
                "memory_read": policy["memory_read"],
                "repeat": repeat,
                "budget_tokens": budget["max_tokens"],
                "budget": budget,
                "task_sha256": task_sha256,
                "planned_turns": len(task["messages"]),
                "completed_turns": 0,
                "min_root_compactions": task["min_root_compactions"],
                "status": "not_run",
                "success": None,
                "tokens_in": None,
                "tokens_out": None,
                "cost": None,
                "cost_currency": None,
                "elapsed_seconds": None,
                "actually_interrupted": False,
                "fault_event_count": 0,
                "failure_window": policy.get("fault_window"),
                "recovery_mode": arm.split("_", 1)[0] if config["experiment"] == "recovery" and arm != "no_fault" else "none",
                "recovered": None,
                "duplicate_effects": None,
                "physical_execution_count": None,
                "root_compaction_count": None,
                "compaction_count": None,
                "ledger_valid": None,
                "receipt_path": "",
                "evidence_kind": config.get("evidence_kind", "unknown"),
                "provenance_verified": False,
            })
    return {
        "schema_version": 1,
        "run_id": config["run_id"],
        "config_sha256": _sha256(config_path),
        "source_pin": source_pin(REPOSITORY),
        "model": config["model"],
        "runtime_contract": config["runtime_contract"],
        "approved_workspace_root_sha256": hashlib.sha256(config["approved_workspace_root"].encode("utf-8")).hexdigest() if config.get("approved_workspace_root") else None,
        "budget": config["budget"],
        "policy": config["policy"],
        "tasks": tasks,
        "planned_trials": len(trials),
        "trials": trials,
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("plan", "run"))
    parser.add_argument("--config", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--agent-binary", type=Path)
    args = parser.parse_args(argv)
    try:
        config_path = args.config.resolve(strict=True)
        config = _read_config(config_path)
        plan = _build_plan(config, config_path)
        run_root = args.output.resolve() / config["run_id"]
        if args.action == "plan" and run_root.exists():
            raise ValueError("run output already exists; preserve it and choose a new run_id")
        for task in config["tasks"]:
            if run_root.is_relative_to(Path(task["workspace"])):
                raise ValueError("artifacts must be outside the Agent workspace")
        if args.action == "run":
            frozen_path = run_root / "plan.json"
            if not frozen_path.is_file() or json.loads(frozen_path.read_text(encoding="utf-8")) != plan:
                raise ValueError("inputs no longer match the frozen plan; no product dispatch")
            artifacts = RunArtifacts(config["run_id"], args.output)
            if artifacts.verify_checksums():
                raise ValueError("frozen plan artifacts failed checksum verification")
            if (run_root / "predictions.jsonl").exists():
                raise ValueError("trial results already exist; preserve the original run")
            if args.agent_binary is None:
                raise ValueError("run requires --agent-binary; direct-model fallback is forbidden")
            return _run(config, plan, artifacts, args.agent_binary.resolve(strict=True), config_path)
        artifacts = RunArtifacts(config["run_id"], args.output)
        artifacts.write("plan.json", plan)
        artifacts.write_manifest({key: value for key, value in plan.items() if key != "trials"})
        artifacts.write_environment()
        artifacts.write_checksums()
        if artifacts.verify_checksums():
            raise ValueError("artifact checksum verification failed")
        print(json.dumps({"status": "planned", "planned_trials": plan["planned_trials"], "artifact_root": str(artifacts.root)}))
        return 0
    except (OSError, ValueError, KeyError, TypeError) as exc:
        print(f"experiment refused: {exc}", file=sys.stderr)
        return 2


def _production_workspace(receipt: dict[str, Any], request: dict[str, Any], approved_root: Path | None) -> Path:
    if approved_root is None or receipt.get("runtime_contract") != "production_opensandbox" or receipt.get("product_path") != "opensandbox-production" or receipt.get("source_workspace") != request["workspace"]:
        raise _ProductionContractError("production runtime contract or source workspace mismatch")
    task = receipt.get("task_workspace")
    if not isinstance(task, dict) or task.get("verified") is not True or task.get("approved_root") != str(approved_root):
        raise _ProductionContractError("Go-owned TaskWorkspace identity is not verified")
    workspace_id = task.get("workspace_id")
    if not isinstance(workspace_id, str) or re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}", workspace_id) is None or task.get("lease_id") != workspace_id:
        raise _ProductionContractError("TaskWorkspace lease identity is invalid")
    actual = receipt.get("workspace")
    if not isinstance(actual, str) or task.get("working_dir") != actual:
        raise _ProductionContractError("TaskWorkspace output directory mismatch")
    path = Path(actual)
    if not path.is_absolute() or path != approved_root / workspace_id or path == Path(request["workspace"]) or path.resolve(strict=True) != path or not path.is_dir():
        raise _ProductionContractError("TaskWorkspace output escapes its approved owned directory")
    for field in ("baseline_checksum", "prepared_event_checksum"):
        if not isinstance(task.get(field), str) or re.fullmatch(r"[0-9a-f]{64}", task[field]) is None:
            raise _ProductionContractError("TaskWorkspace lacks canonical preparation checksums")
    if not isinstance(task.get("prepared_event_id"), str) or re.fullmatch(r"[0-9a-f]{32}", task["prepared_event_id"]) is None:
        raise _ProductionContractError("TaskWorkspace preparation event is missing")
    try:
        expiry = datetime.fromisoformat(task["lease_expires_at"].replace("Z", "+00:00"))
        if expiry.tzinfo is None:
            raise ValueError("lease expiry must include a timezone")
    except (KeyError, TypeError, AttributeError, ValueError) as exc:
        raise _ProductionContractError("TaskWorkspace lease expiry is invalid") from exc
    completed = receipt.get("task_state") in {"completed", "finalized", "graded"}
    if completed and request.get("operation") in {"run", "continue"} and task.get("admission_ready") is not True:
        raise _ProductionContractError("production model workspace admission was not confirmed")
    pins = receipt.get("opensandbox")
    if not isinstance(pins, dict):
        raise _ProductionContractError("OpenSandbox pins are missing")
    for field in ("manifest_sha256", "policy_sha256", "server_container_id"):
        if not isinstance(pins.get(field), str) or re.fullmatch(r"[0-9a-f]{64}", pins[field]) is None:
            raise _ProductionContractError("OpenSandbox control identity is not pinned")
    if not isinstance(pins.get("source_sha"), str) or re.fullmatch(r"[0-9a-f]{40}|[0-9a-f]{64}", pins["source_sha"]) is None:
        raise _ProductionContractError("OpenSandbox source is not pinned")
    for field in ("server_image", "execd_image", "egress_image", "task_image"):
        if not isinstance(pins.get(field), str) or re.fullmatch(r"sha256:[0-9a-f]{64}", pins[field]) is None:
            raise _ProductionContractError("OpenSandbox image is not pinned")
    if completed and request.get("operation") != "grade":
        execution = receipt.get("execution_evidence")
        if not isinstance(execution, dict) or execution.get("verified") is not True or not isinstance(execution.get("tools"), list):
            raise _ProductionContractError("OpenSandbox execution evidence is not verified")
        runtimes = []
        for tool in execution["tools"]:
            if not isinstance(tool, dict):
                raise _ProductionContractError("invalid production tool evidence")
            runtime_id = tool.get("runtime_id")
            if not runtime_id and tool.get("tool_name") not in {"Bash", "Git", "Hook"}:
                continue
            result_bound = (
                isinstance(tool.get("result_event_id"), str)
                and re.fullmatch(r"[0-9a-f]{32}", tool["result_event_id"]) is not None
                and isinstance(tool.get("result_event_checksum"), str)
                and re.fullmatch(r"[0-9a-f]{64}", tool["result_event_checksum"]) is not None
            )
            # Go can reject a call before dispatch; its canonical error has no runtime to stop.
            if (
                runtime_id == "" and tool.get("is_error") is True
                and tool.get("outcome_unknown") is False and result_bound
                and all(
                    type(tool.get(field)) is int and tool[field] == count
                    for field, count in (("dispatch_count", 0), ("result_count", 1), ("unknown_count", 0), ("commit_done_count", 0))
                )
            ):
                continue
            if not isinstance(runtime_id, str) or re.fullmatch(r"[0-9a-f]{64}/[0-9a-f-]{36}/[0-9a-f]{64}", runtime_id) is None or tool.get("stopped") is not True or tool.get("outcome_unknown") is not False or not result_bound:
                raise _ProductionContractError("production tool runtime or known termination is unconfirmed")
            runtimes.append(runtime_id)
    return path


def _fault_boundary(event: dict[str, Any]) -> dict[str, Any]:
    boundary = event.get("fault_boundary")
    if not isinstance(boundary, dict):
        raise ValueError("fault readiness lacks a canonical boundary reference")
    reference, process = boundary.get("ready"), boundary.get("process")
    if (
        type(boundary.get("schema_version")) is not int or boundary["schema_version"] != 1
        or any(boundary.get(key) != event.get(key) for key in ("window", "session_id", "product_run_id", "tool_call_id", "tool_name"))
        or not isinstance(reference, dict) or reference.get("session_id") != event.get("session_id")
        or not isinstance(reference.get("event_id"), str) or not reference["event_id"]
        or type(reference.get("seq")) is not int or reference["seq"] < 0
        or not isinstance(reference.get("checksum"), str) or re.fullmatch(r"[0-9a-f]{64}", reference["checksum"]) is None
        or not isinstance(process, dict) or type(process.get("pid")) is not int or process["pid"] != event.get("pid")
        or process.get("platform") not in {"windows", "linux", "darwin"}
        or not isinstance(process.get("host"), str) or re.fullmatch(r"[0-9a-f]{64}", process["host"]) is None
        or not isinstance(process.get("started"), str) or not process["started"]
        or type(boundary.get("hold_deadline_unix_nano")) is not int
    ):
        raise ValueError("fault readiness has an invalid canonical boundary")
    return boundary


def _confirm_held_fault(interrupted: _TrialInterrupted, receipt: dict[str, Any]) -> dict[str, Any]:
    boundary = _fault_boundary(interrupted.event)
    evidence = receipt.get("execution_evidence")
    proofs = evidence.get("fault_boundaries") if isinstance(evidence, dict) else None
    matches = [proof for proof in proofs if isinstance(proof, dict) and all(proof.get(key) == value for key, value in boundary.items())] if isinstance(proofs, list) else []
    if (
        len(matches) != 1 or not isinstance(evidence, dict) or evidence.get("verified") is not True
        or receipt.get("task_state") != "running" or receipt.get("run_state") not in {"running", "leased"}
        or not 0 < interrupted.termination_started <= interrupted.terminated_at < boundary["hold_deadline_unix_nano"]
    ):
        raise ValueError("physical termination is not confirmed inside the held fault boundary")
    proof = matches[0]
    if (
        any(proof.get(key) is not True for key in ("verified", "result_order_verified", "process_stopped"))
        or any(proof.get(key) is not False for key in ("released", "run_terminal", "cancel_requested"))
        or proof.get("release") is not None
    ):
        raise ValueError("fault boundary was released, canceled or unverified before termination")
    return proof


def _product_call(binary: Path, request: dict[str, Any], timeout: float, artifacts: RunArtifacts, stem: str, *, approved_workspace_root: Path | None = None) -> tuple[subprocess.CompletedProcess[str], dict[str, Any]]:
    creationflags = subprocess.CREATE_NEW_PROCESS_GROUP | subprocess.CREATE_NO_WINDOW if os.name == "nt" else 0
    process = subprocess.Popen(
        [str(binary), "--eval-trial"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, text=True, encoding="utf-8", errors="replace", creationflags=creationflags, start_new_session=os.name != "nt",
    )
    tree = None
    timed_out = False
    chunks: dict[str, list[str]] = {"stdout": [], "stderr": []}
    messages: queue.Queue[tuple[str, str | None]] = queue.Queue()
    readers = []
    interruption: dict[str, Any] | None = None
    first_started: dict[str, Any] | None = None
    failure: Exception | None = None
    owned_tree_stopped = False
    termination_started = terminated_at = 0

    def read_stream(stream: Any, name: str) -> None:
        try:
            for line in stream:
                chunks[name].append(line)
                messages.put((name, line))
        finally:
            messages.put((name, None))

    try:
        tree = _OwnedProcessTree(process)
        for name, stream in (("stdout", process.stdout), ("stderr", process.stderr)):
            reader = threading.Thread(target=read_stream, args=(stream, name), daemon=True)
            reader.start()
            readers.append(reader)
        assert process.stdin is not None
        process.stdin.write(json.dumps(request))
        process.stdin.close()
        deadline, closed = time.monotonic() + timeout, 0
        while closed != 2:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise subprocess.TimeoutExpired(process.args, timeout)
            try:
                channel, line = messages.get(timeout=remaining)
            except queue.Empty as exc:
                raise subprocess.TimeoutExpired(process.args, timeout) from exc
            if line is None:
                closed += 1
                continue
            if sum(sum(map(len, values)) for values in chunks.values()) > 16 * 1024 * 1024:
                raise ValueError("product driver exceeded the protocol output bound")
            if channel != "stdout":
                continue
            try:
                message = json.loads(line)
            except json.JSONDecodeError:
                continue
            if not isinstance(message, dict):
                continue
            if message.get("type") == "started":
                if request.get("runtime_contract") == "production_opensandbox":
                    if (
                        first_started is not None
                        or type(message.get("pid")) is not int or message["pid"] != process.pid
                        or type(message.get("schema_version")) is not int or message["schema_version"] != 1
                        or message.get("execution_path") != "go-session-runner-grpc"
                        or any(message.get(field) != request.get(field) for field in ("run_id", "task_id", "model", "budget", "lifecycle", "turn_id"))
                        or message.get("effective_policy", message.get("policy")) != request["policy"]
                        or not isinstance(message.get("session_id"), str) or not message["session_id"]
                        or not isinstance(message.get("product_run_id"), str) or not message["product_run_id"]
                        or request.get("session_id") is not None and message["session_id"] != request["session_id"]
                    ):
                        raise ValueError("product started identity does not match the owned process and frozen request")
                    _production_workspace(message, request, approved_workspace_root)
                first_started = message
            if message.get("type") != "fault_ready":
                continue
            matches = (
                first_started is not None
                and request.get("operation") == "run"
                and message.get("schema_version") == 1
                and type(message.get("pid")) is int and message["pid"] == process.pid
                and first_started.get("pid") == process.pid
                and message.get("run_id") == request["run_id"] and message.get("task_id") == request["task_id"]
                and message.get("window") == request["policy"].get("fault_window")
                and bool(message.get("session_id")) and message["session_id"] == first_started.get("session_id")
                and bool(message.get("product_run_id")) and message["product_run_id"] == first_started.get("product_run_id")
                and bool(message.get("tool_call_id"))
                and message.get("tool_name") == request["policy"].get("fault_tool")
                and process.poll() is None
            )
            if not matches:
                raise ValueError("fault_ready does not identify the owned process and frozen boundary")
            boundary = _fault_boundary(message)
            termination_started = time.time_ns()
            if termination_started >= boundary["hold_deadline_unix_nano"]:
                raise ValueError("fault readiness expired before the owned termination request")
            tree.stop()
            terminated_at = time.time_ns()
            interruption = message
            deadline = time.monotonic() + 10
        process.wait(timeout=max(0.01, deadline - time.monotonic()))
    except subprocess.TimeoutExpired as exc:
        timed_out = True
        failure = exc
    except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as exc:
        failure = exc
    finally:
        try:
            if tree is not None:
                tree.close()
                owned_tree_stopped = process.poll() is not None
            elif process.poll() is None:
                process.kill()
                process.wait(timeout=10)
        except (OSError, TimeoutError, subprocess.SubprocessError) as exc:
            failure = failure or exc
        for reader in readers:
            reader.join(timeout=5)
        for stream in (process.stdin, process.stdout, process.stderr):
            if stream is not None:
                stream.close()
        artifacts.record_scorer_output(f"{stem}.ndjson", "".join(chunks["stdout"]))
        artifacts.record_scorer_output(f"{stem}-stderr.txt", "".join(chunks["stderr"]))
        artifacts.write(f"processes/{stem}.json", {"pid": process.pid, "tree": "windows_job" if os.name == "nt" else "posix_process_group", "timed_out": timed_out, "exit_code": process.returncode, "started": first_started, "owned_tree_stopped": owned_tree_stopped, "fault_ready": interruption, "physical_termination_confirmed": interruption is not None, "termination_started_unix_nano": termination_started, "terminated_at_unix_nano": terminated_at, "confirmed": False})
    if failure is not None:
        if request.get("runtime_contract") == "production_opensandbox" and request.get("operation") in {"run", "continue"}:
            raise _ProductCallFailed(failure, request, first_started, f"processes/{stem}.json", owned_tree_stopped) from failure
        raise failure
    if interruption is not None:
        raise _TrialInterrupted(interruption, f"processes/{stem}.json", termination_started, terminated_at)
    result = subprocess.CompletedProcess(process.args, process.returncode, "".join(chunks["stdout"]), "".join(chunks["stderr"]))
    try:
        receipts = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
        if any(not isinstance(item, dict) for item in receipts):
            raise ValueError("product driver emitted a non-object protocol message")
        receipts = [item for item in receipts if item.get("type") == "receipt"]
        if len(receipts) != 1:
            raise ValueError("product driver must emit exactly one receipt")
        receipt = receipts[0]
        refusal = receipt.get("task_state") in {"blocked", "infra_error"} and not receipt.get("session_id") and bool(receipt.get("error_code"))
        if (
            type(receipt.get("schema_version")) is not int or receipt["schema_version"] != 1
            or receipt.get("execution_path") != "go-session-runner-grpc"
            or receipt.get("run_id") != request["run_id"]
            or receipt.get("task_id") != request["task_id"]
            or receipt.get("model") != request["model"]
            or receipt.get("budget") != request["budget"]
            or receipt.get("effective_policy", receipt.get("policy")) != request["policy"]
            or not refusal and any(receipt.get(field, "") != request.get(field, "") for field in ("lifecycle", "turn_id"))
            or request.get("runtime_contract") != "production_opensandbox" and not (refusal and not receipt.get("workspace")) and Path(receipt.get("workspace", "")).resolve() != Path(request["workspace"])
        ):
            raise ValueError("product receipt identity mismatch; direct-model fallback is forbidden")
        if request.get("runtime_contract") == "production_opensandbox" and not refusal and (receipt.get("task_state") in {"completed", "finalized", "graded"} or request.get("operation") in {"inspect", "grade", "finalize"} or receipt.get("task_state") in {"blocked", "infra_error"} and not receipt.get("product_run_id")):
            _production_workspace(receipt, request, approved_workspace_root)
        if receipt.get("task_state") in {"completed", "graded"} and result.returncode != 0:
            raise ValueError("product driver failed after reporting completion")
        return result, receipt
    except (OSError, ValueError, KeyError, TypeError) as exc:
        if request.get("runtime_contract") == "production_opensandbox" and request.get("operation") in {"run", "continue"}:
            raise _ProductCallFailed(exc, request, first_started, f"processes/{stem}.json", owned_tree_stopped) from exc
        raise


class _ProductCallFailed(ValueError):
    def __init__(self, cause: Exception, request: dict[str, Any], started: dict[str, Any] | None, record_path: str, owned_tree_stopped: bool) -> None:
        super().__init__(str(cause))
        self.request, self.started, self.record_path, self.owned_tree_stopped = request, started, record_path, owned_tree_stopped
        self.error_code = "production_runtime_invalid" if isinstance(cause, _ProductionContractError) else type(cause).__name__


class _TrialInterrupted(Exception):
    def __init__(self, event: dict[str, Any], record_path: str, termination_started: int, terminated_at: int) -> None:
        super().__init__("owned process terminated after fault readiness; canonical confirmation pending")
        self.event, self.record_path = event, record_path
        self.termination_started, self.terminated_at = termination_started, terminated_at




def _execution_status(receipt: dict[str, Any]) -> str:
    state, code = receipt.get("task_state"), receipt.get("error_code", "")
    if state == "budget_exceeded" or "budget" in code:
        return "budget_exceeded"
    if code.startswith("provider_") or code in {
        "provider_model_identity_unconfirmed", "production_runtime_unavailable",
        "production_workspace_unavailable", "production_child_workspace_unavailable",
        "production_workspace_evidence_unavailable", "recovery_process_stop_unconfirmed",
        "tool_outcome_unknown",
    }:
        return "infra_error"
    if state == "completed":
        return "infra_error" if code else "completed"
    usage = receipt.get("usage", {})
    diagnostic = receipt.get("diagnostic") or {}
    if not isinstance(diagnostic, dict):
        diagnostic = {}
    if usage.get("call_count", 0) == 0 or diagnostic.get("stage", receipt.get("stage")) in {"orchestrator_startup", "transport", "preflight"}:
        return "infra_error"
    return "agent_failure" if state in ("failed", "canceled") else "infra_error"


def _record_trial_usage(row: dict[str, Any], usage: Any, interrupted_usage: dict[str, Any] | None) -> None:
    row["usage"] = usage
    if not isinstance(usage, dict) or usage.get("state") == "known" and not _known_usage(usage):
        row["error_code"] = "invalid_usage_receipt"
        raise ValueError("known usage receipt lacks complete consistent integer accounting")
    if interrupted_usage is not None:
        row["restart_usage"] = usage
        if usage.get("state") == "known":
            usage = {"state": "known", "source": "sum_of_session_receipts", **{field: interrupted_usage[field] + usage[field] for field in USAGE_FIELDS}, "cost_status": "unknown"}
        else:
            count = usage.get("unknown_calls")
            usage = {"state": "unknown", "source": "sum_of_session_receipts", "unknown_calls": interrupted_usage["unknown_calls"] + count if type(count) is int else None, "cost_status": "unknown"}
    row["usage"] = usage
    row["tokens_in"], row["tokens_out"] = None, None
    if usage.get("state") == "known":
        row["tokens_in"], row["tokens_out"] = usage["input_tokens"], usage["output_tokens"]
        if interrupted_usage is not None and usage["confirmed_tokens"] + usage["reserved_tokens"] > row["budget_tokens"]:
            raise _TrialBudgetExceeded("combined attempts exceed the frozen trial token ceiling")


def _score(checks_path: Path, workspace: Path) -> dict[str, Any]:
    checks = _read_checks(checks_path)
    outcomes = []
    for check in checks["files"]:
        relative = Path(check["path"])
        candidate = (workspace / relative).resolve()
        if relative.is_absolute() or not candidate.is_relative_to(workspace):
            raise ValueError("final check path escaped the Agent workspace")
        matched = False
        if candidate.is_file():
            if "json_equals" in check:
                try:
                    actual = json.loads(candidate.read_text(encoding="utf-8"))
                    matched = json.dumps(actual, sort_keys=True, allow_nan=False) == json.dumps(check["json_equals"], sort_keys=True, allow_nan=False)
                except (UnicodeError, ValueError):
                    matched = False
            elif "contains" in check:
                matched = check["contains"] in candidate.read_text(encoding="utf-8")
            elif "sha256" in check:
                matched = _sha256(candidate) == check["sha256"].lower()
            else:
                raise ValueError("unsupported final file assertion")
        outcomes.append({"path": check["path"], "passed": matched})
    return {"success": all(item["passed"] for item in outcomes), "checks": outcomes}


def _grade_production(artifacts: RunArtifacts, row: dict[str, Any], task: dict[str, Any], receipt: dict[str, Any], workspace: Path, request: dict[str, Any], validation: dict[str, Any], call_product: Callable[[dict[str, Any], float, str], tuple[subprocess.CompletedProcess[str], dict[str, Any]]]) -> dict[str, Any]:
    if Path(task["checks_file"]).is_relative_to(workspace):
        raise ValueError("hidden grader inputs must remain outside the Agent TaskWorkspace")
    checks_sha = _sha256(Path(task["checks_file"]))
    grade_request = {**request, "operation": "grade", "session_id": receipt["session_id"], "grade_checks_path": task["checks_file"], "grade_checks_sha256": checks_sha, "expected_session_head_checksum": receipt["ledger"]["head_checksum"]}
    artifacts.write(f"trials/{row['trial_id']}/grade-request.json", grade_request)
    result, graded = call_product(grade_request, 660, f"{row['trial_id']}-grade")
    name = f"trials/{row['trial_id']}/grade-receipt.json"
    artifacts.write(name, graded)
    validation["grade_receipt"] = {"path": name, "sha256": _sha256(artifacts.root / name)}
    row["grade_source_status"] = graded.get("task_state")
    row["grade_source_error_code"] = graded.get("error_code", "")
    row["grade_usage"] = graded.get("usage")
    grade = graded.get("grade")
    if not isinstance(grade, dict):
        raise _ProductionGraderUnavailable("Go-managed independent production grader is not connected")
    row["grade"] = grade
    validation["checks"]["independent_production_grade_verified"] = False
    if result.returncode != 0 or graded.get("task_state") != "graded" or grade.get("state") != "completed":
        raise _ProductionGradeError("production grader did not return a complete known verdict")
    if graded.get("session_id") != receipt["session_id"] or graded.get("product_run_id") != receipt["product_run_id"]:
        raise _ProductionGradeError("production grader changed the model execution identity")
    grade_usage = graded.get("usage")
    if not isinstance(grade_usage, dict) or any(grade_usage.get(field) != receipt.get("usage", {}).get(field) for field in USAGE_FIELDS):
        raise _ProductionGradeError("grading changed model accounting; reconcile the retained receipts")
    total, passed = grade.get("checks_total"), grade.get("checks_passed")
    if type(total) is not int or total != len(_read_checks(Path(task["checks_file"]))["files"]) or type(passed) is not int or not 0 <= passed <= total or type(grade.get("passed")) is not bool or grade["passed"] != (passed == total):
        raise _ProductionGradeError("production grader verdict or complete check denominator is invalid")
    for field in ("candidate_sha256", "candidate_archive_sha256", "input_sha256", "checker_sha256", "checks_sha256", "output_sha256", "operation_id", "intent_event_checksum", "result_event_checksum"):
        if not isinstance(grade.get(field), str) or re.fullmatch(r"[0-9a-f]{64}", grade[field]) is None:
            raise _ProductionGradeError("production grader lacks pinned input/output or canonical references")
    for field in ("intent_event_id", "result_event_id"):
        if not isinstance(grade.get(field), str) or re.fullmatch(r"[0-9a-f]{32}", grade[field]) is None:
            raise _ProductionGradeError("production grader lacks canonical event identities")
    runtime_id = grade.get("runtime_id")
    if grade["checks_sha256"] != checks_sha or not isinstance(runtime_id, str) or re.fullmatch(grade["operation_id"] + r"/[0-9a-f-]{36}/[0-9a-f]{64}", runtime_id) is None or grade.get("stopped") is not True or type(grade.get("exit_code")) is not int or grade["exit_code"] != 0:
        raise _ProductionGradeError("production grader inputs or independent runtime termination are unconfirmed")
    if any(tool.get("runtime_id") == runtime_id for tool in receipt.get("execution_evidence", {}).get("tools", [])):
        raise _ProductionGradeError("hidden grading reused an Agent tool runtime")
    ledger = graded.get("ledger", {})
    if ledger.get("verified") is not True or ledger.get("head_event_id") != grade["result_event_id"] or ledger.get("head_checksum") != grade["result_event_checksum"]:
        raise _ProductionGradeError("production grader result is not the canonical receipt head")
    inspect_request = {**request, "operation": "inspect", "session_id": receipt["session_id"]}
    inspect_request.pop("expected_session_head_checksum", None)
    inspected_result, inspected = call_product(inspect_request, 30, f"{row['trial_id']}-grade-inspect")
    inspect_name = f"trials/{row['trial_id']}/grade-inspect-receipt.json"
    artifacts.write(inspect_name, inspected)
    validation["grade_inspect_receipt"] = {"path": inspect_name, "sha256": _sha256(artifacts.root / inspect_name)}
    if inspected_result.returncode != 0 or inspected.get("session_id") != receipt["session_id"] or inspected.get("ledger", {}).get("verified") is not True or inspected.get("ledger", {}).get("head_checksum") != grade["result_event_checksum"]:
        raise _ProductionGradeError("independent readback did not verify the canonical grader result")
    validation["checks"]["independent_production_grade_verified"] = True
    validation["checks"]["opensandbox_execution_confirmed"] = True
    return {"success": grade["passed"], "checks_total": total, "checks_passed": passed, "candidate_sha256": grade["candidate_sha256"], "checks_sha256": checks_sha, "grade_receipt_sha256": validation["grade_receipt"]["sha256"]}


def _clone_workspace(baseline: str, pin: dict[str, Any], workspace: Path, artifacts: RunArtifacts, stem: str) -> None:
    if pin["untracked_files"] or pin["dirty_hash"] != hashlib.sha256(b"").hexdigest() or source_pin(baseline) != pin:
        raise _PinsChanged("matrix baseline must match its clean pinned Git HEAD")
    if workspace.exists():
        raise ValueError("trial workspace already exists; preserve the original")
    workspace.parent.mkdir(parents=True, exist_ok=True)
    for command in (
        ["git", "clone", "--quiet", "--no-hardlinks", "--no-checkout", "--", baseline, str(workspace)],
        ["git", "-C", str(workspace), "checkout", "--quiet", "--detach", pin["git_sha"]],
    ):
        try:
            subprocess.run(command, check=True, capture_output=True, text=True, encoding="utf-8", errors="replace")
        except subprocess.CalledProcessError as exc:
            artifacts.record_scorer_output(f"{stem}-workspace-stderr.txt", exc.stderr or "")
            raise
    if source_pin(workspace) != pin:
        raise _PinsChanged("isolated trial does not match the pinned baseline")


def _run(config: dict[str, Any], plan: dict[str, Any], artifacts: RunArtifacts, binary: Path, config_path: Path) -> int:
    binary_hash = _sha256(binary)
    artifacts.write_manifest({**{key: value for key, value in plan.items() if key != "trials"}, "agent_binary_sha256": binary_hash})
    rows = [dict(row) for row in plan["trials"]]
    validations = [{"trial_id": row["trial_id"], "task_id": row["task_id"], "status": "not_run", "checks": {}, "run_receipt": None, "inspect_receipt": None} for row in rows]
    task_by_id = {task["task_id"]: task for task in config["tasks"]}
    source_inputs: dict[str, dict[str, Any]] = {}
    production = config["runtime_contract"] == "production_opensandbox"
    approved_root = Path(config["approved_workspace_root"]) if production else None
    execution_workspaces: dict[str, Path] = {}

    def check_pins() -> None:
        try:
            if _sha256(config_path) != plan["config_sha256"]:
                raise _PinsChanged("frozen config changed")
            if _sha256(binary) != binary_hash:
                raise _PinsChanged("frozen driver binary changed")
            if source_pin(REPOSITORY) != plan["source_pin"]:
                raise _PinsChanged("frozen source changed")
            for pin in plan["tasks"]:
                if _sha256(Path(task_by_id[pin["task_id"]]["checks_file"])) != pin["checks_sha256"]:
                    raise _PinsChanged(f"frozen hidden checks changed: {pin['task_id']}")
                if production and source_pin(task_by_id[pin["task_id"]]["workspace"]) != pin["workspace_pin"]:
                    raise _PinsChanged("production source workspace changed")
            for source, expected in source_inputs.items():
                if source_pin(source) != expected:
                    raise _PinsChanged("production trial source input changed")
        except (OSError, subprocess.SubprocessError) as exc:
            raise _PinsChanged("frozen inputs cannot be verified") from exc

    prepared_workspaces: dict[str, Path] = {}
    clone_trials = config["experiment"] != "product_trial" or config["repeats"] != 1
    for row in rows:
        task = task_by_id[row["task_id"]]
        workspace = artifacts.root / "workspaces" / row["trial_id"] if clone_trials else Path(task["workspace"])
        prepared_workspaces[row["trial_id"]] = workspace
        row["workspace_relpath"] = workspace.relative_to(artifacts.root).as_posix() if clone_trials else None
    artifacts.write_jsonl("predictions.jsonl", rows)
    for row, validation in zip(rows, validations, strict=True):
        task = task_by_id[row["task_id"]]
        started = time.perf_counter()
        validation["status"] = "blocked"
        validation["checks"]["frozen_inputs_match"] = True
        source_inputs.clear()
        request = {
            "schema_version": 1,
            "run_id": row["product_request_id"],
            "task_id": row["task_id"],
            "workspace": str(prepared_workspaces[row["trial_id"]]),
            "message": task["message"],
            "model": config["model"],
            "budget": row["budget"],
            "policy": row["policy"],
            "operation": "run",
        }
        if production:
            request["runtime_contract"] = config["runtime_contract"]
        if len(task["messages"]) > 1 or production:
            request.update(lifecycle="hold", turn_id="turn-000")
        artifacts.write(f"trials/{row['trial_id']}/request.json", request)

        def call_product(current_request: dict[str, Any], timeout: float, stem: str) -> tuple[subprocess.CompletedProcess[str], dict[str, Any]]:
            validation["checks"][f"pins_before:{stem}"] = False
            check_pins()
            validation["checks"][f"pins_before:{stem}"] = True
            try:
                result, native = _product_call(binary, current_request, timeout, artifacts, stem, approved_workspace_root=approved_root)
                if production and native.get("task_state") in {"completed", "finalized", "graded"}:
                    actual = Path(native["workspace"])
                    execution_workspaces[row["trial_id"]] = actual
                    row["execution_workspace_sha256"] = hashlib.sha256(str(actual).encode("utf-8")).hexdigest()
                    row["product_path"] = native["product_path"]
                    row["task_workspace"] = native["task_workspace"]
                    row["opensandbox"] = native["opensandbox"]
                    validation["checks"]["production_workspace_owned"] = True
                    observed = native.get("execution_evidence")
                    if isinstance(observed, dict) and isinstance(observed.get("tools"), list):
                        count = sum(bool(tool.get("runtime_id")) for tool in observed["tools"])
                        row["model_opensandbox_executions"] = count
                        if count:
                            validation["checks"]["opensandbox_execution_confirmed"] = True
                return result, native
            except _TrialInterrupted as interrupted:
                row.update(actually_interrupted=None, fault_event_count=None, physical_termination_confirmed=True, interruption_record=interrupted.record_path, original_session_id=interrupted.event["session_id"])
                validation["interruption_record"] = {"path": interrupted.record_path, "sha256": _sha256(artifacts.root / interrupted.record_path)}
                validation["checks"]["owned_tree_termination"] = True
                raise
            finally:
                validation["checks"][f"pins_after:{stem}"] = False
                check_pins()
                validation["checks"][f"pins_after:{stem}"] = True

        try:
            row["stage"] = "workspace_preparation"
            check_pins()
            if clone_trials:
                pin = next(item["workspace_pin"] for item in plan["tasks"] if item["task_id"] == row["task_id"])
                _clone_workspace(task["workspace"], pin, prepared_workspaces[row["trial_id"]], artifacts, row["trial_id"])
            if production:
                source_inputs[request["workspace"]] = next(item["workspace_pin"] for item in plan["tasks"] if item["task_id"] == row["task_id"])
            validation["checks"]["workspace_prepared"] = True
            multi_turn = len(task["messages"]) > 1
            held_session = multi_turn or production
            validation["turn_receipts"] = []
            turn, restarted = 0, False
            interrupted_usage: dict[str, Any] | None = None
            while turn < len(task["messages"]):
                request = {**request, "message": task["messages"][turn], "operation": "run"}
                request.pop("expected_session_head_checksum", None)
                prefix = "restart-" if restarted else ""
                if held_session:
                    request.update(lifecycle="hold", turn_id=f"turn-{turn:03d}")
                    artifacts.write(f"trials/{row['trial_id']}/{prefix}turn-{turn:03d}-request.json", request)
                stem = f"{row['trial_id']}-{prefix}turn-{turn:03d}" if held_session else f"{row['trial_id']}-{prefix}driver"
                try:
                    result, receipt = call_product(request, max(0.01, row["budget"]["wall_clock_seconds"] - (time.perf_counter() - started)) + 30, stem)
                except _TrialInterrupted as interrupted:
                    interrupted_request = {**request, "operation": "inspect", "session_id": interrupted.event["session_id"]}
                    inspected_result, interrupted_receipt = call_product(interrupted_request, 30, f"{stem}-interrupted-inspect")
                    interrupted_name = f"trials/{row['trial_id']}/interrupted-inspect-receipt.json"
                    artifacts.write(interrupted_name, interrupted_receipt)
                    validation["interrupted_inspect_receipt"] = {"path": interrupted_name, "sha256": _sha256(artifacts.root / interrupted_name)}
                    if interrupted_receipt.get("product_run_id") != interrupted.event["product_run_id"]:
                        row["error_code"] = "interrupted_run_changed"
                        raise ValueError("inspection changed the interrupted product Run identity")
                    head = interrupted_receipt.get("ledger", {}).get("head_checksum")
                    validation["checks"]["interrupted_ledger_readback"] = inspected_result.returncode in (0, 1) and not interrupted_receipt.get("error_code") and interrupted_receipt.get("task_state") in {"queued", "running", "canceling", "completed", "failed", "canceled", "budget_exceeded"} and interrupted_receipt.get("session_id") == interrupted.event["session_id"] and interrupted_receipt.get("ledger", {}).get("verified") is True and isinstance(head, str) and len(head) == 64
                    if not validation["checks"]["interrupted_ledger_readback"]:
                        raise ValueError("interrupted Session could not be verified without execution")
                    row["error_code"] = "fault_boundary_unconfirmed"
                    validation["checks"]["canonical_held_fault_boundary"] = False
                    try:
                        proof = _confirm_held_fault(interrupted, interrupted_receipt)
                    except ValueError:
                        row["treatment_error"] = {"error_code": "fault_boundary_unconfirmed", "requested_window": row["failure_window"]}
                        raise
                    confirmation_name = f"trials/{row['trial_id']}/interruption-confirmation.json"
                    artifacts.write(confirmation_name, {"schema_version": 1, "confirmed": True, "physical_termination": validation["interruption_record"], "canonical_inspect": validation["interrupted_inspect_receipt"], "fault_boundary": proof})
                    validation["interruption_confirmation"] = {"path": confirmation_name, "sha256": _sha256(artifacts.root / confirmation_name)}
                    validation["checks"]["canonical_held_fault_boundary"] = True
                    validation["checks"]["owned_fault_termination"] = True
                    row.update(actually_interrupted=True, fault_event_count=1, fault_boundary_proof=proof, interruption_confirmation=confirmation_name)
                    row.pop("error_code", None)
                    if row["recovery_mode"] == "restart":
                        abandon_request = {**interrupted_request, "operation": "finalize", "finalize_mode": "abandon", "expected_session_head_checksum": head}
                        artifacts.write(f"trials/{row['trial_id']}/abandon-request.json", abandon_request)
                        abandoned_result, abandoned = call_product(abandon_request, 30, f"{stem}-abandon")
                        abandon_name = f"trials/{row['trial_id']}/abandon-receipt.json"
                        artifacts.write(abandon_name, abandoned)
                        validation["abandon_receipt"] = {"path": abandon_name, "sha256": _sha256(artifacts.root / abandon_name)}
                        validation["checks"]["interrupted_session_abandoned"] = abandoned_result.returncode == 0 and abandoned.get("task_state") == "finalized" and abandoned.get("admission_finalized") is True and abandoned.get("session_id") == interrupted.event["session_id"] and abandoned.get("ledger", {}).get("verified") is True
                        if not validation["checks"]["interrupted_session_abandoned"]:
                            row["error_code"] = abandoned.get("error_code") or "interrupted_session_not_abandoned"
                            raise ValueError("Go did not safely seal the interrupted Session")
                        row["admission_finalized"] = True
                        original_usage = abandoned.get("usage", {})
                        row["interrupted_usage"] = original_usage
                        row["original_execution_evidence"] = abandoned.get("execution_evidence", {})
                        if not _known_usage(original_usage) or original_usage["unknown_calls"] or original_usage["reserved_tokens"]:
                            row["error_code"] = "restart_budget_unknown"
                            raise ValueError("unknown interrupted usage cannot be assigned a zero-cost restart")
                        interrupted_usage = original_usage
                        remaining_tokens = row["budget_tokens"] - original_usage["confirmed_tokens"]
                        remaining_seconds = row["budget"]["wall_clock_seconds"] - (time.perf_counter() - started)
                        if remaining_tokens <= 0 or remaining_seconds <= 0:
                            raise _TrialBudgetExceeded("the original attempt exhausted the shared restart budget")
                        restart_workspace = artifacts.root / "restarts" / row["trial_id"]
                        check_pins()
                        _clone_workspace(task["workspace"], pin, restart_workspace, artifacts, f"{row['trial_id']}-restart")
                        if production:
                            source_inputs[str(restart_workspace)] = pin
                        row["original_workspace_relpath"] = row["workspace_relpath"]
                        row["workspace_relpath"] = restart_workspace.relative_to(artifacts.root).as_posix()
                        row["completed_turns_before_restart"] = row["completed_turns"]
                        row["completed_turns"] = 0
                        prepared_workspaces[row["trial_id"]] = restart_workspace
                        request = {**request, "run_id": row["restart_request_id"], "workspace": str(restart_workspace), "message": task["messages"][0], "policy": row["restart_policy"], "budget": {"max_tokens": remaining_tokens, "wall_clock_seconds": remaining_seconds}, "operation": "run"}
                        request.pop("session_id", None)
                        if held_session:
                            request["turn_id"] = "turn-000"
                        artifacts.write(f"trials/{row['trial_id']}/restart-request.json", request)
                        turn, restarted = 0, True
                        continue
                    request = {**request, "operation": "continue", "session_id": interrupted.event["session_id"], "expected_session_head_checksum": head}
                    artifacts.write(f"trials/{row['trial_id']}/continuation-request.json", request)
                    resumed_at = time.perf_counter()
                    result, receipt = call_product(request, max(0.01, row["budget"]["wall_clock_seconds"] - (resumed_at - started)) + 30, f"{stem}-continuation")
                    row["recovery_elapsed_seconds"] = time.perf_counter() - resumed_at
                    if receipt.get("session_id") != row["original_session_id"] or receipt.get("product_run_id") != interrupted.event["product_run_id"]:
                        row["error_code"] = "continuation_run_changed"
                        raise ValueError("recovery changed the original Session or product Run")
                if held_session:
                    turn_name = f"trials/{row['trial_id']}/{prefix}turn-{turn:03d}-receipt.json"
                    artifacts.write(turn_name, receipt)
                    validation["turn_receipts"].append({"path": turn_name, "sha256": _sha256(artifacts.root / turn_name), "phase": "restart" if restarted else "original"})
                if receipt.get("task_state") != "completed":
                    break
                row["completed_turns"] += 1
                request["session_id"] = receipt["session_id"]
                turn += 1
            receipt_name = f"trials/{row['trial_id']}/product-receipt.json"
            artifacts.write(receipt_name, receipt)
            validation["run_receipt"] = {"path": receipt_name, "sha256": _sha256(artifacts.root / receipt_name)}
            validation["checks"]["product_receipt_identity"] = True
            diagnostic = receipt.get("diagnostic") or {}
            if not isinstance(diagnostic, dict):
                diagnostic = {}
            row.update({"receipt_path": receipt_name, "source_status": receipt.get("task_state"), "source_error_code": receipt.get("error_code", ""), "session_id": receipt.get("session_id"), "product_run_id": receipt.get("product_run_id"), "trace_id": receipt.get("trace_id"), "error_code": receipt.get("error_code", ""), "diagnostic": diagnostic, "stage": diagnostic.get("stage", receipt.get("stage")), "grpc_status": diagnostic.get("grpc_status", receipt.get("grpc_status"))})
            row["status"] = _execution_status(receipt)
            if row["status"] in {"agent_failure", "budget_exceeded"}:
                row["success"] = False
                if row["actually_interrupted"] and row["recovery_mode"] == "continue":
                    row["recovered"] = False
                if restarted:
                    row["completed_from_start"] = False
            _record_trial_usage(row, receipt.get("usage", {}), interrupted_usage)
            row["execution_evidence"] = receipt.get("execution_evidence", {})
            row["strategy_evidence"] = receipt.get("strategy_evidence", {})
            if "model_identity" in receipt:
                row["model_identity"] = receipt["model_identity"]
            if row["failure_window"] is not None and not row["actually_interrupted"]:
                row["treatment_error"] = {"error_code": "fault_window_not_reached", "requested_window": row["failure_window"]}
                validation["checks"]["fault_window_reached"] = False
                if row["status"] == "completed":
                    row.update(status="infra_error", success=None, error_code="fault_window_not_reached")
            artifacts.write_jsonl("predictions.jsonl", rows)
            ledger_receipt = receipt
            request.pop("expected_session_head_checksum", None)
            if held_session and receipt.get("session_id") and receipt.get("task_state") in {"completed", "failed", "canceled", "budget_exceeded"} and (not production or row["status"] != "completed"):
                finalization_request = {**request, "operation": "finalize", "session_id": receipt["session_id"], "finalize_mode": "finish"}
                artifacts.write(f"trials/{row['trial_id']}/finalization-request.json", finalization_request)
                finalized_result, finalized = call_product(finalization_request, 30, f"{row['trial_id']}-finalize")
                finalization_name = f"trials/{row['trial_id']}/finalization-receipt.json"
                artifacts.write(finalization_name, finalized)
                validation["finalization_receipt"] = {"path": finalization_name, "sha256": _sha256(artifacts.root / finalization_name)}
                validation["checks"]["admission_finalized"] = finalized_result.returncode == 0 and finalized.get("task_state") == "finalized" and finalized.get("admission_finalized") is True and finalized.get("session_id") == receipt["session_id"] and finalized.get("run_state") == receipt.get("run_state", receipt.get("task_state"))
                if not validation["checks"]["admission_finalized"]:
                    raise _CleanupError("held Session was not safely finalized", "finalize", finalized.get("error_code") or "admission_finalization_unconfirmed", validation["finalization_receipt"])
                ledger_receipt = finalized
                row["admission_finalized"] = True
                if production:
                    final_inspect_request = {**request, "operation": "inspect", "session_id": receipt["session_id"]}
                    final_inspect_result, final_inspected = call_product(final_inspect_request, 30, f"{row['trial_id']}-final-inspect")
                    final_inspect_name = f"trials/{row['trial_id']}/final-inspect-receipt.json"
                    artifacts.write(final_inspect_name, final_inspected)
                    validation["final_inspect_receipt"] = {"path": final_inspect_name, "sha256": _sha256(artifacts.root / final_inspect_name)}
                    validation["checks"]["finalized_ledger_readback"] = final_inspect_result.returncode == 0 and final_inspected.get("session_id") == receipt["session_id"] and final_inspected.get("product_run_id") == receipt.get("product_run_id") and final_inspected.get("ledger", {}).get("verified") is True and bool(finalized.get("ledger", {}).get("head_checksum")) and final_inspected.get("ledger", {}).get("head_checksum") == finalized.get("ledger", {}).get("head_checksum")
                    if not validation["checks"]["finalized_ledger_readback"]:
                        raise _CleanupError("known terminal task finalization was not verified", "final_inspect", final_inspected.get("error_code") or "finalized_ledger_readback_unconfirmed", validation["final_inspect_receipt"])
            _record_trial_usage(row, ledger_receipt.get("usage", {}), interrupted_usage)
            execution = ledger_receipt.get("execution_evidence", {})
            row["execution_evidence"] = execution
            row["strategy_evidence"] = ledger_receipt.get("strategy_evidence", {})
            if execution.get("verified") is True:
                for field in ("root_compaction_count", "compaction_count", "duplicate_effects", "physical_execution_count"):
                    row[field] = execution.get(field)
            if interrupted_usage is not None:
                original_execution = row["original_execution_evidence"]
                for field in ("duplicate_effects", "physical_execution_count"):
                    before, after = original_execution.get(field), row[field]
                    row[field] = before + after if original_execution.get("verified") is True and type(before) is int and type(after) is int else None
            row["evidence_kind"] = config.get("evidence_kind", "unknown")
            if row["status"] == "completed":
                try:
                    if receipt.get("model_identity") is not None:
                        validation["checks"]["known_model_identity_consistent"] = _known_model_identity_consistent(receipt)
                        if not validation["checks"]["known_model_identity_consistent"]:
                            row.update(status="infra_error", success=None, error_code="provider_model_identity_unconfirmed")
                            artifacts.write_jsonl("predictions.jsonl", rows)
                            raise ValueError("a known cross-purpose model identity or request profile differs from the frozen profile")
                    inspect_request = {**request, "operation": "inspect", "session_id": receipt["session_id"]}
                    inspect_result, inspected = call_product(inspect_request, 30, f"{row['trial_id']}-inspect")
                    inspect_name = f"trials/{row['trial_id']}/inspect-receipt.json"
                    artifacts.write(inspect_name, inspected)
                    validation["inspect_receipt"] = {"path": inspect_name, "sha256": _sha256(artifacts.root / inspect_name)}
                    validation["checks"]["ledger_readback"] = inspect_result.returncode == 0 and inspected.get("ledger", {}).get("verified") is True
                    if not validation["checks"]["ledger_readback"]:
                        raise ValueError("product Ledger inspection did not verify the original Session")
                    validation["checks"]["same_ledger_head"] = inspected.get("session_id") == receipt["session_id"] and bool(inspected.get("ledger", {}).get("head_checksum")) and inspected.get("ledger", {}).get("head_checksum") == ledger_receipt.get("ledger", {}).get("head_checksum")
                    if not validation["checks"]["same_ledger_head"]:
                        raise ValueError("product inspection did not confirm the receipt Ledger head")
                    row["ledger_valid"] = True
                    if task["min_root_compactions"]:
                        observed = row["root_compaction_count"]
                        validation["checks"]["required_compaction_observed"] = type(observed) is int and observed >= task["min_root_compactions"]
                        if not validation["checks"]["required_compaction_observed"]:
                            row["error_code"] = "required_compaction_not_observed"
                            row["treatment_error"] = {"error_code": row["error_code"]}
                            raise ValueError("the required verified root compaction count was not observed")
                    effective = receipt.get("effective_policy", receipt.get("policy"))
                    validation["checks"]["effective_policy_and_budget"] = effective == request["policy"] and receipt.get("budget") == request["budget"]
                    if not validation["checks"]["effective_policy_and_budget"]:
                        raise ValueError("product effective policy or budget differs from the frozen plan")
                    row["context_strategy"], row["memory_read"] = effective["context_strategy"], effective["memory_read"]
                    if config["experiment"] == "budget":
                        strategy = row["strategy_evidence"]
                        children, child_calls = strategy.get("child_session_ids"), strategy.get("child_model_call_count")
                        observed = strategy.get("verified") is True and isinstance(children, list) and all(isinstance(child, str) and child for child in children) and len(children) == len(set(children)) and type(child_calls) is int and child_calls >= 0
                        if observed:
                            observed = bool(children) and child_calls > 0 if row["arm"] == "subagent" else not children and child_calls == 0
                        if row["arm"] == "verify_retry":
                            observed = observed and _verified_retry(strategy, receipt)
                        validation["checks"]["execution_strategy_observed"] = observed
                        if not observed:
                            row["error_code"] = "execution_strategy_not_observed"
                            row["treatment_error"] = {"error_code": row["error_code"]}
                            raise ValueError("the declared strategy lacks verified child or visible verification execution")
                    validation["checks"]["runtime_pins_unchanged"] = True
                    validation["checks"]["attributable_execution"] = all(isinstance(receipt.get(field), str) and bool(receipt[field]) for field in ("session_id", "product_run_id", "trace_id")) and ledger_receipt.get("usage", {}).get("call_count", 0) > 0
                    if not validation["checks"]["attributable_execution"]:
                        raise ValueError("product receipt lacks attributable model and trace identities")
                    try:
                        verdict = _grade_production(artifacts, row, task, ledger_receipt, execution_workspaces[row["trial_id"]], request, validation, call_product) if production else _score(Path(task["checks_file"]), prepared_workspaces[row["trial_id"]])
                        artifacts.write(f"trials/{row['trial_id']}/final-score.json", verdict)
                        row["success"] = verdict["success"]
                        if production and verdict["success"] is False:
                            row["error_code"] = "final_state_check_failed"
                        if row["actually_interrupted"] and row["recovery_mode"] == "continue":
                            row["recovered"] = bool(verdict["success"])
                        if restarted:
                            row["completed_from_start"] = bool(verdict["success"])
                        validation["checks"]["independent_scorer_completed"] = True
                        validation["status"] = "verified"
                        row["provenance_verified"] = row["evidence_kind"] == "provider"
                    except _ProductionGraderUnavailable:
                        row.update(status="scorer_error", success=None, error_code=row.get("grade_source_error_code") or "production_grader_unavailable")
                        validation["checks"]["independent_scorer_completed"] = False
                    except _ProductionGradeError as exc:
                        row.update(status="scorer_error", success=None, error_code=row["grade_source_error_code"] or "production_grade_invalid")
                        validation["error"] = str(exc)
                        validation["checks"]["independent_scorer_completed"] = False
                    except _ProductionContractError:
                        raise
                    except (OSError, ValueError, KeyError, TypeError) as exc:
                        row.update(status="scorer_error", error_code=type(exc).__name__)
                        validation["checks"]["independent_scorer_completed"] = False
                finally:
                    if production:
                        final_request = {**request, "operation": "finalize", "session_id": receipt["session_id"], "finalize_mode": "finish"}
                        artifacts.write(f"trials/{row['trial_id']}/finalization-request.json", final_request)
                        final_result, final_receipt = call_product(final_request, 30, f"{row['trial_id']}-finalize")
                        final_name = f"trials/{row['trial_id']}/finalization-receipt.json"
                        artifacts.write(final_name, final_receipt)
                        validation["finalization_receipt"] = {"path": final_name, "sha256": _sha256(artifacts.root / final_name)}
                        validation["checks"]["production_finalized"] = final_result.returncode == 0 and final_receipt.get("task_state") == "finalized" and final_receipt.get("admission_finalized") is True and final_receipt.get("session_id") == receipt["session_id"] and final_receipt.get("product_run_id") == receipt["product_run_id"] and final_receipt.get("run_state") == "completed"
                        if "grade_receipt" in validation:
                            validation["checks"]["production_finalized_after_grading"] = validation["checks"]["production_finalized"]
                        if not validation["checks"]["production_finalized"]:
                            grade = row.get("grade", {})
                            if row["status"] == "scorer_error" and grade.get("state") == "unknown" and grade.get("stopped") is not True:
                                row["cleanup_blocked"] = True
                                row["cleanup_error"] = {"error_code": final_receipt.get("error_code") or "production_finalization_unconfirmed", "receipt": validation["finalization_receipt"]}
                                artifacts.record_failure(row["trial_id"], "cleanup_blocked", row["cleanup_error"]["error_code"])
                            else:
                                raise _CleanupError("production task admission was not safely finalized", "finalize", final_receipt.get("error_code") or "production_finalization_unconfirmed", validation["finalization_receipt"])
                        else:
                            row["admission_finalized"] = True
                            final_inspect_request = {**request, "operation": "inspect", "session_id": receipt["session_id"]}
                            final_inspect_result, final_inspected = call_product(final_inspect_request, 30, f"{row['trial_id']}-final-inspect")
                            final_inspect_name = f"trials/{row['trial_id']}/final-inspect-receipt.json"
                            artifacts.write(final_inspect_name, final_inspected)
                            validation["final_inspect_receipt"] = {"path": final_inspect_name, "sha256": _sha256(artifacts.root / final_inspect_name)}
                            validation["checks"]["finalized_ledger_readback"] = final_inspect_result.returncode == 0 and final_inspected.get("ledger", {}).get("verified") is True and final_inspected.get("ledger", {}).get("head_checksum") == final_receipt.get("ledger", {}).get("head_checksum")
                            if not validation["checks"]["finalized_ledger_readback"]:
                                raise _CleanupError("final production admission head was not verified", "final_inspect", final_inspected.get("error_code") or "finalized_ledger_readback_unconfirmed", validation["final_inspect_receipt"])
        except _ProductCallFailed as exc:
            row.update(status="infra_error", success=None, provenance_verified=False, source_status="unknown", error_code=exc.error_code)
            validation["status"], validation["error"] = "blocked", str(exc)
            validation["started_record"] = {"path": exc.record_path, "sha256": _sha256(artifacts.root / exc.record_path)}
            artifacts.record_failure(row["trial_id"], "infra_error", str(exc))
            identity = exc.started
            if identity is not None:
                row.update(session_id=identity["session_id"], product_run_id=identity["product_run_id"], trace_id=identity.get("trace_id"))
            artifacts.write_jsonl("predictions.jsonl", rows)
            try:
                if identity is None or not exc.owned_tree_stopped:
                    raise _CleanupError("missing receipt cannot be reconciled without owned identity and process stop proof", "cleanup_preflight", "product_started_identity_unavailable" if identity is None else "owned_process_stop_unconfirmed", {})
                inspect_request = {**exc.request, "operation": "inspect", "session_id": identity["session_id"]}
                inspect_request.pop("expected_session_head_checksum", None)
                inspect_result, inspected = call_product(inspect_request, 30, f"{row['trial_id']}-missing-receipt-inspect")
                name = f"trials/{row['trial_id']}/missing-receipt-inspect-receipt.json"
                artifacts.write(name, inspected)
                validation["missing_receipt_inspect"] = {"path": name, "sha256": _sha256(artifacts.root / name)}
                head = inspected.get("ledger", {}).get("head_checksum")
                stable_task = ("approved_root", "working_dir", "workspace_id", "lease_id", "baseline_checksum", "prepared_event_id", "prepared_event_checksum")
                validation["checks"]["started_ledger_readback"] = inspect_result.returncode in (0, 1) and inspected.get("task_state") in {"queued", "running", "canceling", "completed", "failed", "canceled", "budget_exceeded"} and all(inspected.get(key) == identity.get(key) for key in ("session_id", "product_run_id", "workspace", "opensandbox")) and all(inspected["task_workspace"].get(key) == identity["task_workspace"].get(key) for key in stable_task) and inspected.get("ledger", {}).get("verified") is True and inspected["ledger"].get("session_id") == identity["session_id"] and isinstance(head, str) and re.fullmatch(r"[0-9a-f]{64}", head) is not None
                if not validation["checks"]["started_ledger_readback"]:
                    raise _CleanupError("canonical inspection did not confirm the owned started Session", "cleanup_inspect", "started_ledger_readback_unconfirmed", validation["missing_receipt_inspect"])
                _record_trial_usage(row, inspected.get("usage", {}), interrupted_usage)
                artifacts.write_jsonl("predictions.jsonl", rows)
                abandon_request = {**inspect_request, "operation": "finalize", "finalize_mode": "abandon", "expected_session_head_checksum": head}
                artifacts.write(f"trials/{row['trial_id']}/abandon-request.json", abandon_request)
                abandon_result, abandoned = call_product(abandon_request, 30, f"{row['trial_id']}-missing-receipt-abandon")
                name = f"trials/{row['trial_id']}/abandon-receipt.json"
                artifacts.write(name, abandoned)
                validation["abandon_receipt"] = {"path": name, "sha256": _sha256(artifacts.root / name)}
                validation["checks"]["admission_finalized"] = abandon_result.returncode == 0 and abandoned.get("task_state") == "finalized" and abandoned.get("admission_finalized") is True and abandoned.get("session_id") == identity["session_id"] and abandoned.get("product_run_id") == identity["product_run_id"] and isinstance(abandoned.get("run_state"), str) and bool(abandoned["run_state"]) and abandoned.get("ledger", {}).get("verified") is True and abandoned["ledger"].get("session_id") == identity["session_id"] and isinstance(abandoned["ledger"].get("head_checksum"), str) and re.fullmatch(r"[0-9a-f]{64}", abandoned["ledger"]["head_checksum"]) is not None
                if not validation["checks"]["admission_finalized"]:
                    raise _CleanupError("Go did not safely abandon the Session without a final receipt", "cleanup_abandon", abandoned.get("error_code") or "admission_finalization_unconfirmed", validation["abandon_receipt"])
                final_result, final_inspected = call_product(inspect_request, 30, f"{row['trial_id']}-final-inspect")
                name = f"trials/{row['trial_id']}/final-inspect-receipt.json"
                artifacts.write(name, final_inspected)
                validation["final_inspect_receipt"] = {"path": name, "sha256": _sha256(artifacts.root / name)}
                validation["checks"]["finalized_ledger_readback"] = final_result.returncode == 0 and final_inspected.get("task_state") == "finalized" and final_inspected.get("admission_finalized") is True and final_inspected.get("session_id") == identity["session_id"] and final_inspected.get("product_run_id") == identity["product_run_id"] and final_inspected.get("run_state") == abandoned["run_state"] and final_inspected.get("ledger", {}).get("verified") is True and final_inspected["ledger"].get("session_id") == identity["session_id"] and final_inspected["ledger"].get("head_checksum") == abandoned["ledger"]["head_checksum"]
                if not validation["checks"]["finalized_ledger_readback"]:
                    raise _CleanupError("abandoned Session head was not verified", "final_inspect", "finalized_ledger_readback_unconfirmed", validation["final_inspect_receipt"])
                row["admission_finalized"], row["ledger_valid"] = True, True
                _record_trial_usage(row, final_inspected.get("usage", {}), interrupted_usage)
            except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as cleanup_exc:
                row["cleanup_blocked"] = True
                row["cleanup_error"] = cleanup_exc.details if isinstance(cleanup_exc, _CleanupError) else {"stage": "missing_receipt_cleanup", "error_code": type(cleanup_exc).__name__}
                validation["cleanup_error"] = row["cleanup_error"]
                artifacts.record_failure(row["trial_id"], "cleanup_blocked", str(cleanup_exc))
        except _CleanupError as exc:
            row["cleanup_blocked"] = True
            row["cleanup_error"] = exc.details
            if row["status"] == "completed":
                row.update(status="infra_error", success=None, error_code=exc.details["error_code"])
            validation["status"] = "blocked"
            validation["cleanup_error"] = exc.details
            artifacts.record_failure(row["trial_id"], "cleanup_blocked", str(exc))
        except _TrialBudgetExceeded as exc:
            row.update(status="budget_exceeded", success=False, error_code="restart_budget_exhausted")
            validation["error"] = str(exc)
            artifacts.record_failure(row["trial_id"], "budget_exceeded", str(exc))
        except (OSError, ValueError, KeyError, TypeError, subprocess.SubprocessError) as exc:
            row.update(status="infra_error", success=None, provenance_verified=False, error_code="frozen_pins_changed" if isinstance(exc, _PinsChanged) else "production_runtime_invalid" if isinstance(exc, _ProductionContractError) else row.get("error_code") or type(exc).__name__)
            validation["status"] = "blocked"
            validation["error"] = str(exc)
            artifacts.record_failure(row["trial_id"], "infra_error", str(exc))
        row["elapsed_seconds"] = time.perf_counter() - started
        artifacts.write_jsonl("predictions.jsonl", rows)
        isolated_failure = row.get("admission_finalized") is True and (
            row.get("error_code") == "restart_budget_unknown"
            or production and validation["checks"].get("finalized_ledger_readback") is True
            and row.get("error_code") in {"provider_transient_unknown", "provider_timeout_unknown", "provider_transport_unknown"}
        )
        if row.get("cleanup_blocked") or production and row["status"] == "scorer_error" or row["status"] == "infra_error" and not isolated_failure:
            break
    persisted_rows = redact_credential_value(rows)
    payload = {"schema_version": 1, "source_pin": plan["source_pin"], "planned_trials": len(rows), "trials": persisted_rows}
    validation_record = {
        "schema_version": 1,
        "config_sha256": plan["config_sha256"],
        "source_pin": plan["source_pin"],
        "agent_binary_sha256": binary_hash,
        "rows_sha256": hashlib.sha256(json.dumps(persisted_rows, sort_keys=True, separators=(",", ":")).encode("utf-8")).hexdigest(),
        "planned_trials": len(rows),
        "trials": validations,
    }
    validation_path = artifacts.write("receipt-validation.json", validation_record)
    payload["verification_record"] = {"path": "../receipt-validation.json", "sha256": _sha256(validation_path)}
    artifacts.write_summary(payload)
    from eval.experiment_report import write_report

    write_report(payload, artifacts.root / "report")
    artifacts.write_checksums()
    if artifacts.verify_checksums():
        raise ValueError("run artifact checksum verification failed")
    status = "BLOCKED" if any(row["status"] in ("infra_error", "scorer_error", "not_run") for row in rows) else "PASS" if all(row["success"] is True for row in rows) else "FAIL"
    print(json.dumps({"status": status, "planned_trials": len(rows), "successful_trials": sum(row["success"] is True for row in rows), "artifact_root": str(artifacts.root)}))
    return 0 if all(row["success"] is True for row in rows) else 3


if __name__ == "__main__":
    raise SystemExit(main())
