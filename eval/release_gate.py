"""Fail-closed release gate for the CodeOps-Agent acceptance contract.

The gate is deliberately small and boring: it reads only curated receipts,
checks their pins and denominators, and never turns a smoke run or a missing
external service into a release pass.  The full run trees remain outside the
repository; only the redacted receipt surface is considered here.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import re
import subprocess
import sys
from dataclasses import asdict, dataclass
from enum import Enum
from pathlib import Path, PurePosixPath
from typing import Any, Sequence

from orchestrator.security.credentials import redact_credential_text

SHA256_LENGTH = 64
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
EMPTY_DIRTY_HASH = hashlib.sha256(b"").hexdigest()
RELEASE_BLOCKED_EXIT_CODE = 3
RELEASE_ERROR_EXIT_CODE = 2


class GateStatus(str, Enum):
    ELIGIBLE = "ELIGIBLE"
    BLOCKED = "BLOCKED"


@dataclass(frozen=True)
class GateCheck:
    name: str
    status: str
    detail: str


@dataclass(frozen=True)
class GateReport:
    status: GateStatus
    checks: tuple[GateCheck, ...]
    git_sha: str = ""

    @property
    def exit_code(self) -> int:
        return 0 if self.status is GateStatus.ELIGIBLE else RELEASE_BLOCKED_EXIT_CODE

    def to_dict(self) -> dict[str, Any]:
        return {
            "status": self.status.value,
            "exit_code": self.exit_code,
            "git_sha": self.git_sha,
            "checks": [asdict(check) for check in self.checks],
        }


def _git_head(repo_root: Path) -> str:
    result = subprocess.run(
        ["git", "-C", str(repo_root), "rev-parse", "HEAD"],
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    return result.stdout.strip()


def _git_is_clean(repo_root: Path) -> bool:
    result = subprocess.run(
        ["git", "-C", str(repo_root), "status", "--porcelain", "--untracked-files=all"],
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    return not bool(result.stdout.strip())


def _check(name: str, ok: bool, detail: str) -> GateCheck:
    return GateCheck(name=name, status="PASS" if ok else GateStatus.BLOCKED.value, detail=detail)


def _source_pin_matches_head(repo_root: Path, source_sha: str, current_sha: str) -> bool:
    """Accept HEAD or the parent of a commit that only adds curated receipts.

    Evaluation runs finish before their redacted receipt is committed. The
    receipt therefore cannot pin the commit that contains itself. A narrow
    exception keeps the source binding intact: the current commit must have
    exactly one parent equal to the pinned source, and every changed path must
    be a curated JSON receipt.
    """

    if source_sha == current_sha:
        return True
    try:
        parents = subprocess.run(
            ["git", "-C", str(repo_root), "rev-list", "--parents", "-n", "1", current_sha],
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
        ).stdout.strip().split()[1:]
        if parents != [source_sha]:
            return False
        changed_paths = subprocess.run(
            [
                "git",
                "-C",
                str(repo_root),
                "diff-tree",
                "--no-commit-id",
                "--name-only",
                "-r",
                current_sha,
            ],
            check=True,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
        ).stdout.splitlines()
    except (OSError, subprocess.CalledProcessError):
        return False
    return bool(changed_paths) and all(
        PurePosixPath(path.replace("\\", "/")).match("data/eval/*/receipts/*.json")
        for path in changed_paths
    )


def _read_receipts(data_root: Path, lane: str) -> tuple[list[dict[str, Any]], GateCheck | None]:
    receipt_dir = data_root / lane / "receipts"
    if not receipt_dir.is_dir():
        return [], _check(f"evidence.{lane}", False, "receipt directory is missing")
    paths = sorted(receipt_dir.glob("*.json"))
    if not paths:
        return [], _check(f"evidence.{lane}", False, "no curated receipt is present")
    receipts: list[dict[str, Any]] = []
    malformed: list[str] = []
    for path in paths:
        try:
            payload = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            malformed.append(path.name)
            continue
        if isinstance(payload, dict):
            payload["__path"] = path.as_posix()
            receipts.append(payload)
        else:
            malformed.append(path.name)
    if malformed:
        return [], _check(
            f"evidence.{lane}",
            False,
            "malformed receipt(s): " + ", ".join(sorted(malformed)),
        )
    if not receipts:
        return [], _check(f"evidence.{lane}", False, "all receipts are unreadable")
    return receipts, None


def _receipt_base(
    payload: dict[str, Any],
    *,
    repo_root: Path,
    current_sha: str,
    require_verified: bool = True,
) -> tuple[bool, str]:
    if require_verified and payload.get("status") != "VERIFIED":
        return False, f"status is {payload.get('status', '<missing>')!r}"
    source_pin = payload.get("source_pin")
    if not isinstance(source_pin, dict) or not _source_pin_matches_head(
        repo_root, str(source_pin.get("git_sha", "")), current_sha
    ):
        return False, "source_pin.git_sha is not current HEAD or an evidence-only commit parent"
    if source_pin.get("dirty_hash") != EMPTY_DIRTY_HASH or source_pin.get("untracked_files") != 0:
        return False, "source_pin records a dirty or untracked source tree"
    run_id = payload.get("run_id")
    trace_id = payload.get("trace_id")
    if not isinstance(run_id, str) or not run_id:
        return False, "run_id is missing"
    if not isinstance(trace_id, str) or not trace_id:
        return False, "trace_id is missing"
    raw = payload.get("raw_evidence")
    if not isinstance(raw, dict) or not raw.get("artifact_root"):
        return False, "raw_evidence.artifact_root is missing"
    hashes = [value for key, value in raw.items() if "sha256" in key and isinstance(value, str)]
    if not hashes or any(not SHA256_RE.fullmatch(value) for value in hashes):
        return False, "raw evidence is missing a SHA-256 pin"
    if payload.get("failures") != []:
        return False, "receipt contains failures"
    return True, "receipt pins and evidence metadata are complete"


def _find_lane_receipt(
    receipts: Sequence[dict[str, Any]],
    *,
    repo_root: Path,
    current_sha: str,
    predicate,
) -> tuple[dict[str, Any] | None, str]:
    reasons: list[str] = []
    for payload in receipts:
        ok, reason = _receipt_base(payload, repo_root=repo_root, current_sha=current_sha)
        if not ok:
            reasons.append(f"{Path(str(payload.get('__path', 'receipt'))).name}: {reason}")
            continue
        try:
            predicate(payload)
        except (AttributeError, KeyError, TypeError, ValueError) as exc:
            reasons.append(f"{Path(str(payload.get('__path', 'receipt'))).name}: {exc}")
            continue
        return payload, "source-bound receipt satisfies the lane contract"
    return None, "; ".join(reasons) if reasons else "no receipt satisfies the lane contract"


def _workflow_predicate(payload: dict[str, Any]) -> None:
    budget = payload.get("budget")
    target = budget.get("canonical_target") if isinstance(budget, dict) else None
    if not isinstance(target, dict):
        target = budget if isinstance(budget, dict) else {}
    expected = {"worker_count": 8, "task_count": 200, "fault_count": 30, "stage_count": 3}
    if any(target.get(key) != value for key, value in expected.items()):
        raise ValueError("canonical target must be 8 workers, 200 tasks and 30 faults")
    if not isinstance(budget, dict) or budget.get("canonical") is not True:
        raise ValueError("receipt is not marked canonical")
    injection = payload.get("fault_injection", {})
    recovery = payload.get("recovery", {})
    workload = payload.get("workload", {})
    sqlite_summary = payload.get("sqlite", {})
    if workload.get("kind") != "checkpointed_dependency_pipeline" or workload.get("stage_count") != 3:
        raise ValueError("workflow tasks must be three-stage checkpointed dependency pipelines")
    if injection.get("requested") != 30 or injection.get("applied") != 30:
        raise ValueError("30 real process faults were not applied")
    evidence = injection.get("evidence")
    if (
        not isinstance(evidence, list)
        or len(evidence) != 30
        or any(not isinstance(item, dict) or not item.get("task_id") or not item.get("stage_id") for item in evidence)
    ):
        raise ValueError("each process fault must identify the checkpointed task stage it interrupted")
    if recovery.get("denominator") != 200 or float(recovery.get("success_rate", 0)) < 0.985:
        raise ValueError("recovery rate is below 98.5% or denominator is not 200")
    if (
        recovery.get("stage_denominator") != 600
        or recovery.get("completed_stages") != 600
        or recovery.get("checkpoint_complete") is not True
        or int(recovery.get("resume_events", 0)) < 30
    ):
        raise ValueError("all 600 stages and at least 30 durable recovery events are required")
    if sqlite_summary.get("completed_stage_count") != 600:
        raise ValueError("SQLite checkpoints do not contain all 600 completed stages")
    if payload.get("checksum_verification") not in ([], None):
        raise ValueError("receipt checksum verification failed")


def _context_predicate(payload: dict[str, Any]) -> None:
    comparison = payload.get("comparison")
    if not isinstance(comparison, dict):
        raise ValueError("comparison is missing")
    if float(comparison.get("input_token_reduction", 0)) < 0.60:
        raise ValueError("input token reduction is below 60%")
    if comparison.get("outcome_regressed") is not False:
        raise ValueError("task outcome regressed or was not measured")
    arms = payload.get("arms")
    if not isinstance(arms, dict) or not isinstance(arms.get("baseline"), dict) or not isinstance(arms.get("layered"), dict):
        raise ValueError("baseline and layered arm evidence is missing")
    baseline = arms["baseline"]
    layered = arms["layered"]
    if baseline.get("call_completed") is not True or layered.get("call_completed") is not True:
        raise ValueError("both context arms must complete")
    if baseline.get("reported_model") != layered.get("reported_model"):
        raise ValueError("context arms used different models")
    provider = payload.get("provider", {})
    if provider.get("model_revision_status") != "MODEL_IDENTITY_VERIFIED":
        raise ValueError("context provider model revision is not verified")
    data_pin = payload.get("data_pin", {})
    if not SHA256_RE.fullmatch(str(data_pin.get("task_sha256", ""))):
        raise ValueError("context task pin is missing")
    budget = payload.get("budget", {})
    if (
        budget.get("calls_per_arm") != 1
        or not isinstance(budget.get("max_output_tokens_per_arm"), int)
        or budget.get("max_output_tokens_per_arm", 0) <= 0
    ):
        raise ValueError("context arms do not share one fixed output budget")


def _skills_predicate(payload: dict[str, Any]) -> None:
    dataset = payload.get("dataset_pin", {})
    catalog = payload.get("catalog_pin", {})
    scoring = payload.get("scoring", {})
    provider = payload.get("provider", {})
    if dataset.get("case_count") != 1000 or catalog.get("skill_count", 0) < 40:
        raise ValueError("locked 1,000 cases and 40 runnable skills are required")
    denominator = scoring.get("denominator")
    correct = scoring.get("correct")
    if denominator != 1000 or type(correct) is not int or not 948 <= correct <= denominator:
        raise ValueError("skill selection must be at least 948/1000")
    if provider.get("model_revision_status") not in {
        "VERIFIED",
        "MODEL_IDENTITY_VERIFIED",
    }:
        raise ValueError("provider model revision is not verified")
    manifest_sha256 = str(catalog.get("sha256", ""))
    if not SHA256_RE.fullmatch(manifest_sha256):
        raise ValueError("evaluated Skill manifest SHA-256 is missing")
    matrix = payload.get("execution_matrix", {})
    matrix_failures = matrix.get("failures")
    matrix_denominator = matrix.get("denominator")
    if (
        matrix_denominator != catalog.get("skill_count")
        or matrix_denominator < 40
        or matrix.get("passed") != matrix_denominator
        or matrix_failures != []
        or matrix.get("production_loader") is not True
        or matrix.get("metadata_only_discovery") is not True
        or matrix.get("lazy_body_loads") != matrix_denominator
    ):
        raise ValueError("at least 40 Skills must pass the production discovery and lazy-load matrix")
    source_pin = payload.get("source_pin")
    if matrix.get("source_pin") != source_pin:
        raise ValueError("Skill execution matrix is not bound to the evaluation source")
    if matrix.get("manifest_sha256") != manifest_sha256:
        raise ValueError("Skill execution matrix is not bound to the evaluated manifest")
    artifact_sha256 = str(matrix.get("artifact_sha256", ""))
    catalog_sha256 = str(matrix.get("catalog_sha256", ""))
    if not SHA256_RE.fullmatch(artifact_sha256) or not SHA256_RE.fullmatch(catalog_sha256):
        raise ValueError("Skill execution matrix artifact and catalog pins are required")
    artifacts = payload.get("artifacts", {})
    raw_evidence = payload.get("raw_evidence", {})
    if artifacts.get("execution_matrix") != "skill-execution-matrix.json":
        raise ValueError("Skill execution matrix artifact path is missing")
    if (
        raw_evidence.get("execution_matrix_sha256") != artifact_sha256
        or raw_evidence.get("execution_matrix_catalog_sha256") != catalog_sha256
    ):
        raise ValueError("raw Skill execution matrix evidence does not match the receipt pins")


def _swebench_predicate(payload: dict[str, Any]) -> None:
    if payload.get("status") == "SMOKE_PASS":
        raise ValueError("smoke receipts cannot satisfy the official scorer gate")
    scorer = payload.get("scorer", {})
    verdict = payload.get("official_verdict", {})
    if scorer.get("name") != "swebench.harness.run_evaluation":
        raise ValueError("official SWE-bench scorer is missing")
    if verdict.get("submitted_instances") != 20 or verdict.get("resolved_instances", 0) < 18:
        raise ValueError("official SWE-bench result must be at least 18/20")
    baseline = payload.get("baseline", {})
    if (
        baseline.get("submitted_instances") != 20
        or baseline.get("resolved_instances") != 8
        or not SHA256_RE.fullmatch(str(baseline.get("receipt_sha256", "")))
    ):
        raise ValueError("the locked 8/20 baseline receipt is missing")


def _extension_onboarding_predicate(payload: dict[str, Any]) -> None:
    measurement = payload.get("measurement", {})
    baseline = measurement.get("baseline", {})
    candidate = measurement.get("candidate", {})
    scope = payload.get("scope", {})
    verification = payload.get("verification", {})
    if measurement.get("unit") != "engineer_hours" or measurement.get("same_scope") is not True:
        raise ValueError("module onboarding must compare the same scope in engineer-hours")
    if float(baseline.get("value", 0)) < 16.0:
        raise ValueError("module onboarding baseline must record at least two working days")
    candidate_hours = float(candidate.get("value", 0))
    if candidate_hours <= 0 or candidate_hours > 8.0:
        raise ValueError("plugin-based module onboarding must complete within eight engineer-hours")
    if not SHA256_RE.fullmatch(str(scope.get("contract_sha256", ""))):
        raise ValueError("module onboarding scope contract is not pinned")
    if verification.get("targeted_tests") is not True or verification.get("runtime_e2e") is not True:
        raise ValueError("module onboarding tests and runtime E2E are required")


def _agent_e2e_predicate(payload: dict[str, Any]) -> None:
    provider = payload.get("provider", {})
    isolation = payload.get("isolation", {})
    checks = payload.get("checks", {})
    trace = payload.get("trace", {})
    if provider.get("backed") is not True or provider.get("model_revision_status") != "MODEL_IDENTITY_VERIFIED":
        raise ValueError("agent E2E must use a provider with verified model identity")
    if (
        not isolation.get("parent_session_id")
        or not isolation.get("child_session_id")
        or isolation.get("parent_session_id") == isolation.get("child_session_id")
    ):
        raise ValueError("agent E2E does not prove an independent child session")
    required_checks = {
        "agent_card",
        "message_text_file_json",
        "task_lifecycle",
        "isolated_child_context",
        "skill_lazy_loaded",
        "harness_authorized_tool",
        "mcp_call",
        "sandbox_enforced",
        "artifact_pinned",
        "memory_reflection_written",
        "restart_recall",
        "ledger_hash_chain",
    }
    missing = sorted(name for name in required_checks if checks.get(name) is not True)
    if missing:
        raise ValueError("agent E2E checks are missing: " + ", ".join(missing))
    required_spans = {
        "agent.main",
        "agent.subagent",
        "skill.load",
        "tool.mcp",
        "artifact.publish",
        "memory.reflect",
        "memory.recall",
    }
    observed_spans = trace.get("observed_spans")
    if (
        trace.get("backend_readback") is not True
        or trace.get("single_trace") is not True
        or not isinstance(observed_spans, list)
        or not required_spans.issubset(set(observed_spans))
    ):
        raise ValueError("agent E2E OpenTelemetry readback is incomplete")


def _terminalbench_predicate(payload: dict[str, Any]) -> None:
    scorer = payload.get("scorer", {})
    verdict = payload.get("official_verdict", {})
    provider = payload.get("provider", {})
    trace = payload.get("trace", {})
    dataset = payload.get("dataset_pin", {})
    if scorer.get("name") != "terminal_bench.Harness":
        raise ValueError("official Terminal-Bench harness is missing")
    submitted = verdict.get("submitted_instances")
    if (
        not isinstance(submitted, int)
        or submitted < 1
        or verdict.get("scored_instances") != submitted
        or verdict.get("resolved_instances", 0) < 1
    ):
        raise ValueError("Terminal-Bench requires at least one resolved, officially scored task")
    if provider.get("backed") is not True or provider.get("model_revision_status") != "MODEL_IDENTITY_VERIFIED":
        raise ValueError("Terminal-Bench provider model identity is not verified")
    if trace.get("official_runner_parentage") is not True or trace.get("backend_readback") is not True:
        raise ValueError("Terminal-Bench official runner trace readback is incomplete")
    if not SHA256_RE.fullmatch(str(dataset.get("sha256", ""))):
        raise ValueError("Terminal-Bench dataset pin is missing")


def _redacted_failure_tail(stdout: str, stderr: str, *, max_chars: int = 600) -> str:
    lines = [line.strip() for line in (stdout + "\n" + stderr).splitlines() if line.strip()]
    if not lines:
        return ""
    # Preserve pytest/Go failure identifiers when a long test run emits a
    # summary followed by plugin diagnostics.  The final lines alone often
    # contain only framework noise (or dependency download messages).
    signal = [
        line
        for line in lines
        if "FAILED" in line
        or "FAIL:" in line
        or line.startswith("--- FAIL")
        or line.startswith("FAIL\t")
        or "short test summary" in line.lower()
    ]
    summary_lines = (signal[-3:] if signal else lines[-4:])
    summary = " | ".join(summary_lines)
    summary = " ".join(redact_credential_text(summary).split())
    if len(summary) > max_chars:
        summary = summary[: max_chars - 3].rstrip() + "..."
    return summary


def _test_check(name: str, command: Sequence[str], repo_root: Path, timeout: float) -> GateCheck:
    try:
        result = subprocess.run(
            list(command),
            cwd=repo_root,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=timeout,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        return _check(name, False, f"test command unavailable or timed out: {type(exc).__name__}")
    detail = f"exit_code={result.returncode}"
    if result.returncode != 0:
        summary = _redacted_failure_tail(result.stdout, result.stderr)
        if summary:
            detail += f"; failure_tail={summary}"
    return _check(name, result.returncode == 0, detail)


def evaluate_release(
    repo_root: str | Path = ".",
    *,
    run_tests: bool = True,
    test_commands: Sequence[Sequence[str]] | None = None,
    test_timeout_seconds: float = 1800.0,
) -> GateReport:
    """Evaluate the repository release contract without mutating anything."""
    root = Path(repo_root).resolve()
    checks: list[GateCheck] = []
    try:
        current_sha = _git_head(root)
    except (OSError, subprocess.CalledProcessError) as exc:
        return GateReport(
            status=GateStatus.BLOCKED,
            checks=(_check("git.head", False, f"cannot resolve HEAD: {type(exc).__name__}"),),
        )
    checks.append(_check("git.head", bool(current_sha), "current HEAD resolved"))
    try:
        clean = _git_is_clean(root)
    except (OSError, subprocess.CalledProcessError) as exc:
        clean = False
        checks.append(_check("git.clean", False, f"cannot inspect worktree: {type(exc).__name__}"))
    else:
        checks.append(_check("git.clean", clean, "working tree is clean" if clean else "working tree has changes"))

    if run_tests:
        commands = test_commands or (
            (sys.executable, "-m", "pytest", "-q"),
            ("go", "test", "./..."),
        )
        for index, command in enumerate(commands, start=1):
            checks.append(_test_check(f"tests.{index}", command, root, test_timeout_seconds))

    data_root = root / "data" / "eval"
    lane_specs = (
        ("extension-onboarding", _extension_onboarding_predicate),
        ("workflow", _workflow_predicate),
        ("context-token", _context_predicate),
        ("skills", _skills_predicate),
        ("agent-e2e", _agent_e2e_predicate),
        ("terminalbench", _terminalbench_predicate),
        ("swebench", _swebench_predicate),
    )
    for lane, predicate in lane_specs:
        receipts, loading_check = _read_receipts(data_root, lane)
        if loading_check is not None:
            checks.append(loading_check)
            continue
        _, detail = _find_lane_receipt(
            receipts, repo_root=root, current_sha=current_sha, predicate=predicate
        )
        checks.append(_check(f"evidence.{lane}", "source-bound receipt" in detail, detail))

    status = GateStatus.ELIGIBLE if all(check.status == "PASS" for check in checks) else GateStatus.BLOCKED
    return GateReport(status=status, checks=tuple(checks), git_sha=current_sha)


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Run the CodeOps-Agent fail-closed release gate")
    parser.add_argument("--repo-root", type=Path, default=Path("."))
    parser.add_argument("--json", action="store_true", dest="as_json")
    parser.add_argument("--skip-tests", action="store_true", help="do not execute Go/Python test suites")
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    report = evaluate_release(args.repo_root, run_tests=not args.skip_tests)
    if args.as_json:
        print(json.dumps(report.to_dict(), ensure_ascii=True, sort_keys=True))
    else:
        print(f"release_status={report.status.value} git_sha={report.git_sha}")
        for check in report.checks:
            print(f"{check.status:<7} {check.name}: {check.detail}")
    return report.exit_code


if __name__ == "__main__":  # pragma: no cover - exercised by the CLI smoke test
    raise SystemExit(main())
