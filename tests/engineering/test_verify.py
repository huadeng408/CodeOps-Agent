"""Public CLI receipts, logs and actual Windows-owned process lifecycle."""
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time

import pytest

pytestmark = pytest.mark.skipif(os.name != "nt", reason="the engineering runner requires Windows Job process ownership")


@pytest.fixture
def repository(tmp_path):
    repo = tmp_path / "repo"
    repo.mkdir()
    (repo / "input.txt").write_text("fixed input\n", encoding="utf-8")
    for args in (["init", "--quiet"], ["add", "input.txt"], ["-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "baseline"]):
        subprocess.run(["git", *args], cwd=repo, check=True, capture_output=True)
    return repo


def verification_command(repo, plan, output, run_id="target", *extra):
    root = Path(__file__).resolve().parents[2]
    return subprocess.run([sys.executable, "-m", "scripts.verify", "--plan", str(plan), "--output", str(output), "--run-id", run_id, *extra], cwd=repo, env={**os.environ, "PYTHONPATH": str(root)}, capture_output=True, text=True, encoding="utf-8", timeout=40)


def write_plan(path, checks):
    path.write_text(json.dumps({"schema_version": 1, "scope_kind": "targeted", "scope_name": "explicit fixture checks", "checks": checks}), encoding="utf-8")
    return path


def test_public_verification_keeps_scope_logs_and_complete_check_denominator(tmp_path):
    repo = tmp_path / "repo"
    repo.mkdir()
    (repo / "input.txt").write_text("fixed input\n", encoding="utf-8")
    for args in (["init", "--quiet"], ["add", "input.txt"], ["-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "baseline"]):
        subprocess.run(["git", *args], cwd=repo, check=True, capture_output=True)
    plan = tmp_path / "plan.json"
    plan.write_text(json.dumps({"schema_version": 1, "scope_kind": "targeted", "scope_name": "one real process", "checks": [{"id": "success", "argv": [sys.executable, "-c", "from pathlib import Path; import sys; Path(sys.argv[1]).write_text('done'); print('fixture completed')", str(tmp_path / "completed.txt")], "cwd": ".", "timeout_seconds": 10}]}), encoding="utf-8")
    root = Path(__file__).resolve().parents[2]
    environment = {**os.environ, "PYTHONPATH": str(root)}
    result = subprocess.run([sys.executable, "-m", "scripts.verify", "--plan", str(plan), "--output", str(tmp_path / "receipts"), "--run-id", "first-target"], cwd=repo, env=environment, capture_output=True, text=True, encoding="utf-8", timeout=40)
    assert result.returncode == 0, result.stderr
    concise = json.loads(result.stdout)
    assert concise["status"] == "PASS" and len(result.stdout) < 4096
    artifact = tmp_path / "receipts/first-target"
    receipt = json.loads((artifact / "summary.json").read_text(encoding="utf-8"))
    assert receipt["scope_kind"] == "targeted" and receipt["scope_name"] == "one real process"
    assert receipt["planned_checks"] == receipt["attempted_checks"] == receipt["passed_checks"] == 1
    assert receipt["not_run_checks"] == 0 and receipt["checks"][0]["executed_this_run"] is True
    assert receipt["inputs"]["input_closure"]["complete"] is False
    assert receipt["checks"][0]["process_tree_stop_confirmed"] is True
    assert (tmp_path / "completed.txt").read_text() == "done"
    for raw in (artifact / "checksums.sha256").read_text(encoding="utf-8").splitlines():
        digest, relative = raw.split("  ", 1)
        assert hashlib.sha256((artifact / relative).read_bytes()).hexdigest() == digest
    assert "fixture completed" in (artifact / receipt["checks"][0]["stdout_log"]).read_text(encoding="utf-8")


def test_public_verification_rejects_output_that_would_change_source_inputs(tmp_path, repository):
    marker = tmp_path / "must-not-run.txt"
    plan = write_plan(tmp_path / "plan.json", [{"id": "unignored-output", "argv": [sys.executable, "-c", "from pathlib import Path; import sys; Path(sys.argv[1]).write_text('ran')", str(marker)]}])
    result = verification_command(repository, plan, repository / "unignored-output")
    assert result.returncode == 2
    assert json.loads(result.stdout)["status"] == "BLOCKED"
    assert not marker.exists()
    assert not (repository / "unignored-output").exists()


def test_public_verification_preserves_actual_pytest_pass_skip_denominator(tmp_path, repository):
    (repository / ".gitignore").write_text("__pycache__/\n.pytest_cache/\n", encoding="utf-8")
    (repository / "test_case.py").write_text("import pytest\ndef test_pass(): assert True\n@pytest.mark.skip(reason='retained skip')\ndef test_skip(): pass\n", encoding="utf-8")
    plan = write_plan(tmp_path / "plan.json", [{"id": "pytest", "argv": [sys.executable, "-m", "pytest", "-q", "-p", "no:phoenix", "test_case.py"], "timeout_seconds": 20}])
    result = verification_command(repository, plan, tmp_path / "receipts")
    assert result.returncode == 0, result.stderr
    receipt = json.loads((tmp_path / "receipts/target/summary.json").read_text(encoding="utf-8"))
    denominator = receipt["checks"][0]["test_denominator"]
    assert denominator["kind"] == "pytest" and denominator["complete"] is True
    assert denominator["passed"] == 1 and denominator["failed"] == 0 and denominator["skipped"] == 1 and denominator["total"] == 2


@pytest.mark.parametrize("early_stop_source", ["environment", "configuration"])
def test_public_verification_does_not_claim_a_complete_denominator_after_pytest_stops_early(tmp_path, repository, monkeypatch, early_stop_source):
    (repository / ".gitignore").write_text("__pycache__/\n.pytest_cache/\n", encoding="utf-8")
    (repository / "test_case.py").write_text("def test_first(): assert False\ndef test_second(): assert False\n", encoding="utf-8")
    if early_stop_source == "environment":
        monkeypatch.setenv("PYTEST_ADDOPTS", "-x -p no:phoenix")
    else:
        monkeypatch.delenv("PYTEST_ADDOPTS", raising=False)
        (repository / "pyproject.toml").write_text('[tool.pytest.ini_options]\naddopts = ["-x", "-p", "no:phoenix"]\n', encoding="utf-8")
    plan = write_plan(tmp_path / "plan.json", [{"id": "pytest", "argv": [sys.executable, "-m", "pytest", "-q", "test_case.py"], "timeout_seconds": 20}])
    result = verification_command(repository, plan, tmp_path / "receipts")
    assert result.returncode == 3
    receipt = json.loads((tmp_path / "receipts/target/summary.json").read_text(encoding="utf-8"))
    record = receipt["checks"][0]
    assert record["source_state"] == "failed" and record["error_code"] == "test_denominator_unconfirmed"
    assert record["test_denominator"]["complete"] is False and record["test_denominator"]["total"] is None
    assert record["test_denominator"]["failed"] == 1


@pytest.mark.parametrize("early_stop_source", ["argv", "environment"])
def test_public_verification_does_not_claim_a_complete_go_denominator_after_failfast(tmp_path, repository, monkeypatch, early_stop_source):
    monkeypatch.setenv("GOPROXY", "off")
    monkeypatch.setenv("GOSUMDB", "off")
    monkeypatch.setenv("GOTOOLCHAIN", "local")
    monkeypatch.setenv("GOFLAGS", "-failfast" if early_stop_source == "environment" else "")
    (repository / "go.mod").write_bytes(b"module fixture\n\ngo 1.24\n")
    (repository / "fixture_test.go").write_bytes(b'package fixture\nimport "testing"\nfunc TestFirst(t *testing.T) { t.Fatal("retained failure") }\nfunc TestSecond(t *testing.T) { t.Fatal("must remain unstarted") }\n')
    arguments = ["go", "test", "-vet=off", "-p", "1", "-json", "./...", "-count=1"]
    if early_stop_source == "argv":
        arguments.append("-failfast")
    plan = write_plan(tmp_path / "plan.json", [{"id": "go-test", "argv": arguments, "timeout_seconds": 30}])
    result = verification_command(repository, plan, tmp_path / "receipts")
    assert result.returncode == 3
    record = json.loads((tmp_path / "receipts/target/summary.json").read_text(encoding="utf-8"))["checks"][0]
    assert record["source_state"] == "failed" and record["error_code"] == "test_denominator_unconfirmed"
    assert record["test_denominator"]["complete"] is False and record["test_denominator"]["total"] is None
    assert record["test_denominator"]["test_events"] == {"pass": 0, "fail": 1, "skip": 0}


def test_public_verification_preserves_actual_go_json_test_denominator(tmp_path, repository, monkeypatch):
    monkeypatch.setenv("GOPROXY", "off")
    monkeypatch.setenv("GOSUMDB", "off")
    monkeypatch.setenv("GOTOOLCHAIN", "local")
    (repository / "go.mod").write_text("module fixture\n\ngo 1.24\n", encoding="utf-8")
    (repository / "fixture_test.go").write_text('package fixture\nimport "testing"\nfunc TestPass(t *testing.T) {}\nfunc TestSkip(t *testing.T) {t.Skip("retained skip")}\n', encoding="utf-8")
    plan = write_plan(tmp_path / "plan.json", [{"id": "go-test", "argv": ["go", "test", "-p", "1", "-json", "./...", "-count=1"], "timeout_seconds": 30}])
    result = verification_command(repository, plan, tmp_path / "receipts")
    assert result.returncode == 0, result.stderr
    receipt = json.loads((tmp_path / "receipts/target/summary.json").read_text(encoding="utf-8"))
    denominator = receipt["checks"][0]["test_denominator"]
    assert denominator["kind"] == "go-test-json" and denominator["complete"] is True
    assert denominator["test_events"] == {"pass": 1, "fail": 0, "skip": 1}
    assert denominator["total"] == 2 and denominator["package_events"]["pass"] == 1


def test_public_verification_keeps_missing_test_totals_unknown(tmp_path, repository):
    (repository / ".gitignore").write_text("__pycache__/\n.pytest_cache/\n", encoding="utf-8")
    (repository / "test_case.py").write_text("def test_pass(): assert True\n", encoding="utf-8")
    plan = write_plan(tmp_path / "plan.json", [{"id": "quiet-pytest", "argv": [sys.executable, "-m", "pytest", "-qq", "-p", "no:phoenix", "test_case.py"], "timeout_seconds": 20}])
    result = verification_command(repository, plan, tmp_path / "receipts")
    assert result.returncode == 3, result.stderr
    receipt = json.loads((tmp_path / "receipts/target/summary.json").read_text(encoding="utf-8"))
    check = receipt["checks"][0]
    assert check["exit_code"] == 0 and check["state"] == "infra_error"
    assert check["test_denominator"]["complete"] is False and check["test_denominator"]["total"] is None


def test_public_verification_keeps_failed_exit_and_later_checks_and_rejects_overwrite(tmp_path, repository):
    marker = tmp_path / "later.txt"
    plan = write_plan(tmp_path / "plan.json", [
        {"id": "failure", "argv": [sys.executable, "-c", "import sys; print('known failure',file=sys.stderr); sys.exit(7)"]},
        {"id": "later", "argv": [sys.executable, "-c", "from pathlib import Path; import sys; Path(sys.argv[1]).write_text('done')", str(marker)]},
    ])
    output = tmp_path / "receipts"
    result = verification_command(repository, plan, output)
    assert result.returncode == 1, result.stderr
    receipt = json.loads((output / "target/summary.json").read_text(encoding="utf-8"))
    assert receipt["planned_checks"] == receipt["attempted_checks"] == 2
    assert receipt["failed_checks"] == receipt["passed_checks"] == 1 and receipt["not_run_checks"] == 0
    assert receipt["checks"][0]["exit_code"] == 7 and marker.exists()
    before = {path.relative_to(output): path.read_bytes() for path in (output / "target").rglob("*") if path.is_file()}
    again = verification_command(repository, plan, output)
    assert again.returncode == 2
    assert before == {path.relative_to(output): path.read_bytes() for path in (output / "target").rglob("*") if path.is_file()}


def process_is_running(pid):
    if os.name == "nt":
        import ctypes
        from ctypes import wintypes
        kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        kernel.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
        kernel.OpenProcess.restype = wintypes.HANDLE
        kernel.WaitForSingleObject.argtypes = [wintypes.HANDLE, wintypes.DWORD]
        kernel.WaitForSingleObject.restype = wintypes.DWORD
        kernel.CloseHandle.argtypes = [wintypes.HANDLE]
        handle = kernel.OpenProcess(0x100000, False, pid)
        if not handle:
            assert ctypes.get_last_error() == 87
            return False
        try:
            status = kernel.WaitForSingleObject(handle, 0)
            assert status in (0, 258)
            return status == 258
        finally:
            kernel.CloseHandle(handle)
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    status = Path(f"/proc/{pid}/stat")
    if status.exists() and status.read_text().split(") ", 1)[1].startswith("Z"):
        return False
    return True


def test_public_verification_timeout_reaps_owned_children_and_preserves_unrelated_process(tmp_path, repository):
    identities = tmp_path / "owned.json"
    helper = "import json,os,subprocess,sys,time; from pathlib import Path; child=subprocess.Popen([sys.executable,'-c','import time; time.sleep(120)']); Path(sys.argv[1]).write_text(json.dumps({'pid':os.getpid(),'child':child.pid})); time.sleep(120)"
    plan = write_plan(tmp_path / "plan.json", [{"id": "timeout", "argv": [sys.executable, "-c", helper, str(identities)], "timeout_seconds": 2}])
    unrelated = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(120)"])
    try:
        result = verification_command(repository, plan, tmp_path / "receipts")
        assert result.returncode == 3, result.stderr
        receipt = json.loads((tmp_path / "receipts/target/summary.json").read_text(encoding="utf-8"))
        check = receipt["checks"][0]
        assert check["state"] == "timed_out" and check["timed_out"] is True and check["process_tree_stop_confirmed"] is True
        owned = json.loads(identities.read_text())
        assert not process_is_running(owned["pid"]) and not process_is_running(owned["child"])
        assert unrelated.poll() is None
    finally:
        unrelated.kill()
        unrelated.wait(timeout=10)


def test_public_verification_serializes_managed_checks_with_bounded_queue(tmp_path, repository, monkeypatch):
    (repository / "go.mod").write_bytes(b"module fixture\n\ngo 1.24\n")
    started, release, second_marker = (tmp_path / name for name in ("started", "release", "second"))
    helper = "import sys,time; from pathlib import Path; Path(sys.argv[1]).write_text('started'); end=time.monotonic()+20\nwhile not Path(sys.argv[2]).exists() and time.monotonic()<end: time.sleep(.02)\n"
    first_plan = write_plan(tmp_path / "first.json", [{"id": "held", "argv": [sys.executable, "-c", helper, str(started), str(release)], "timeout_seconds": 25}])
    second_plan = write_plan(tmp_path / "second.json", [{"id": "second", "argv": [sys.executable, "-c", "from pathlib import Path; import sys; Path(sys.argv[1]).write_text('done')", str(second_marker)]}])
    root = Path(__file__).resolve().parents[2]
    output = tmp_path / "receipts"
    first = subprocess.Popen([sys.executable, "-m", "scripts.verify", "--plan", str(first_plan), "--output", str(output), "--run-id", "held"], cwd=repository, env={**os.environ, "PYTHONPATH": str(root)}, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8")
    try:
        deadline = time.monotonic() + 15
        while not started.exists() and first.poll() is None and time.monotonic() < deadline:
            time.sleep(.02)
        assert started.exists()
        blocked = verification_command(repository, second_plan, output, "queued", "--queue-timeout-seconds", ".1")
        assert blocked.returncode == 3 and not second_marker.exists()
        queued = json.loads((output / "queued/summary.json").read_text(encoding="utf-8"))
        assert queued["attempted_checks"] == 0 and queued["not_run_checks"] == queued["planned_checks"] == 1
        assert first.poll() is None
        monkeypatch.setenv("PYTEST_ADDOPTS", "-k must_not_shrink_full_scope")
        monkeypatch.setenv("GOFLAGS", "-run=MustNotShrinkFullScope")
        full_plan = tmp_path / "full.json"
        full_plan.write_text(json.dumps({"schema_version": 1, "scope_kind": "full"}), encoding="utf-8")
        full = verification_command(repository, full_plan, output, "full-queued", "--queue-timeout-seconds", ".1")
        assert full.returncode == 3 and first.poll() is None
        full_receipt = json.loads((output / "full-queued/summary.json").read_text(encoding="utf-8"))
        assert full_receipt["attempted_checks"] == 0
        assert full_receipt["inputs"]["environment_adjustments"]["PYTEST_ADDOPTS"] == "cleared"
        assert full_receipt["inputs"]["environment_adjustments"]["GOFLAGS"] == "cleared"
        release.write_text("release")
        stdout, stderr = first.communicate(timeout=15)
        assert first.returncode == 0, (stdout, stderr)
        after = verification_command(repository, second_plan, output, "after", "--queue-timeout-seconds", ".1")
        assert after.returncode == 0 and second_marker.exists()
    finally:
        release.touch()
        if first.poll() is None:
            first.kill()
        first.communicate(timeout=10)


def test_public_verification_full_plan_rejects_arbitrary_checks(tmp_path, repository):
    plan = tmp_path / "full.json"
    plan.write_text(json.dumps({"schema_version": 1, "scope_kind": "full", "checks": [{"id": "fake", "argv": [sys.executable, "-c", "pass"]}]}), encoding="utf-8")
    result = verification_command(repository, plan, tmp_path / "receipts")
    assert result.returncode == 2 and not (tmp_path / "receipts/target").exists()


def test_public_verification_keeps_executed_attempt_when_log_persistence_fails(tmp_path, repository):
    output = tmp_path / "receipts"
    later = tmp_path / "must-not-run"
    log_target = output / "target/scorer/collision-stdout.log"
    plan = write_plan(tmp_path / "plan.json", [
        {"id": "collision", "argv": [sys.executable, "-c", "from pathlib import Path; import sys; Path(sys.argv[1]).mkdir(parents=True)", str(log_target)]},
        {"id": "later", "argv": [sys.executable, "-c", "from pathlib import Path; import sys; Path(sys.argv[1]).write_text('ran')", str(later)]},
    ])
    result = verification_command(repository, plan, output)
    assert result.returncode == 3
    receipt = json.loads((output / "target/summary.json").read_text(encoding="utf-8"))
    assert receipt["planned_checks"] == 2 and receipt["attempted_checks"] == receipt["not_run_checks"] == 1
    assert receipt["checks"][0]["executed_this_run"] is True
    assert receipt["checks"][0]["exit_code"] == 0 and receipt["checks"][0]["process_tree_stop_confirmed"] is True
    assert receipt["checks"][0]["error_code"] == "log_persistence_unavailable"
    assert not later.exists()


def test_public_verification_holder_exit_releases_slot_and_stops_owned_tree(tmp_path, repository):
    identity, completed = tmp_path / "owned.json", tmp_path / "after.txt"
    helper = "import json,os,subprocess,sys,time; from pathlib import Path; child=subprocess.Popen([sys.executable,'-c','import time; time.sleep(120)']); Path(sys.argv[1]).write_text(json.dumps({'pid':os.getpid(),'child':child.pid})); time.sleep(120)"
    first_plan = write_plan(tmp_path / "held.json", [{"id": "held", "argv": [sys.executable, "-c", helper, str(identity)], "timeout_seconds": 60}])
    after_plan = write_plan(tmp_path / "after.json", [{"id": "after", "argv": [sys.executable, "-c", "from pathlib import Path; import sys; Path(sys.argv[1]).write_text('done')", str(completed)]}])
    root = Path(__file__).resolve().parents[2]
    output = tmp_path / "receipts"
    first = subprocess.Popen([sys.executable, "-m", "scripts.verify", "--plan", str(first_plan), "--output", str(output), "--run-id", "held"], cwd=repository, env={**os.environ, "PYTHONPATH": str(root)}, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, encoding="utf-8")
    try:
        deadline = time.monotonic() + 15
        while not identity.exists() and first.poll() is None and time.monotonic() < deadline:
            time.sleep(.02)
        assert identity.exists()
        owned = json.loads(identity.read_text())
        first.kill()
        first.communicate(timeout=10)
        deadline = time.monotonic() + 5
        while any(process_is_running(pid) for pid in owned.values()) and time.monotonic() < deadline:
            time.sleep(.02)
        assert not any(process_is_running(pid) for pid in owned.values())
        after = verification_command(repository, after_plan, output, "after", "--queue-timeout-seconds", ".1")
        assert after.returncode == 0 and completed.exists()
        assert not (output / "held/summary.json").exists()
    finally:
        if first.poll() is None:
            first.kill()
        first.communicate(timeout=10)


def test_public_verification_interrupt_seals_attempt_and_respects_stop_intent(tmp_path, repository):
    identity, later = tmp_path / "ready.json", tmp_path / "must-not-run"
    helper = "import json,os,subprocess,sys,time; from pathlib import Path; child=subprocess.Popen([sys.executable,'-c','import time; time.sleep(120)']); Path(sys.argv[1]).write_text(json.dumps({'pid':os.getpid(),'child':child.pid})); time.sleep(120)"
    plan = write_plan(tmp_path / "plan.json", [
        {"id": "interrupted", "argv": [sys.executable, "-c", helper, str(identity)], "timeout_seconds": 30},
        {"id": "later", "argv": [sys.executable, "-c", "from pathlib import Path; import sys; Path(sys.argv[1]).write_text('ran')", str(later)]},
    ])
    bootstrap = "import _thread,runpy,sys,threading,time\nfrom pathlib import Path\nready=Path(sys.argv[1])\ndef interrupt():\n end=time.monotonic()+20\n while not ready.exists() and time.monotonic()<end: time.sleep(.01)\n if ready.exists(): _thread.interrupt_main()\nthreading.Thread(target=interrupt,daemon=True).start()\nsys.argv=['scripts.verify',*sys.argv[2:]]\nrunpy.run_module('scripts.verify',run_name='__main__')\n"
    root = Path(__file__).resolve().parents[2]
    output = tmp_path / "receipts"
    result = subprocess.run([sys.executable, "-c", bootstrap, str(identity), "--plan", str(plan), "--output", str(output), "--run-id", "interrupted"], cwd=repository, env={**os.environ, "PYTHONPATH": str(root)}, capture_output=True, text=True, encoding="utf-8", timeout=40)
    assert result.returncode != 0
    receipt_path = output / "interrupted/summary.json"
    assert receipt_path.is_file(), result.stderr
    receipt = json.loads(receipt_path.read_text(encoding="utf-8"))
    assert result.returncode == 130 and receipt["status"] == "BLOCKED"
    assert receipt["attempted_checks"] == receipt["not_run_checks"] == 1 and receipt["planned_checks"] == 2
    check = receipt["checks"][0]
    assert check["state"] == "interrupted" and check["process_tree_stop_confirmed"] is True
    assert (output / "interrupted/checksums.sha256").is_file() and not later.exists()
    assert not any(process_is_running(pid) for pid in json.loads(identity.read_text()).values())


def test_public_verification_accepts_existing_gitignored_output_root(tmp_path, repository):
    (repository / ".gitignore").write_text(".verification/\n", encoding="utf-8")
    plan = write_plan(tmp_path / "plan.json", [{"id": "ignored-output", "argv": [sys.executable, "-c", "print('completed')"]}])
    result = verification_command(repository, plan, repository / ".verification")
    assert result.returncode == 0, result.stderr
    receipt = json.loads((repository / ".verification/target/summary.json").read_text(encoding="utf-8"))
    assert receipt["inputs"]["source_pin_before"] == receipt["inputs"]["source_pin_after"]


def test_public_verification_full_refuses_enabled_real_integration_flags(tmp_path, repository, monkeypatch):
    monkeypatch.setenv("CODE_AGENT_RUN_FIXTURE_UNSUPPORTED_EXTERNAL", "1")
    plan = tmp_path / "full.json"
    plan.write_text(json.dumps({"schema_version": 1, "scope_kind": "full"}), encoding="utf-8")
    result = verification_command(repository, plan, tmp_path / "receipts")
    assert result.returncode == 2 and "real integration flags" in json.loads(result.stdout)["error"]
    assert not (tmp_path / "receipts/target").exists()
