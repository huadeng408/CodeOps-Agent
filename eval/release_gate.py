"""Fail-closed release gate for the CodeOps-Agent acceptance contract.

The gate is deliberately small and boring: it reads only curated receipts,
checks their pins and denominators, and never turns a smoke run or a missing
external service into a release pass.  The full run trees remain outside the
repository; only the redacted receipt surface is considered here.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from dataclasses import asdict, dataclass
from enum import Enum
from pathlib import Path
from typing import Any, Sequence

SHA256_LENGTH = 64
SHA256_RE = re.compile(r"^[0-9a-f]{64}$")
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
    current_sha: str,
    require_verified: bool = True,
) -> tuple[bool, str]:
    if require_verified and payload.get("status") != "VERIFIED":
        return False, f"status is {payload.get('status', '<missing>')!r}"
    source_pin = payload.get("source_pin")
    if not isinstance(source_pin, dict) or source_pin.get("git_sha") != current_sha:
        return False, "source_pin.git_sha is not the current HEAD"
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
    current_sha: str,
    predicate,
) -> tuple[dict[str, Any] | None, str]:
    reasons: list[str] = []
    for payload in receipts:
        ok, reason = _receipt_base(payload, current_sha=current_sha)
        if not ok:
            reasons.append(f"{Path(str(payload.get('__path', 'receipt'))).name}: {reason}")
            continue
        try:
            predicate(payload)
        except (AttributeError, KeyError, TypeError, ValueError) as exc:
            reasons.append(f"{Path(str(payload.get('__path', 'receipt'))).name}: {exc}")
            continue
        return payload, "current-HEAD receipt satisfies the lane contract"
    return None, "; ".join(reasons) if reasons else "no receipt satisfies the lane contract"


def _workflow_predicate(payload: dict[str, Any]) -> None:
    budget = payload.get("budget")
    target = budget.get("canonical_target") if isinstance(budget, dict) else None
    if not isinstance(target, dict):
        target = budget if isinstance(budget, dict) else {}
    expected = {"worker_count": 8, "task_count": 200, "fault_count": 30}
    if any(target.get(key) != value for key, value in expected.items()):
        raise ValueError("canonical target must be 8 workers, 200 tasks and 30 faults")
    if not isinstance(budget, dict) or budget.get("canonical") is not True:
        raise ValueError("receipt is not marked canonical")
    injection = payload.get("fault_injection", {})
    recovery = payload.get("recovery", {})
    if injection.get("requested") != 30 or injection.get("applied") != 30:
        raise ValueError("30 real process faults were not applied")
    if recovery.get("denominator") != 200 or float(recovery.get("success_rate", 0)) < 0.985:
        raise ValueError("recovery rate is below 98.5% or denominator is not 200")
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


def _skills_predicate(payload: dict[str, Any]) -> None:
    dataset = payload.get("dataset_pin", {})
    catalog = payload.get("catalog_pin", {})
    scoring = payload.get("scoring", {})
    provider = payload.get("provider", {})
    if dataset.get("case_count") != 1000 or catalog.get("skill_count", 0) < 40:
        raise ValueError("locked 1,000 cases and 40 runnable skills are required")
    denominator = scoring.get("denominator")
    correct = scoring.get("correct")
    if denominator != 1000 or not isinstance(correct, (int, float)) or correct < 948:
        raise ValueError("skill selection must be at least 948/1000")
    if provider.get("model_revision_status") in (None, "MODEL_IDENTITY_UNVERIFIED"):
        raise ValueError("provider model revision is not verified")


def _swebench_predicate(payload: dict[str, Any]) -> None:
    if payload.get("status") == "SMOKE_PASS":
        raise ValueError("smoke receipts cannot satisfy the official scorer gate")
    scorer = payload.get("scorer", {})
    verdict = payload.get("official_verdict", {})
    if scorer.get("name") != "swebench.harness.run_evaluation":
        raise ValueError("official SWE-bench scorer is missing")
    if verdict.get("submitted_instances") != 20 or verdict.get("resolved_instances", 0) < 18:
        raise ValueError("official SWE-bench result must be at least 18/20")


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
    return _check(name, result.returncode == 0, f"exit_code={result.returncode}")


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
        ("workflow", _workflow_predicate),
        ("context-token", _context_predicate),
        ("skills", _skills_predicate),
        ("swebench", _swebench_predicate),
    )
    for lane, predicate in lane_specs:
        receipts, loading_check = _read_receipts(data_root, lane)
        if loading_check is not None:
            checks.append(loading_check)
            continue
        _, detail = _find_lane_receipt(receipts, current_sha=current_sha, predicate=predicate)
        checks.append(_check(f"evidence.{lane}", "current-HEAD receipt" in detail, detail))

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
