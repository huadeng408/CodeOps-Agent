from __future__ import annotations

import json
import subprocess
from pathlib import Path

from eval.release_gate import GateStatus, evaluate_release


def _git_repo(tmp_path: Path) -> tuple[Path, str]:
    repo = tmp_path / "repo"
    repo.mkdir()
    subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
    subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=repo, check=True)
    subprocess.run(["git", "config", "user.name", "Release Gate Test"], cwd=repo, check=True)
    (repo / "tracked.txt").write_text("tracked\n", encoding="utf-8")
    subprocess.run(["git", "add", "tracked.txt"], cwd=repo, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "test"], cwd=repo, check=True)
    sha = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()
    return repo, sha


def _receipt(root: Path, lane: str, payload: dict) -> None:
    path = root / "data" / "eval" / lane / "receipts" / "receipt.json"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload) + "\n", encoding="utf-8")


def _evidence(sha: str, run_id: str = "run") -> dict:
    return {
        "schema_version": 1,
        "status": "VERIFIED",
        "run_id": run_id,
        "trace_id": "trace-1",
        "source_pin": {"git_sha": sha, "dirty_hash": "0" * 64, "untracked_files": 0},
        "raw_evidence": {"artifact_root": "eval_results/ignored", "checksum_manifest_sha256": "a" * 64},
        "failures": [],
    }


def _passing_receipts(repo: Path, sha: str) -> None:
    workflow = _evidence(sha, "workflow")
    workflow.update(
        {
            "budget": {"worker_count": 8, "task_count": 200, "fault_count": 30, "canonical": True},
            "fault_injection": {"requested": 30, "applied": 30},
            "recovery": {"denominator": 200, "successes": 198, "success_rate": 0.99},
            "checksum_verification": [],
        }
    )
    context = _evidence(sha, "context")
    context.update(
        {
            "comparison": {"input_token_reduction": 0.7, "outcome_regressed": False},
            "arms": {
                "baseline": {"call_completed": True, "reported_model": "model"},
                "layered": {"call_completed": True, "reported_model": "model"},
            },
        }
    )
    skills = _evidence(sha, "skills")
    skills.update(
        {
            "dataset_pin": {"case_count": 1000},
            "catalog_pin": {"skill_count": 40},
            "provider": {"model_revision_status": "MODEL_IDENTITY_VERIFIED"},
            "scoring": {"correct": 950, "denominator": 1000, "accuracy": 0.95},
        }
    )
    swe = _evidence(sha, "swe")
    swe.update(
        {
            "scorer": {"name": "swebench.harness.run_evaluation"},
            "official_verdict": {"submitted_instances": 20, "resolved_instances": 18},
        }
    )
    _receipt(repo, "workflow", workflow)
    _receipt(repo, "context-token", context)
    _receipt(repo, "skills", skills)
    _receipt(repo, "swebench", swe)
    subprocess.run(["git", "add", "data"], cwd=repo, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "receipts"], cwd=repo, check=True)


def test_release_gate_requires_current_head_and_clean_tree(tmp_path: Path, monkeypatch) -> None:
    repo, sha = _git_repo(tmp_path)
    _passing_receipts(repo, sha)
    monkeypatch.setattr("eval.release_gate._git_head", lambda _: sha)

    report = evaluate_release(repo, run_tests=False)
    assert report.status is GateStatus.ELIGIBLE

    # The receipts are current, but an untracked file is still a release refusal.
    (repo / "untracked.txt").write_text("do not publish\n", encoding="utf-8")
    report = evaluate_release(repo, run_tests=False)
    assert report.exit_code == 3
    assert any(check.name == "git.clean" and check.status == "BLOCKED" for check in report.checks)


def test_release_gate_passes_only_when_all_lanes_are_current_and_passing(tmp_path: Path, monkeypatch) -> None:
    repo, sha = _git_repo(tmp_path)
    _passing_receipts(repo, sha)
    monkeypatch.setattr("eval.release_gate._git_head", lambda _: sha)

    report = evaluate_release(repo, run_tests=False)
    assert report.status is GateStatus.ELIGIBLE
    assert report.exit_code == 0
    assert {check.name for check in report.checks} >= {
        "git.clean",
        "evidence.workflow",
        "evidence.context-token",
        "evidence.skills",
        "evidence.swebench",
    }


def test_release_gate_accepts_receipt_commit_bound_to_its_source_parent(tmp_path: Path) -> None:
    repo, source_sha = _git_repo(tmp_path)
    _passing_receipts(repo, source_sha)

    report = evaluate_release(repo, run_tests=False)

    assert report.status is GateStatus.ELIGIBLE
    assert all(
        check.detail == "source-bound receipt satisfies the lane contract"
        for check in report.checks
        if check.name.startswith("evidence.")
    )


def test_release_gate_rejects_receipt_pin_after_a_non_evidence_commit(tmp_path: Path) -> None:
    repo, source_sha = _git_repo(tmp_path)
    _passing_receipts(repo, source_sha)
    (repo / "tracked.txt").write_text("source changed\n", encoding="utf-8")
    subprocess.run(["git", "add", "tracked.txt"], cwd=repo, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "source change"], cwd=repo, check=True)

    report = evaluate_release(repo, run_tests=False)

    assert report.status is GateStatus.BLOCKED
    assert any(check.name == "evidence.workflow" and check.status == "BLOCKED" for check in report.checks)


def test_release_gate_rejects_smoke_swebench_and_bad_skill_fingerprint(tmp_path: Path, monkeypatch) -> None:
    repo, sha = _git_repo(tmp_path)
    _passing_receipts(repo, sha)
    monkeypatch.setattr("eval.release_gate._git_head", lambda _: sha)
    skills = next((repo / "data" / "eval" / "skills" / "receipts").glob("*.json"))
    skill_payload = json.loads(skills.read_text(encoding="utf-8"))
    skill_payload["provider"]["model_revision_status"] = "MODEL_IDENTITY_UNVERIFIED"
    skills.write_text(json.dumps(skill_payload) + "\n", encoding="utf-8")
    swe = next((repo / "data" / "eval" / "swebench" / "receipts").glob("*.json"))
    swe_payload = json.loads(swe.read_text(encoding="utf-8"))
    swe_payload["status"] = "SMOKE_PASS"
    swe.write_text(json.dumps(swe_payload) + "\n", encoding="utf-8")

    report = evaluate_release(repo, run_tests=False)
    assert report.status is GateStatus.BLOCKED
    assert any(check.name == "evidence.skills" and check.status == "BLOCKED" for check in report.checks)
    assert any(check.name == "evidence.swebench" and check.status == "BLOCKED" for check in report.checks)


def test_release_gate_test_command_failure_is_fail_closed(tmp_path: Path, monkeypatch) -> None:
    repo, sha = _git_repo(tmp_path)
    _passing_receipts(repo, sha)
    monkeypatch.setattr("eval.release_gate._git_head", lambda _: sha)

    report = evaluate_release(repo, run_tests=True, test_commands=(("python", "-c", "raise SystemExit(7)"),))
    assert report.status is GateStatus.BLOCKED
    assert any(check.name == "tests.1" and check.status == "BLOCKED" for check in report.checks)
