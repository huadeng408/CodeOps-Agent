"""Public CLI receipts for Go JSON outcome streams; no Go compiler is used."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

import pytest

pytestmark = pytest.mark.skipif(os.name != "nt", reason="verification requires Windows Job ownership")
ROOT = Path(__file__).resolve().parents[2]


@pytest.fixture(scope="session")
def fake_go(tmp_path_factory):
    directory = tmp_path_factory.mktemp("fake-go")
    executable = directory / "go.exe"
    shutil.copyfile(sys._base_executable, executable)
    for library in Path(sys.base_prefix).glob("python*.dll"):
        shutil.copyfile(library, directory / library.name)
    (directory / "go._pth").write_text(
        str(Path(sys.base_prefix) / "Lib") + "\n" + str(Path(sys.base_prefix) / "DLLs") + "\n",
        encoding="utf-8",
    )
    return executable


@pytest.fixture
def repository(tmp_path):
    root = tmp_path / "repository"
    root.mkdir()
    (root / "input.txt").write_text("fixed fixture input\n", encoding="utf-8")
    for arguments in (["init", "--quiet"], ["add", "."], ["-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixture"]):
        subprocess.run(["git", *arguments], cwd=root, check=True, capture_output=True)
    return root


def go_event(action, test=""):
    return {"Action": action, "Package": "fixture", **({"Test": test} if test else {})}


def run_verification(tmp_path, repository, fake_go, rows, *, exit_code=1, arguments=(), timeout=10, program_suffix="", environment=None):
    (repository / "test").write_text(
        "import json,sys,time\n"
        + "rows=" + repr(rows) + "\n"
        + "for row in rows: print(json.dumps(row,ensure_ascii=False),flush=True)\n"
        + program_suffix
        + "sys.exit(" + str(exit_code) + ")\n",
        encoding="utf-8",
    )
    plan = tmp_path / "plan.json"
    plan.write_text(json.dumps({"schema_version": 1, "scope_kind": "targeted", "scope_name": "literal Go outcome stream; engineering fixture only", "checks": [{"id": "go-test", "argv": [str(fake_go), "test", "-json", *arguments], "timeout_seconds": timeout}]}), encoding="utf-8")
    output = tmp_path / "receipts"
    env = {**os.environ, "PYTHONPATH": str(ROOT) + os.pathsep + os.environ.get("PYTHONPATH", ""), "PYTHONIOENCODING": "utf-8", "GOENV": "off", "GOFLAGS": "", **(environment or {})}
    done = subprocess.run([sys.executable, "-X", "utf8", "-B", "-m", "scripts.verify", "--plan", str(plan), "--output", str(output), "--run-id", "fixture"], cwd=repository, env=env, capture_output=True, text=True, encoding="utf-8", timeout=30)
    artifact = output / "fixture"
    receipt = json.loads((artifact / "summary.json").read_text(encoding="utf-8"))
    for line in (artifact / "checksums.sha256").read_text(encoding="utf-8").splitlines():
        digest, relative = line.split("  ", 1)
        assert hashlib.sha256((artifact / relative).read_bytes()).hexdigest() == digest
    assert receipt["checks"][0]["executed_this_run"] is True
    assert receipt["checks"][0]["process_tree_stop_confirmed"] is True
    return done, receipt


def test_public_cli_keeps_complete_go_failure_as_failed_with_full_denominator(tmp_path, repository, fake_go):
    rows = [go_event("start"), go_event("run", "TestFailure"), go_event("fail", "TestFailure"),
            {**go_event("output"), "Output": "FAIL\n"}, go_event("fail")]
    done, receipt = run_verification(tmp_path, repository, fake_go, rows)
    assert done.returncode == 1, done.stdout + done.stderr
    assert receipt["status"] == "FAIL" and receipt["failed_checks"] == 1
    check = receipt["checks"][0]
    assert check["state"] == "failed" and check["exit_code"] == 1
    assert check["test_denominator"]["complete"] is True
    assert check["test_denominator"]["test_events"] == {"pass": 0, "fail": 1, "skip": 0}
    assert check["test_denominator"]["package_events"] == {"pass": 0, "fail": 1, "skip": 0}
    assert check["test_denominator"]["total"] == 1


def test_public_cli_complete_go_failure_retains_pass_skip_and_failure(tmp_path, repository, fake_go):
    rows = [go_event("start"), go_event("run", "TestPass"), go_event("pass", "TestPass"),
            go_event("run", "TestSkip"), go_event("skip", "TestSkip"),
            go_event("run", "TestFailure"), go_event("fail", "TestFailure"),
            {**go_event("output"), "Output": "FAIL\n"}, go_event("fail")]
    done, receipt = run_verification(tmp_path, repository, fake_go, rows, environment={"GOFLAGS": "-tags=fixture"})
    assert done.returncode == 1 and receipt["status"] == "FAIL"
    denominator = receipt["checks"][0]["test_denominator"]
    assert denominator["complete"] is True and denominator["total"] == 3
    assert denominator["test_events"] == {"pass": 1, "fail": 1, "skip": 1}


def test_public_cli_successful_go_json_remains_complete(tmp_path, repository, fake_go):
    rows = [go_event("start"), go_event("run", "TestPass"), go_event("pass", "TestPass"), go_event("pass")]
    done, receipt = run_verification(tmp_path, repository, fake_go, rows, exit_code=0)
    assert done.returncode == 0 and receipt["status"] == "PASS"
    assert receipt["checks"][0]["test_denominator"]["complete"] is True
    assert receipt["checks"][0]["test_denominator"]["total"] == 1


def test_public_cli_successful_repeated_go_tests_keep_complete_terminal_event_denominator(tmp_path, repository, fake_go):
    rows = [go_event("start"), go_event("run", "TestPass"), go_event("pass", "TestPass"),
            go_event("run", "TestPass"), go_event("pass", "TestPass"), go_event("pass")]
    done, receipt = run_verification(tmp_path, repository, fake_go, rows, exit_code=0, arguments=["-count=2"])
    assert done.returncode == 0, done.stdout + done.stderr
    assert receipt["status"] == "PASS" and receipt["passed_checks"] == 1
    denominator = receipt["checks"][0]["test_denominator"]
    assert denominator["complete"] is True and denominator["total"] == 2
    assert denominator["test_events"] == {"pass": 2, "fail": 0, "skip": 0}
    assert denominator["package_events"] == {"pass": 1, "fail": 0, "skip": 0}


def test_public_cli_redacts_escaped_go_output_without_breaking_json(tmp_path, repository, fake_go):
    nonce = hashlib.sha256(os.urandom(32)).hexdigest()
    rows = [go_event("start"), go_event("run", "TestPass"),
            {**go_event("output", "TestPass"), "Output": 'access_token="' + nonce + '"\n'},
            go_event("pass", "TestPass"), go_event("pass")]
    done, receipt = run_verification(tmp_path, repository, fake_go, rows, exit_code=0)
    check = receipt["checks"][0]
    assert check["exit_code"] == 0
    log = (tmp_path / "receipts/fixture" / check["stdout_log"]).read_text(encoding="utf-8")
    if nonce in log:
        pytest.fail("persisted Go stdout leaked the synthetic credential", pytrace=False)
    try:
        decoded = [json.loads(line) for line in log.split("\n") if line.strip()]
    except json.JSONDecodeError:
        pytest.fail("persisted Go stdout is not valid NDJSON", pytrace=False)
    assert len(decoded) == 5 and any("<redacted>" in row.get("Output", "") for row in decoded)
    assert done.returncode == 0 and receipt["status"] == "PASS"
    assert check["state"] == "passed" and check["test_denominator"]["complete"] is True
    assert check["test_denominator"]["test_events"] == {"pass": 1, "fail": 0, "skip": 0}
    assert check["test_denominator"]["total"] == 1


def test_public_cli_keeps_unicode_nel_inside_go_json_output(tmp_path, repository, fake_go):
    rows = [go_event("start"), go_event("run", "TestPass"),
            {**go_event("output", "TestPass"), "Output": "first\u0085second\n"},
            go_event("pass", "TestPass"), go_event("pass")]
    done, receipt = run_verification(tmp_path, repository, fake_go, rows, exit_code=0)
    check = receipt["checks"][0]
    assert check["exit_code"] == 0
    log = (tmp_path / "receipts/fixture" / check["stdout_log"]).read_text(encoding="utf-8")
    decoded = [json.loads(line) for line in log.split("\n") if line.strip()]
    assert [row["Output"] for row in decoded if row["Action"] == "output"] == ["first\u0085second\n"]
    assert done.returncode == 0 and receipt["status"] == "PASS"
    assert check["state"] == "passed" and check["test_denominator"]["complete"] is True
    assert check["test_denominator"]["test_events"] == {"pass": 1, "fail": 0, "skip": 0}
    assert check["test_denominator"]["total"] == 1


@pytest.mark.parametrize("case", [
    "missing_test_terminal", "missing_package_terminal", "duplicate_terminal", "duplicate_start_and_terminal",
    "build_failure", "another_package_build_failure", "truncated_json", "failfast_argv", "failfast_environment",
    "double_dash_failfast_argv", "double_dash_test_failfast_argv", "double_dash_failfast_true_argv",
    "double_dash_failfast_environment", "double_dash_test_failfast_environment", "double_dash_failfast_true_environment",
    "missing_normal_failure_marker", "synthesized_package_failure", "panic_output", "fatal_error_output",
    "goexit_output", "timeout_diagnostic_output",
    "unknown_goenv", "unexpected_exit", "timeout", "output_overflow",
])
def test_public_cli_unconfirmed_go_denominator_stays_blocked(tmp_path, repository, fake_go, case):
    rows = [go_event("start"), go_event("run", "TestFailure"), go_event("fail", "TestFailure"),
            {**go_event("output"), "Output": "FAIL\n"}, go_event("fail")]
    options = {}
    if case == "missing_test_terminal":
        rows.pop(2)
    elif case == "missing_package_terminal":
        rows.pop()
    elif case == "duplicate_terminal":
        rows.insert(3, go_event("fail", "TestFailure"))
    elif case == "duplicate_start_and_terminal":
        rows[2:2] = [go_event("run", "TestFailure"), go_event("fail", "TestFailure")]
    elif case == "build_failure":
        rows = [go_event("start"), go_event("fail")]
    elif case == "another_package_build_failure":
        rows += [{"Action": "start", "Package": "broken"}, {"Action": "fail", "Package": "broken"}]
    elif case == "truncated_json":
        options["program_suffix"] = "print('{\"Action\":',flush=True)\n"
    elif case == "failfast_argv":
        options["arguments"] = ["-failfast"]
    elif case == "failfast_environment":
        options["environment"] = {"GOFLAGS": "-failfast"}
    elif case in {"double_dash_failfast_argv", "double_dash_test_failfast_argv", "double_dash_failfast_true_argv"}:
        options["arguments"] = [{"double_dash_failfast_argv": "--failfast", "double_dash_test_failfast_argv": "--test.failfast", "double_dash_failfast_true_argv": "--failfast=true"}[case]]
    elif case in {"double_dash_failfast_environment", "double_dash_test_failfast_environment", "double_dash_failfast_true_environment"}:
        options["environment"] = {"GOFLAGS": {"double_dash_failfast_environment": "--failfast", "double_dash_test_failfast_environment": "--test.failfast", "double_dash_failfast_true_environment": "--failfast=true"}[case]}
    elif case == "missing_normal_failure_marker":
        rows.pop(3)
    elif case == "synthesized_package_failure":
        rows[3]["Output"] = "FAIL\tfixture\t0.001s\n"
    elif case in {"panic_output", "fatal_error_output", "goexit_output", "timeout_diagnostic_output"}:
        diagnostic = {"panic_output": "panic: retained failure\n", "fatal_error_output": "fatal error: all goroutines are asleep - deadlock!\n", "goexit_output": "test executed panic(nil) or runtime.Goexit\n", "timeout_diagnostic_output": "test timed out after 1s\n"}[case]
        rows.insert(2, {**go_event("output", "TestFailure"), "Output": diagnostic})
    elif case == "unknown_goenv":
        options["environment"] = {"GOENV": ""}
    elif case == "unexpected_exit":
        options["exit_code"] = 2
    elif case == "timeout":
        options.update(timeout=0.5, program_suffix="time.sleep(5)\n")
    elif case == "output_overflow":
        options["program_suffix"] = "sys.stdout.write('x' * (20 * 1024 * 1024));sys.stdout.flush()\n"
    done, receipt = run_verification(tmp_path, repository, fake_go, rows, **options)
    assert done.returncode == 3, done.stdout + done.stderr
    assert receipt["status"] == "BLOCKED" and receipt["failed_checks"] == 0
    check = receipt["checks"][0]
    assert check["state"] == "infra_error" and check["error_code"] == "test_denominator_unconfirmed"
    assert check["test_denominator"]["complete"] is False and check["test_denominator"]["total"] is None
