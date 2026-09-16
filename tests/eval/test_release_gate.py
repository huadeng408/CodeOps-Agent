from __future__ import annotations

import json
import hashlib
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
        "source_pin": {
            "git_sha": sha,
            "dirty_hash": hashlib.sha256(b"").hexdigest(),
            "untracked_files": 0,
        },
        "raw_evidence": {"artifact_root": "eval_results/ignored", "checksum_manifest_sha256": "a" * 64},
        "failures": [],
    }


def _passing_receipts(repo: Path, sha: str) -> None:
    extension = _evidence(sha, "extension")
    extension.update(
        {
            "measurement": {
                "unit": "engineer_hours",
                "same_scope": True,
                "baseline": {"value": 16.0},
                "candidate": {"value": 6.0},
            },
            "scope": {"contract_sha256": "1" * 64},
            "verification": {"targeted_tests": True, "runtime_e2e": True},
        }
    )
    workflow = _evidence(sha, "workflow")
    workflow.update(
        {
            "workload": {"kind": "checkpointed_dependency_pipeline", "stage_count": 3},
            "budget": {
                "worker_count": 8,
                "task_count": 200,
                "fault_count": 30,
                "stage_count": 3,
                "canonical": True,
            },
            "fault_injection": {
                "requested": 30,
                "applied": 30,
                "evidence": [
                    {"task_id": f"task-{index:04d}", "stage_id": "stage-2"}
                    for index in range(1, 31)
                ],
            },
            "recovery": {
                "denominator": 200,
                "successes": 198,
                "success_rate": 0.99,
                "stage_denominator": 600,
                "completed_stages": 600,
                "resume_events": 30,
                "checkpoint_complete": True,
            },
            "sqlite": {"completed_stage_count": 600},
            "checksum_verification": [],
        }
    )
    context = _evidence(sha, "context")
    context.update(
        {
            "comparison": {"input_token_reduction": 0.7, "outcome_regressed": False},
            "provider": {"model_revision_status": "MODEL_IDENTITY_VERIFIED"},
            "data_pin": {"task_sha256": "2" * 64},
            "budget": {"calls_per_arm": 1, "max_output_tokens_per_arm": 512},
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
            "catalog_pin": {"skill_count": 40, "sha256": "5" * 64},
            "provider": {"model_revision_status": "MODEL_IDENTITY_VERIFIED"},
            "scoring": {"correct": 950, "denominator": 1000, "accuracy": 0.95},
            "execution_matrix": {
                "denominator": 40,
                "passed": 40,
                "failures": [],
                "production_loader": True,
                "metadata_only_discovery": True,
                "lazy_body_loads": 40,
                "manifest_sha256": "5" * 64,
                "catalog_sha256": "6" * 64,
                "artifact_sha256": "7" * 64,
                "source_pin": skills["source_pin"],
            },
            "artifacts": {"execution_matrix": "skill-execution-matrix.json"},
        }
    )
    skills["raw_evidence"].update(
        {
            "execution_matrix_sha256": "7" * 64,
            "execution_matrix_catalog_sha256": "6" * 64,
        }
    )
    agent = _evidence(sha, "agent")
    agent.update(
        {
            "provider": {"backed": True, "model_revision_status": "MODEL_IDENTITY_VERIFIED"},
            "isolation": {"parent_session_id": "parent", "child_session_id": "child"},
            "checks": {
                "agent_card": True,
                "message_text_file_json": True,
                "task_lifecycle": True,
                "isolated_child_context": True,
                "skill_lazy_loaded": True,
                "harness_authorized_tool": True,
                "mcp_call": True,
                "sandbox_enforced": True,
                "artifact_pinned": True,
                "memory_reflection_written": True,
                "restart_recall": True,
                "ledger_hash_chain": True,
            },
            "trace": {
                "backend_readback": True,
                "single_trace": True,
                "observed_spans": [
                    "agent.main",
                    "agent.subagent",
                    "skill.load",
                    "tool.mcp",
                    "artifact.publish",
                    "memory.reflect",
                    "memory.recall",
                ],
            },
        }
    )
    terminal = _evidence(sha, "terminal")
    terminal.update(
        {
            "scorer": {"name": "terminal_bench.Harness"},
            "official_verdict": {
                "submitted_instances": 1,
                "scored_instances": 1,
                "resolved_instances": 1,
            },
            "provider": {"backed": True, "model_revision_status": "MODEL_IDENTITY_VERIFIED"},
            "trace": {"official_runner_parentage": True, "backend_readback": True},
            "dataset_pin": {"sha256": "3" * 64},
        }
    )
    swe = _evidence(sha, "swe")
    swe.update(
        {
            "scorer": {"name": "swebench.harness.run_evaluation"},
            "official_verdict": {"submitted_instances": 20, "resolved_instances": 18},
            "baseline": {
                "submitted_instances": 20,
                "resolved_instances": 8,
                "receipt_sha256": "4" * 64,
            },
        }
    )
    _receipt(repo, "extension-onboarding", extension)
    _receipt(repo, "workflow", workflow)
    _receipt(repo, "context-token", context)
    _receipt(repo, "skills", skills)
    _receipt(repo, "agent-e2e", agent)
    _receipt(repo, "terminalbench", terminal)
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
        "evidence.extension-onboarding",
        "evidence.workflow",
        "evidence.context-token",
        "evidence.skills",
        "evidence.agent-e2e",
        "evidence.terminalbench",
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


def test_release_gate_rejects_receipt_generated_from_dirty_source(tmp_path: Path, monkeypatch) -> None:
    repo, source_sha = _git_repo(tmp_path)
    _passing_receipts(repo, source_sha)
    receipt_path = next((repo / "data" / "eval" / "workflow" / "receipts").glob("*.json"))
    payload = json.loads(receipt_path.read_text(encoding="utf-8"))
    payload["source_pin"]["dirty_hash"] = "a" * 64
    receipt_path.write_text(json.dumps(payload) + "\n", encoding="utf-8")
    subprocess.run(["git", "add", str(receipt_path)], cwd=repo, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "dirty source receipt"], cwd=repo, check=True)
    monkeypatch.setattr("eval.release_gate._git_head", lambda _: source_sha)

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


def test_release_gate_rejects_unpinned_skill_execution_matrix(
    tmp_path: Path, monkeypatch
) -> None:
    repo, sha = _git_repo(tmp_path)
    _passing_receipts(repo, sha)
    monkeypatch.setattr("eval.release_gate._git_head", lambda _: sha)
    skills = next((repo / "data" / "eval" / "skills" / "receipts").glob("*.json"))
    payload = json.loads(skills.read_text(encoding="utf-8"))
    payload["execution_matrix"].pop("artifact_sha256")
    skills.write_text(json.dumps(payload) + "\n", encoding="utf-8")

    report = evaluate_release(repo, run_tests=False)

    assert report.status is GateStatus.BLOCKED
    assert any(
        check.name == "evidence.skills" and check.status == "BLOCKED"
        for check in report.checks
    )


def test_release_gate_rejects_bogus_skill_identity_and_manifest_hash(
    tmp_path: Path, monkeypatch
) -> None:
    for mutation in ("identity", "manifest"):
        root = tmp_path / mutation
        root.mkdir()
        repo, sha = _git_repo(root)
        _passing_receipts(repo, sha)
        monkeypatch.setattr("eval.release_gate._git_head", lambda _, value=sha: value)
        skills = next(
            (repo / "data" / "eval" / "skills" / "receipts").glob("*.json")
        )
        payload = json.loads(skills.read_text(encoding="utf-8"))
        if mutation == "identity":
            payload["provider"]["model_revision_status"] = "bogus-but-accepted"
        else:
            payload["catalog_pin"]["sha256"] = "not-a-hash"
            payload["execution_matrix"]["manifest_sha256"] = "not-a-hash"
        skills.write_text(json.dumps(payload) + "\n", encoding="utf-8")

        report = evaluate_release(repo, run_tests=False)

        assert report.status is GateStatus.BLOCKED
        assert any(
            check.name == "evidence.skills" and check.status == "BLOCKED"
            for check in report.checks
        )


def test_release_gate_rejects_non_integral_or_impossible_skill_scores(
    tmp_path: Path, monkeypatch
) -> None:
    for index, invalid_correct in enumerate((float("nan"), float("inf"), 1001, 948.5)):
        root = tmp_path / f"score-{index}"
        root.mkdir()
        repo, sha = _git_repo(root)
        _passing_receipts(repo, sha)
        monkeypatch.setattr("eval.release_gate._git_head", lambda _, value=sha: value)
        skills = next(
            (repo / "data" / "eval" / "skills" / "receipts").glob("*.json")
        )
        payload = json.loads(skills.read_text(encoding="utf-8"))
        payload["scoring"]["correct"] = invalid_correct
        skills.write_text(json.dumps(payload) + "\n", encoding="utf-8")

        report = evaluate_release(repo, run_tests=False)

        assert report.status is GateStatus.BLOCKED
        assert any(
            check.name == "evidence.skills" and check.status == "BLOCKED"
            for check in report.checks
        )


def test_release_gate_rejects_skill_matrix_from_different_source(
    tmp_path: Path, monkeypatch
) -> None:
    repo, sha = _git_repo(tmp_path)
    _passing_receipts(repo, sha)
    monkeypatch.setattr("eval.release_gate._git_head", lambda _: sha)
    skills = next((repo / "data" / "eval" / "skills" / "receipts").glob("*.json"))
    payload = json.loads(skills.read_text(encoding="utf-8"))
    payload["execution_matrix"]["source_pin"]["dirty_hash"] = "8" * 64
    skills.write_text(json.dumps(payload) + "\n", encoding="utf-8")

    report = evaluate_release(repo, run_tests=False)

    assert report.status is GateStatus.BLOCKED
    assert any(
        check.name == "evidence.skills" and check.status == "BLOCKED"
        for check in report.checks
    )


def test_release_gate_rejects_short_workflow_and_fixture_backed_agent(
    tmp_path: Path, monkeypatch
) -> None:
    repo, sha = _git_repo(tmp_path)
    _passing_receipts(repo, sha)
    monkeypatch.setattr("eval.release_gate._git_head", lambda _: sha)
    workflow = next((repo / "data" / "eval" / "workflow" / "receipts").glob("*.json"))
    workflow_payload = json.loads(workflow.read_text(encoding="utf-8"))
    workflow_payload["workload"]["stage_count"] = 1
    workflow.write_text(json.dumps(workflow_payload) + "\n", encoding="utf-8")
    agent = next((repo / "data" / "eval" / "agent-e2e" / "receipts").glob("*.json"))
    agent_payload = json.loads(agent.read_text(encoding="utf-8"))
    agent_payload["provider"]["backed"] = False
    agent.write_text(json.dumps(agent_payload) + "\n", encoding="utf-8")

    report = evaluate_release(repo, run_tests=False)

    assert report.status is GateStatus.BLOCKED
    assert any(check.name == "evidence.workflow" and check.status == "BLOCKED" for check in report.checks)
    assert any(check.name == "evidence.agent-e2e" and check.status == "BLOCKED" for check in report.checks)


def test_release_gate_test_command_failure_is_fail_closed(tmp_path: Path, monkeypatch) -> None:
    repo, sha = _git_repo(tmp_path)
    _passing_receipts(repo, sha)
    monkeypatch.setattr("eval.release_gate._git_head", lambda _: sha)

    report = evaluate_release(repo, run_tests=True, test_commands=(("python", "-c", "raise SystemExit(7)"),))
    assert report.status is GateStatus.BLOCKED
    assert any(check.name == "tests.1" and check.status == "BLOCKED" for check in report.checks)


def test_release_gate_test_failure_detail_is_short_and_redacted(tmp_path: Path, monkeypatch) -> None:
    repo, sha = _git_repo(tmp_path)
    _passing_receipts(repo, sha)
    monkeypatch.setattr("eval.release_gate._git_head", lambda _: sha)

    report = evaluate_release(
        repo,
        run_tests=True,
        test_commands=(
            (
                "python",
                "-c",
                "import sys; print('OPENAI_API_KEY=fixture-secret-value', file=sys.stderr); print('Bearer bearer-secret-value'); print('failure marker'); raise SystemExit(7)",
            ),
        ),
    )

    check = next(check for check in report.checks if check.name == "tests.1")
    assert check.detail.startswith("exit_code=7; failure_tail=")
    assert "failure marker" in check.detail
    assert "fixture-secret-value" not in check.detail
    assert "bearer-secret-value" not in check.detail
    assert "<redacted>" in check.detail
