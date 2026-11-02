"""Original receipt provenance and invalidation through the public verification CLI."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

import pytest

pytestmark = pytest.mark.skipif(os.name != "nt", reason="the engineering runner requires Windows Job process ownership")


@pytest.fixture
def static_case(tmp_path: Path) -> dict:
    if shutil.which("gofmt") is None:
        pytest.skip("the real gofmt executable is required")
    repository = tmp_path / "repository"
    repository.mkdir()
    (repository / "input.go").write_bytes(b"package fixture\n\nfunc answer() int { return 42 }\n")
    for arguments in (["init", "--quiet"], ["add", "input.go"], ["-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixed static input"]):
        subprocess.run(["git", *arguments], cwd=repository, check=True, capture_output=True)
    plan = tmp_path / "plan.json"
    plan.write_text(json.dumps({"schema_version": 1, "scope_kind": "targeted", "scope_name": "one explicit readonly formatter check", "checks": [{"id": "gofmt", "argv": ["gofmt", "-l", "input.go"], "cwd": ".", "timeout_seconds": 10, "reuse_policy": "readonly_gofmt_files_v1"}]}), encoding="utf-8")
    return {"repository": repository, "plan": plan, "output": tmp_path / "receipts", "environment": {**os.environ, "PYTHONPATH": str(Path(__file__).resolve().parents[2])}}


def run_verification(case: dict, run_id: str, *, reuse_from: Path | None = None, environment: dict | None = None):
    command = [sys.executable, "-m", "scripts.verify", "--plan", str(case["plan"]), "--output", str(case["output"]), "--run-id", run_id]
    if reuse_from is not None:
        command.extend(["--reuse-from", str(reuse_from)])
    return subprocess.run(command, cwd=case["repository"], env=environment or case["environment"], capture_output=True, text=True, encoding="utf-8", timeout=40)


def original_pass(case: dict) -> tuple[Path, dict]:
    result = run_verification(case, "original")
    assert result.returncode == 0, result.stdout + result.stderr
    path = case["output"] / "original/summary.json"
    summary = json.loads(path.read_text(encoding="utf-8"))
    assert summary["status"] == "PASS" and summary["checks"][0]["executed_this_run"] is True
    return path, summary


def tree_hashes(root: Path) -> dict:
    return {path.relative_to(root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest() for path in root.rglob("*") if path.is_file()}


def reseal_fixture_checksums(root: Path) -> None:
    # Deliberately corrupt one schema field while keeping byte-integrity valid.
    lines = [f"{hashlib.sha256(path.read_bytes()).hexdigest()}  {path.relative_to(root).as_posix()}" for path in sorted(root.rglob("*")) if path.is_file() and path.name != "checksums.sha256"]
    (root / "checksums.sha256").write_text("\n".join(lines) + "\n", encoding="utf-8")


def assert_fresh_pass(case: dict, source: Path, *, environment: dict | None = None) -> dict:
    preserved = tree_hashes(source.parent)
    result = run_verification(case, "next", reuse_from=source, environment=environment)
    assert result.returncode == 0, result.stdout + result.stderr
    summary = json.loads((case["output"] / "next/summary.json").read_text(encoding="utf-8"))
    record = summary["checks"][0]
    assert summary["status"] == "PASS" and summary["attempted_checks"] == 1
    assert record["state"] == "passed" and record["executed_this_run"] is True and record["reused"] is False
    assert record["owned_root_pid"] > 0 and record["process_tree_stop_confirmed"] is True
    assert record["reuse_miss_reason"]
    assert preserved == tree_hashes(source.parent)
    return summary


@pytest.mark.parametrize("field,value", [
    ("planned_checks", True), ("attempted_checks", True), ("passed_checks", True),
    ("failed_checks", False), ("not_run_checks", False), ("reused_checks", False),
    ("exit_code", False), ("check_count", True), ("input_file_count", True), ("total", False),
])
def test_public_verification_refuses_boolean_numbers_in_original_evidence(static_case: dict, field: str, value: bool) -> None:
    source, summary = original_pass(static_case)
    target = summary["checks"][0] if field == "exit_code" else summary["checks"][0]["test_denominator"] if field in {"check_count", "input_file_count", "total"} else summary
    target[field] = value
    source.write_text(json.dumps(summary), encoding="utf-8")
    reseal_fixture_checksums(source.parent)
    assert_fresh_pass(static_case, source)


@pytest.mark.parametrize("changed", ["source", "configuration", "effective_environment"])
def test_public_verification_executes_again_when_closed_inputs_change(static_case: dict, tmp_path: Path, changed: str) -> None:
    source, original = original_pass(static_case)
    environment = static_case["environment"].copy()
    if changed == "source":
        (static_case["repository"] / "input.go").write_bytes(b"package fixture\n\nfunc answer() int { return 43 }\n")
    elif changed == "configuration":
        plan = json.loads(static_case["plan"].read_text(encoding="utf-8"))
        plan["checks"][0]["timeout_seconds"] = 11
        static_case["plan"].write_text(json.dumps(plan), encoding="utf-8")
    else:
        # This changes the actual child PATH/tool resolution, not an ignored parent ID.
        tool_path = shutil.which("gofmt")
        assert tool_path is not None
        tool = Path(tool_path)
        directory = tmp_path / "other-real-tool-path"
        directory.mkdir()
        shutil.copy2(tool, directory / tool.name)
        environment["PATH"] = str(directory) + os.pathsep + environment["PATH"]
    current = assert_fresh_pass(static_case, source, environment=environment)
    if changed == "effective_environment":
        key = "effective_environment_sha256"
        assert original["checks"][0]["reuse_inputs_before"]["material"][key] != current["checks"][0]["reuse_inputs_before"]["material"][key]


@pytest.mark.parametrize("changed", ["instructions", "command", "task_inputs"])
def test_public_verification_invalidates_changed_instruction_command_or_file_list(static_case: dict, changed: str) -> None:
    source, original = original_pass(static_case)
    plan = json.loads(static_case["plan"].read_text(encoding="utf-8"))
    if changed == "instructions":
        (static_case["repository"] / "AGENTS.md").write_text("Keep validation evidence complete.\n", encoding="utf-8")
    elif changed == "command":
        tool = shutil.which("gofmt")
        assert tool is not None
        plan["checks"][0]["argv"][0] = str(Path(tool).resolve())
        static_case["plan"].write_text(json.dumps(plan), encoding="utf-8")
    else:
        (static_case["repository"] / "additional.go").write_bytes(b"package fixture\n\nconst Another = 7\n")
        plan["checks"][0]["argv"].append("additional.go")
        static_case["plan"].write_text(json.dumps(plan), encoding="utf-8")
    current = assert_fresh_pass(static_case, source)
    before = original["checks"][0]["reuse_inputs_before"]
    after = current["checks"][0]["reuse_inputs_before"]
    assert current["checks"][0]["reuse_cost_threshold_seconds"] is None
    assert before["fingerprint"] != after["fingerprint"]
    if changed == "instructions":
        assert before["material"]["instructions"]["AGENTS.md"] == {"absent": True}
        assert "sha256" in after["material"]["instructions"]["AGENTS.md"]
    elif changed == "command":
        assert before["material"]["check_sha256"] != after["material"]["check_sha256"]
        assert before["material"]["tool"]["sha256"] == after["material"]["tool"]["sha256"]
    else:
        assert before["input_file_count"] == 1 and after["input_file_count"] == 2


def test_public_verification_invalidates_changed_loaded_dependency_bytes(static_case: dict, tmp_path: Path) -> None:
    implementation = Path(__file__).resolve().parents[2]
    deployment = tmp_path / "independent-implementation"
    sources = [implementation / "scripts/verify.py", implementation / "orchestrator/__init__.py", implementation / "orchestrator/session_control.py"]
    for directory in ("eval", "eval/harness", "orchestrator/security"):
        sources.extend((implementation / directory).glob("*.py"))
    for source_file in sources:
        destination = deployment / source_file.relative_to(implementation)
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(source_file, destination)
    static_case["environment"] = {**static_case["environment"], "PYTHONPATH": str(deployment)}
    source, original = original_pass(static_case)
    dependency = deployment / "eval/harness/process_tree.py"
    original_dependency_sha = hashlib.sha256(dependency.read_bytes()).hexdigest()
    dependency.write_bytes(dependency.read_bytes() + b"\n# Deployment dependency bytes changed.\n")
    current = assert_fresh_pass(static_case, source)
    before = original["checks"][0]["reuse_inputs_before"]["material"]["runtime"]
    after = current["checks"][0]["reuse_inputs_before"]["material"]["runtime"]
    assert current["checks"][0]["reuse_cost_threshold_seconds"] is None
    module = "eval.harness.process_tree"
    assert before["loaded_files"][before["module_bindings"][module]]["sha256"] == original_dependency_sha
    assert after["loaded_files"][after["module_bindings"][module]]["sha256"] == hashlib.sha256(dependency.read_bytes()).hexdigest()
    assert original_dependency_sha != hashlib.sha256(dependency.read_bytes()).hexdigest()
    assert original["inputs"]["source_pin_before"] == current["inputs"]["source_pin_before"]
    assert original["inputs"]["plan_sha256"] == current["inputs"]["plan_sha256"]


def test_public_verification_does_not_reuse_after_the_actual_formatter_is_replaced(static_case: dict, tmp_path: Path) -> None:
    official = shutil.which("gofmt")
    assert official is not None
    directory = tmp_path / "isolated-toolchain"
    directory.mkdir()
    tool = directory / Path(official).name
    shutil.copy2(official, tool)
    static_case["environment"] = {**static_case["environment"], "PATH": str(directory) + os.pathsep + static_case["environment"]["PATH"]}
    source, original = original_pass(static_case)
    preserved = tree_hashes(source.parent)
    # Only this owned copy becomes invalid; the installed formatter is untouched.
    tool.chmod(tool.stat().st_mode | 0o200)
    tool.write_bytes(b"not an executable: intentional toolchain failure fixture\n")
    result = run_verification(static_case, "next", reuse_from=source)
    assert result.returncode in (1, 3), result.stdout + result.stderr
    current = json.loads((static_case["output"] / "next/summary.json").read_text(encoding="utf-8"))
    record = current["checks"][0]
    assert current["status"] in {"FAIL", "BLOCKED"} and current["attempted_checks"] == 1
    assert record["reused"] is False and record["executed_this_run"] is True
    assert record["state"] in {"failed", "infra_error"} and record["reuse_miss_reason"]
    assert record["reuse_cost_threshold_seconds"] is None
    assert original["inputs"]["effective_environment_sha256"] == current["inputs"]["effective_environment_sha256"]
    assert current["inputs"]["tools"][0]["sha256"] == hashlib.sha256(tool.read_bytes()).hexdigest()
    assert original["inputs"]["tools"][0]["sha256"] != current["inputs"]["tools"][0]["sha256"]
    assert preserved == tree_hashes(source.parent)


@pytest.mark.parametrize("damage", ["log", "null_closure", "list_closure"])
def test_public_verification_keeps_invalid_prior_evidence_and_executes_fresh(static_case: dict, damage: str) -> None:
    source, summary = original_pass(static_case)
    if damage == "log":
        (source.parent / summary["checks"][0]["stdout_log"]).write_text("damaged prior log", encoding="utf-8")
    else:
        unknown = None if damage == "null_closure" else []
        summary["checks"][0].update(reuse_inputs_before=unknown, reuse_inputs_after=unknown)
        source.write_text(json.dumps(summary), encoding="utf-8")
        reseal_fixture_checksums(source.parent)
    assert_fresh_pass(static_case, source)


def test_public_verification_executes_when_verifying_reuse_costs_as_much_as_execution(static_case: dict) -> None:
    source, summary = original_pass(static_case)
    # A zero-duration fixture makes the cost boundary deterministic on any host.
    summary["checks"][0]["elapsed_seconds"] = 0.0
    source.write_text(json.dumps(summary), encoding="utf-8")
    reseal_fixture_checksums(source.parent)
    current = assert_fresh_pass(static_case, source)
    record = current["checks"][0]
    assert record["reuse_miss_reason"] == "verification_cost_not_lower_than_execution"
    assert record["reuse_lookup_seconds"] > 0


def test_public_verification_keeps_formatter_failure_and_does_not_reuse_it(static_case: dict) -> None:
    (static_case["repository"] / "input.go").write_bytes(b"package fixture\nfunc answer()int{return 42}\n")
    original = run_verification(static_case, "original")
    assert original.returncode == 1, original.stdout + original.stderr
    source = static_case["output"] / "original/summary.json"
    preserved = tree_hashes(source.parent)
    current = run_verification(static_case, "next", reuse_from=source)
    assert current.returncode == 1, current.stdout + current.stderr
    summary = json.loads((static_case["output"] / "next/summary.json").read_text(encoding="utf-8"))
    assert summary["status"] == "FAIL" and summary["attempted_checks"] == 1 and summary["reused_checks"] == 0
    assert summary["checks"][0]["error_code"] == "formatter_output_not_empty"
    assert preserved == tree_hashes(source.parent)


def test_full_verification_refuses_reuse_parameters_before_dispatch(static_case: dict) -> None:
    source, _ = original_pass(static_case)
    static_case["plan"].write_text(json.dumps({"schema_version": 1, "scope_kind": "full"}), encoding="utf-8")
    result = run_verification(static_case, "full", reuse_from=source)
    assert result.returncode == 2
    assert "always executes fresh" in json.loads(result.stdout)["error"]
    assert not (static_case["output"] / "full").exists()


def test_public_verification_refuses_an_invalid_original_execution_timestamp(static_case: dict) -> None:
    source, summary = original_pass(static_case)
    summary["checks"][0]["started_utc"] = "not-a-timestamp"
    source.write_text(json.dumps(summary), encoding="utf-8")
    reseal_fixture_checksums(source.parent)
    assert_fresh_pass(static_case, source)


@pytest.mark.parametrize("command_kind", ["python", "pytest"])
def test_public_verification_does_not_reuse_unclosed_python_commands(static_case: dict, tmp_path: Path, command_kind: str) -> None:
    counter = tmp_path / "executions.txt"
    code = f"from pathlib import Path; p=Path({str(counter)!r}); p.open('a').write('executed\\n')"
    if command_kind == "python":
        arguments = [sys.executable, "-c", code]
    else:
        (static_case["repository"] / ".gitignore").write_text("__pycache__/\n.pytest_cache/\n", encoding="utf-8")
        (static_case["repository"] / "test_probe.py").write_text("def test_probe():\n    " + code + "\n", encoding="utf-8")
        arguments = [sys.executable, "-m", "pytest", "-q", "-p", "no:phoenix", "test_probe.py"]
    plan = json.loads(static_case["plan"].read_text(encoding="utf-8"))
    plan["checks"][0].update(argv=arguments, timeout_seconds=20)
    static_case["plan"].write_text(json.dumps(plan), encoding="utf-8")
    source, _ = original_pass(static_case)
    assert_fresh_pass(static_case, source)
    assert counter.read_text(encoding="utf-8").splitlines() == ["executed", "executed"]


def test_public_verification_reuses_an_original_static_pass_without_a_new_process(tmp_path: Path) -> None:
    if shutil.which("gofmt") is None:
        pytest.skip("the real gofmt executable is required")
    repository = tmp_path / "repository"
    repository.mkdir()
    # Parsing real declarations is deliberately costlier than bounded fingerprinting.
    (repository / "input.go").write_bytes(b"package fixture\n\n" + b"".join(f"const Value{index:06d} = 0\n".encode() for index in range(100_000)))
    for arguments in (
        ["init", "--quiet"],
        ["add", "input.go"],
        ["-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixed static input"],
    ):
        subprocess.run(["git", *arguments], cwd=repository, check=True, capture_output=True)
    plan = tmp_path / "plan.json"
    plan.write_text(json.dumps({
        "schema_version": 1,
        "scope_kind": "targeted",
        "scope_name": "one explicit readonly formatter check",
        "checks": [{
            "id": "gofmt",
            "argv": ["gofmt", "-l", "input.go"],
            "cwd": ".",
            "timeout_seconds": 10,
            "reuse_policy": "readonly_gofmt_files_v1",
        }],
    }), encoding="utf-8")
    implementation_root = Path(__file__).resolve().parents[2]
    environment = {**os.environ, "PYTHONPATH": str(implementation_root)}
    output = tmp_path / "receipts"
    command = [sys.executable, "-m", "scripts.verify", "--plan", str(plan), "--output", str(output)]
    original = subprocess.run([*command, "--run-id", "original"], cwd=repository, env=environment, capture_output=True, text=True, encoding="utf-8", timeout=40)
    assert original.returncode == 0, original.stdout + original.stderr
    original_root = output / "original"
    original_path = original_root / "summary.json"
    original_summary = json.loads(original_path.read_text(encoding="utf-8"))
    original_check = original_summary["checks"][0]
    assert original_summary["status"] == "PASS" and original_summary["attempted_checks"] == 1
    assert original_check["state"] == "passed" and original_check["executed_this_run"] is True
    assert original_check["reused"] is False and original_check["owned_root_pid"] > 0
    assert original_check["process_tree_stop_confirmed"] is True
    assert original_check["test_denominator"]["kind"] == "static_files"
    assert original_check["test_denominator"]["check_count"] == original_check["test_denominator"]["input_file_count"] == 1
    assert (original_root / original_check["stdout_log"]).read_text(encoding="utf-8") == ""
    preserved = {path.relative_to(original_root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest() for path in original_root.rglob("*") if path.is_file()}

    reused = subprocess.run([*command, "--run-id", "reused", "--reuse-from", str(original_path)], cwd=repository, env=environment, capture_output=True, text=True, encoding="utf-8", timeout=40)
    assert reused.returncode == 0, reused.stdout + reused.stderr
    current_root = output / "reused"
    current_summary = json.loads((current_root / "summary.json").read_text(encoding="utf-8"))
    record = current_summary["checks"][0]
    assert current_summary["status"] == "PASS" and current_summary["attempted_checks"] == 0
    assert record["state"] == "reused" and record["executed_this_run"] is False and record["reused"] is True
    assert "owned_root_pid" not in record
    assert record["started_utc"] is None and record["finished_utc"] is None and record["elapsed_seconds"] is None
    reference = record["reuse"]
    assert reference["source_summary"] == "../original/summary.json"
    assert reference["source_summary_sha256"] == preserved["summary.json"]
    assert reference["source_checksums_sha256"] == preserved["checksums.sha256"]
    assert reference["source_check"]["started_utc"] == original_check["started_utc"]
    assert reference["source_check"]["finished_utc"] == original_check["finished_utc"]
    assert reference["source_check"]["elapsed_seconds"] == original_check["elapsed_seconds"]
    assert reference["source_check"]["process_tree_stop_confirmed"] is True
    assert record["test_denominator"] == original_check["test_denominator"]
    assert reference["denominator_source"] == "original_execution"
    for channel in ("stdout", "stderr"):
        assert record[channel + "_log"] == "../original/" + original_check[channel + "_log"]
        assert hashlib.sha256((current_root / record[channel + "_log"]).read_bytes()).hexdigest() == original_check[channel + "_sha256"]
    assert not list((current_root / "scorer").glob("*"))
    assert preserved == {path.relative_to(original_root).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest() for path in original_root.rglob("*") if path.is_file()}
    for line in (current_root / "checksums.sha256").read_text(encoding="utf-8").splitlines():
        digest, relative = line.split("  ", 1)
        assert hashlib.sha256((current_root / relative).read_bytes()).hexdigest() == digest
