"""Lifecycle and preflight contracts for the PowerShell receipt runners."""

from __future__ import annotations

import os
import shutil
import subprocess
import uuid
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parents[2]
SCRIPT_NAMES = (
    "run_h5_smoke.ps1",
    "run-terminalbench-official-receipt.ps1",
    "run-tau2-official-receipt.ps1",
)


def _script_text(name: str) -> str:
    return (REPO_ROOT / "scripts" / name).read_text(encoding="utf-8")


@pytest.mark.parametrize(
    "script_name",
    ("run-terminalbench-official-receipt.ps1", "run-tau2-official-receipt.ps1"),
)
def test_receipt_run_id_is_validated_before_path_construction(script_name: str) -> None:
    script = _script_text(script_name)

    validation = script.index("$RunId -notmatch $runIdPattern")
    path_construction = script.index("$runRoot = Join-Path")

    assert "^[A-Za-z0-9][A-Za-z0-9._-]{0,63}\\z" in script
    assert validation < path_construction


@pytest.mark.parametrize("script_name", SCRIPT_NAMES)
def test_receipt_credentials_are_checked_before_output_directory_creation(script_name: str) -> None:
    script = _script_text(script_name)

    credential_check = script.index("IsNullOrWhiteSpace($env:LOCAL_LLM_API_KEY)")
    directory_creation = script.index("New-Item -ItemType Directory")

    assert credential_check < directory_creation


@pytest.mark.parametrize("script_name", SCRIPT_NAMES)
def test_receipt_runners_restore_process_environment(script_name: str) -> None:
    script = _script_text(script_name)

    assert "$trackedEnvironmentNames" in script
    assert "$previousEnvironment" in script
    assert "function Restore-ReceiptEnvironment" in script
    assert script.count("Restore-ReceiptEnvironment") >= 2

    if script_name == "run_h5_smoke.ps1":
        assert "Restore-ReceiptEnvironment\n    Pop-Location" in script
    else:
        assert "finally {\n    Restore-ReceiptEnvironment\n}" in script


def _pwsh() -> str | None:
    return shutil.which("pwsh") or shutil.which("powershell")


def _without_credential() -> dict[str, str]:
    env = {key: value for key, value in os.environ.items() if key.upper() != "LOCAL_LLM_API_KEY"}
    return env


@pytest.mark.parametrize("script_name", SCRIPT_NAMES)
def test_missing_credential_does_not_create_receipt_output(
    script_name: str, tmp_path: Path
) -> None:
    """Run each preflight in a temporary script root to prove it is side-effect free."""

    powershell = _pwsh()
    if powershell is None:
        pytest.skip("PowerShell is required for runner lifecycle tests")

    script_root = tmp_path / "scripts"
    script_root.mkdir()
    script_path = script_root / script_name
    shutil.copy2(REPO_ROOT / "scripts" / script_name, script_path)
    python_command = shutil.which("python") or "python"
    run_id = "preflight-" + uuid.uuid4().hex[:12]

    if script_name == "run_h5_smoke.ps1":
        output_path = tmp_path / "h5-output"
        arguments = [
            "-OutDir",
            str(output_path),
            "-Python",
            python_command,
        ]
    elif script_name == "run-tau2-official-receipt.ps1":
        output_path = tmp_path / "eval_results" / "tau2official" / run_id
        checkout = tmp_path / "tau2-checkout"
        checkout.mkdir()
        arguments = [
            "-RunId",
            run_id,
            "-CheckoutPath",
            str(checkout),
            "-Python",
            python_command,
        ]
    else:
        output_path = tmp_path / "eval_results" / "terminalbenchofficial" / run_id
        arguments = ["-RunId", run_id, "-Python", python_command]

    completed = subprocess.run(
        [
            powershell,
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(script_path),
            *arguments,
        ],
        cwd=tmp_path,
        env=_without_credential(),
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )

    assert completed.returncode != 0
    assert not output_path.exists()


@pytest.mark.parametrize(
    ("script_name", "run_id"),
    [
        ("run-terminalbench-official-receipt.ps1", r"..\escape"),
        ("run-tau2-official-receipt.ps1", r"..\escape"),
        ("run-terminalbench-official-receipt.ps1", r"nested/escape"),
        ("run-tau2-official-receipt.ps1", r"nested/escape"),
    ],
)
def test_invalid_run_id_fails_without_creating_a_path(
    script_name: str, run_id: str, tmp_path: Path
) -> None:
    powershell = _pwsh()
    if powershell is None:
        pytest.skip("PowerShell is required for runner lifecycle tests")

    script_root = tmp_path / "scripts"
    script_root.mkdir()
    script_path = script_root / script_name
    shutil.copy2(REPO_ROOT / "scripts" / script_name, script_path)
    checkout = tmp_path / "tau2-checkout"
    checkout.mkdir()
    env = _without_credential()
    env["LOCAL_LLM_API_KEY"] = "lifecycle-test-sentinel"

    arguments = ["-RunId", run_id, "-Python", shutil.which("python") or "python"]
    if script_name == "run-tau2-official-receipt.ps1":
        arguments.extend(["-CheckoutPath", str(checkout)])

    completed = subprocess.run(
        [
            powershell,
            "-NoProfile",
            "-ExecutionPolicy",
            "Bypass",
            "-File",
            str(script_path),
            *arguments,
        ],
        cwd=tmp_path,
        env=env,
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )

    assert completed.returncode != 0
    assert not (tmp_path / "eval_results").exists()
