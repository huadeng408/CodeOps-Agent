from __future__ import annotations

import os
import shutil
import socket
import subprocess
import time
import uuid
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parents[2]
REDACTION_HELPER = REPO_ROOT / "scripts" / "lib" / "credential-redaction.ps1"


def _pwsh() -> str | None:
    return shutil.which("pwsh") or shutil.which("powershell")


def _write_fake_python(
    tmp_path: Path,
    *,
    windows_lines: tuple[str, ...],
    posix_lines: tuple[str, ...],
) -> Path:
    """Create an executable child fixture for both PowerShell platforms.

    ``pwsh`` on Linux cannot execute a Windows ``.cmd`` file.  Keep the child
    behaviour identical while using a native shell script on POSIX runners.
    """
    if os.name == "nt":
        path = tmp_path / "fake-python.cmd"
        path.write_text(
            "@echo off\n" + "\n".join(windows_lines) + "\nexit /b 7\n",
            encoding="utf-8",
        )
    else:
        path = tmp_path / "fake-python.sh"
        path.write_text(
            "#!/bin/sh\n" + "\n".join(posix_lines) + "\nexit 7\n",
            encoding="utf-8",
        )
        path.chmod(0o755)
    return path


def test_redacted_native_command_filters_both_output_streams_and_preserves_exit_code(
    tmp_path: Path,
) -> None:
    powershell = _pwsh()
    if powershell is None:
        pytest.skip("PowerShell is required for receipt redaction tests")

    child = tmp_path / "emit_secret.py"
    child.write_text(
        "import os, sys\n"
        "value = os.environ['REDACTION_TEST_SECRET']\n"
        "print('stdout=' + value)\n"
        "print('stderr=Bearer ' + value, file=sys.stderr)\n"
        "print('generic_bearer=Bearer generic.test-token_123')\n"
        "print('generic_key=' + 's' + 'k-' + 'unit-test-token_1234567890')\n"
        "sys.exit(7)\n",
        encoding="utf-8",
    )
    secret = "redaction-test-sentinel-value"
    env = os.environ.copy()
    env["REDACTION_TEST_SECRET"] = secret
    command = (
        f". '{REDACTION_HELPER}'; "
        "$exitCode = 99; "
        f"Invoke-RedactedNativeCommand -FilePath '{shutil.which('python') or 'python'}' "
        f"-ArgumentList @('{child}') -Secrets @($env:REDACTION_TEST_SECRET) "
        "-ExitCode ([ref]$exitCode); exit $exitCode"
    )

    completed = subprocess.run(
        [powershell, "-NoProfile", "-Command", command],
        cwd=REPO_ROOT,
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=30,
        check=False,
    )

    output = completed.stdout + completed.stderr
    assert completed.returncode == 7
    assert secret not in output
    assert "generic.test-token_123" not in output
    assert "unit-test-token_1234567890" not in output
    assert output.count("<redacted>") >= 4


def test_h5_receipt_runner_redacts_child_output(tmp_path: Path) -> None:
    powershell = _pwsh()
    if powershell is None:
        pytest.skip("PowerShell is required for receipt redaction tests")

    fake_python = _write_fake_python(
        tmp_path,
        windows_lines=(
            "echo stdout=%LOCAL_LLM_API_KEY%",
            "echo stderr=Bearer %LOCAL_LLM_API_KEY% 1>&2",
        ),
        posix_lines=(
            'echo "stdout=$LOCAL_LLM_API_KEY"',
            'echo "stderr=Bearer $LOCAL_LLM_API_KEY" 1>&2',
        ),
    )
    output_dir = tmp_path / "receipt"
    secret = "h5-redaction-test-sentinel"
    env = os.environ.copy()
    env["LOCAL_LLM_API_KEY"] = secret

    completed = subprocess.run(
        [
            powershell,
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(REPO_ROOT / "scripts" / "run_h5_smoke.ps1"),
            "-OutDir",
            str(output_dir),
            "-Python",
            str(fake_python),
        ],
        cwd=REPO_ROOT,
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=30,
        check=False,
    )

    output = completed.stdout + completed.stderr
    assert completed.returncode == 7
    assert secret not in output
    assert output.count("<redacted>") >= 2
    assert (output_dir / "run.rc").read_text(encoding="utf-8-sig").strip() == "7"


def test_o3_receipt_uses_custom_loopback_port_and_redacts_children(
    tmp_path: Path,
) -> None:
    powershell = _pwsh()
    go = shutil.which("go")
    if powershell is None or go is None:
        pytest.skip("PowerShell and Go are required for the O3 receipt test")

    repository = tmp_path / "repository"
    script_root = repository / "scripts"
    helper_root = script_root / "lib"
    helper_root.mkdir(parents=True)
    shutil.copy2(REPO_ROOT / "scripts" / "run-o3-receipt.ps1", script_root)
    shutil.copy2(REDACTION_HELPER, helper_root / REDACTION_HELPER.name)
    endpoint_helper = REPO_ROOT / "scripts" / "lib" / "receipt-endpoints.ps1"
    if endpoint_helper.exists():
        shutil.copy2(endpoint_helper, helper_root / endpoint_helper.name)

    (repository / "go.mod").write_text(
        "module example.com/o3-receipt-test\n\ngo 1.22\n",
        encoding="utf-8",
    )
    server_root = repository / "cmd" / "server"
    server_root.mkdir(parents=True)
    (server_root / "main.go").write_text(
        "package main\n"
        "import (\"fmt\"; \"net/http\"; \"os\")\n"
        "func main() {\n"
        "  port := os.Getenv(\"CODE_AGENT_SERVER_PORT\")\n"
        "  if port == \"\" { port = \"8081\" }\n"
        "  fmt.Println(\"key=\" + os.Getenv(\"LOCAL_LLM_API_KEY\"))\n"
        "  fmt.Fprintln(os.Stderr, \"internal=\" + os.Getenv(\"CODE_AGENT_RAG_INTERNAL_SECRET\"))\n"
        "  http.HandleFunc(\"/healthz\", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })\n"
        "  if err := http.ListenAndServe(\"127.0.0.1:\" + port, nil); err != nil { panic(err) }\n"
        "}\n",
        encoding="utf-8",
    )
    fake_python = _write_fake_python(
        tmp_path,
        windows_lines=(
            "echo python=%LOCAL_LLM_API_KEY%",
            "echo python-internal=%CODE_AGENT_RAG_INTERNAL_SECRET% 1>&2",
        ),
        posix_lines=(
            'echo "python=$LOCAL_LLM_API_KEY"',
            'echo "python-internal=$CODE_AGENT_RAG_INTERNAL_SECRET" 1>&2',
        ),
    )
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]

    secret = "o3-redaction-test-sentinel"
    env = os.environ.copy()
    env["LOCAL_LLM_API_KEY"] = secret
    env["TEMP"] = str(tmp_path)
    env["TMP"] = str(tmp_path)
    completed = subprocess.run(
        [
            powershell,
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(script_root / "run-o3-receipt.ps1"),
            "-OutputDir",
            str(tmp_path / "output"),
            "-ServerUrl",
            f"http://127.0.0.1:{port}",
            "-Python",
            str(fake_python),
            "-StartupTimeoutSeconds",
            "20",
        ],
        cwd=repository,
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=60,
        check=False,
    )

    visible_output = completed.stdout + completed.stderr
    server_output = "".join(
        path.read_text(encoding="utf-8", errors="replace")
        for path in tmp_path.glob("codeagent-o3-server.*.log")
    )
    assert completed.returncode == 7, visible_output + server_output
    assert secret not in visible_output + server_output
    assert "internal=<redacted>" in server_output
    assert visible_output.count("<redacted>") >= 2


def test_o3_receipt_rejects_non_loopback_server_url(tmp_path: Path) -> None:
    powershell = _pwsh()
    if powershell is None:
        pytest.skip("PowerShell is required for the O3 receipt test")

    env = {key: value for key, value in os.environ.items() if key != "LOCAL_LLM_API_KEY"}
    completed = subprocess.run(
        [
            powershell,
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(REPO_ROOT / "scripts" / "run-o3-receipt.ps1"),
            "-ServerUrl",
            "http://example.com:8443",
        ],
        cwd=REPO_ROOT,
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=30,
        check=False,
    )

    assert completed.returncode != 0
    assert "loopback" in (completed.stdout + completed.stderr).lower()


@pytest.mark.parametrize(
    ("script_name", "result_folder"),
    [
        ("run-tau2-official-receipt.ps1", "tau2official"),
        ("run-terminalbench-official-receipt.ps1", "terminalbenchofficial"),
    ],
)
def test_background_receipt_runner_redacts_log_files(
    script_name: str,
    result_folder: str,
    tmp_path: Path,
) -> None:
    powershell = _pwsh()
    if powershell is None:
        pytest.skip("PowerShell is required for receipt redaction tests")

    script_root = tmp_path / "scripts"
    helper_root = script_root / "lib"
    helper_root.mkdir(parents=True)
    shutil.copy2(REPO_ROOT / "scripts" / script_name, script_root / script_name)
    shutil.copy2(REDACTION_HELPER, helper_root / REDACTION_HELPER.name)
    fake_python = _write_fake_python(
        tmp_path,
        windows_lines=(
            "echo stdout=%LOCAL_LLM_API_KEY%%TAU2_RECEIPT_API_KEY%",
            "echo stderr=Bearer %LOCAL_LLM_API_KEY%%TAU2_RECEIPT_API_KEY% 1>&2",
        ),
        posix_lines=(
            'echo "stdout=$LOCAL_LLM_API_KEY$TAU2_RECEIPT_API_KEY"',
            'echo "stderr=Bearer $LOCAL_LLM_API_KEY$TAU2_RECEIPT_API_KEY" 1>&2',
        ),
    )
    run_id = "redaction-" + uuid.uuid4().hex[:12]
    output_dir = tmp_path / "eval_results" / result_folder / run_id
    env = os.environ.copy()
    secret = "background-redaction-test-sentinel"
    env["LOCAL_LLM_API_KEY"] = secret
    arguments = [
        powershell,
        "-NoProfile",
        "-ExecutionPolicy",
        "Bypass",
        "-File",
        str(script_root / script_name),
        "-RunId",
        run_id,
        "-Python",
        str(fake_python),
    ]
    if script_name.startswith("run-tau2"):
        checkout = tmp_path / "tau2-checkout"
        checkout.mkdir()
        arguments.extend(["-CheckoutPath", str(checkout)])

    launched = subprocess.run(
        arguments,
        cwd=tmp_path,
        env=env,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=30,
        check=False,
    )
    assert launched.returncode == 0, launched.stdout + launched.stderr

    exit_path = output_dir / "exit-code.txt"
    deadline = time.monotonic() + 15
    while not exit_path.exists() and time.monotonic() < deadline:
        time.sleep(0.1)
    assert exit_path.exists(), "background finalizer did not record an exit code"

    output = "".join(
        (output_dir / name).read_text(encoding="utf-8", errors="replace")
        for name in ("stdout.log", "stderr.log")
    )
    assert exit_path.read_text(encoding="ascii").strip() == "7"
    assert secret not in output
    assert output.count("<redacted>") >= 2
