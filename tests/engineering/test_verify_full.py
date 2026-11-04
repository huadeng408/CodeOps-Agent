"""The fixed full recipe executes real tools in a small isolated repository."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

import pytest

pytestmark = pytest.mark.skipif(os.name != "nt", reason="the engineering runner requires Windows Job process ownership")


def test_full_verification_executes_again_with_an_existing_matching_pass(tmp_path: Path) -> None:
    if shutil.which("go") is None:
        pytest.skip("the real Go toolchain is required")
    repository = tmp_path / "repository"
    repository.mkdir()
    files = {
        ".gitignore": "__pycache__/\n.pytest_cache/\n.runs\neval/swebench_work/repos/\n",
        "go.mod": "module fixture\n\n" + next(line for line in (Path(__file__).resolve().parents[2] / "go.mod").read_text().splitlines() if line.startswith("go ")) + "\n",
        "fixture.go": "package fixture\n",
        "fixture_test.go": 'package fixture\nimport "testing"\nfunc TestPass(t *testing.T) {}\n',
        "cmd/agent/main.go": "package main\nfunc main() {}\n",
        "pyproject.toml": '[tool.pytest.ini_options]\naddopts = ["-p", "no:phoenix"]\n',
        "test_probe.py": "from pathlib import Path\ndef test_probe():\n    with Path('.runs').open('a') as output: output.write('executed\\n')\n",
        "eval/swebench_work/repos/foreign/intentionally_invalid.py": "this is deliberately invalid python !!!\n",
    }
    for name in ("orchestrator", "codeagent", "eval", "scripts"):
        files[name + "/compile_probe.py"] = "VALUE = 1\n"
    for relative, content in files.items():
        path = repository / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(content.encode())
    for arguments in (["init", "--quiet"], ["add", "."], ["-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixed full input"]):
        subprocess.run(["git", *arguments], cwd=repository, check=True, capture_output=True)
    plan = tmp_path / "full.json"
    plan.write_text(json.dumps({"schema_version": 1, "scope_kind": "full"}), encoding="utf-8")
    environment = {**os.environ, "PYTHONPATH": str(Path(__file__).resolve().parents[2])}
    for name in list(environment):
        if name.startswith(("CODE_AGENT_RUN_", "CODEOPS_RUN_")):
            environment.pop(name)
    output = tmp_path / "receipts"
    command = [sys.executable, "-m", "scripts.verify", "--plan", str(plan), "--output", str(output)]
    for run_id in ("original", "again"):
        result = subprocess.run([*command, "--run-id", run_id], cwd=repository, env=environment, capture_output=True, text=True, encoding="utf-8", timeout=120)
        assert result.returncode == 0, result.stdout + result.stderr
        receipt = json.loads((output / run_id / "summary.json").read_text(encoding="utf-8"))
        assert receipt["status"] == "PASS" and receipt["scope_kind"] == "full"
        assert receipt["inputs"]["go_toolchain"]["automatic_downloads"] is False
        assert receipt["inputs"]["go_toolchain"]["sha256"] == receipt["inputs"]["tools"][0]["sha256"]
        compiled = (output / run_id / receipt["inputs"]["python_compile_sources"]["path"]).read_text(encoding="utf-8").splitlines()
        assert "orchestrator/compile_probe.py" in compiled
        assert all("eval/swebench_work/repos/" not in path for path in compiled)
        assert receipt["planned_checks"] == receipt["attempted_checks"] == receipt["passed_checks"] == 7
        assert receipt["reused_checks"] == 0
        assert all(check["executed_this_run"] is True and check["reused"] is False and check["process_tree_stop_confirmed"] is True for check in receipt["checks"])
        assert (output / run_id / "bin/agent.exe").is_file()
        for check in receipt["checks"]:
            assert (output / run_id / check["stdout_log"]).is_file()
            assert check["test_denominator"]["complete"] is True
    assert (repository / ".runs").read_text(encoding="utf-8").splitlines() == ["executed", "executed"]
    invalid = repository / "eval/invalid_runtime.py"
    invalid.write_bytes(b"VALUE = 1\n")
    subprocess.run([sys.executable, "-m", "compileall", "-q", str(invalid)], cwd=repository, check=True, capture_output=True)
    previous = invalid.stat()
    invalid.write_bytes(b"VALUE = !\n")
    os.utime(invalid, ns=(previous.st_atime_ns, previous.st_mtime_ns))
    (repository / "README.md").write_bytes(b"staged trailing whitespace \n")
    subprocess.run(["git", "add", "README.md"], cwd=repository, check=True, capture_output=True)
    result = subprocess.run([*command, "--run-id", "bad-source"], cwd=repository, env=environment, capture_output=True, text=True, encoding="utf-8", timeout=120)
    assert result.returncode == 1, result.stdout + result.stderr
    failed = json.loads((output / "bad-source/summary.json").read_text(encoding="utf-8"))
    assert failed["status"] == "FAIL" and failed["attempted_checks"] == 5
    assert failed["planned_checks"] == 7 and failed["not_run_checks"] == 2
    assert next(check for check in failed["checks"] if check["id"] == "python-compile")["state"] == "failed"
    assert next(check for check in failed["checks"] if check["id"] == "diff-check")["state"] == "not_run"
    assert "eval/invalid_runtime.py" in (output / "bad-source/python-sources.txt").read_text(encoding="utf-8").splitlines()


def test_full_verification_refuses_an_uncached_sdk_without_running_checks(tmp_path: Path) -> None:
    repository = tmp_path / "repository"
    repository.mkdir()
    (repository / "go.mod").write_bytes(b"module fixture\n\ngo 1.999.999\n")
    for arguments in (["init", "--quiet"], ["add", "."], ["-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "unavailable SDK input"]):
        subprocess.run(["git", *arguments], cwd=repository, check=True, capture_output=True)
    plan = tmp_path / "full.json"
    plan.write_text(json.dumps({"schema_version": 1, "scope_kind": "full"}), encoding="utf-8")
    output = tmp_path / "receipts"
    result = subprocess.run([sys.executable, "-m", "scripts.verify", "--plan", str(plan), "--output", str(output), "--run-id", "refused"], cwd=repository, env={**os.environ, "PYTHONPATH": str(Path(__file__).resolve().parents[2])}, capture_output=True, text=True, encoding="utf-8", timeout=40)
    assert result.returncode == 2, result.stdout + result.stderr
    assert json.loads(result.stdout)["status"] == "BLOCKED" and "toolchain" in json.loads(result.stdout)["error"]
    assert not output.exists()
