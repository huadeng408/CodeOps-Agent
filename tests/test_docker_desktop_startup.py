from __future__ import annotations

import json
import os
import shutil
import subprocess
from pathlib import Path

import pytest


ROOT = Path(__file__).parents[1]
RUNTIME = ROOT / "scripts" / "rag-agent-e2e-runtime.ps1"


def run_startup(tmp_path: Path, checks: str, *, ready: bool = False) -> subprocess.CompletedProcess[str]:
    powershell = shutil.which("pwsh") or shutil.which("powershell.exe")
    if os.name != "nt" or not powershell:
        pytest.skip("Windows PowerShell startup contract")
    desktop = tmp_path / "programs" / "Docker" / "Docker" / "Docker Desktop.exe"
    desktop.parent.mkdir(parents=True)
    desktop.touch()
    runtime_dir = tmp_path / "local" / "Docker" / "run"
    runtime_dir.mkdir(parents=True)
    (runtime_dir / "dockerInference").touch()
    (runtime_dir / "userAnalyticsOtlpHttp.sock").touch()
    script = tmp_path / "startup.ps1"
    script.write_text(
        "\n".join(
            [
                "$ErrorActionPreference = 'Stop'",
                f". '{RUNTIME.as_posix()}'",
                "$script:started = $false",
                "function Get-Process { param([string[]]$Name) }",
                f"function Resolve-DockerCli {{ return '{Path(powershell).as_posix()}' }}",
                "function Use-DockerDesktopLinuxContext { param($DockerCli) return $null }",
                "function Invoke-DockerProbe { param($DockerCli,$ProbeArguments,$ProbeTimeoutMilliseconds)",
                f"    $code = if ($script:started -or ${str(ready).lower()}) {{ 0 }} else {{ 1 }}",
                "    return [pscustomobject]@{ TimedOut=$false; ExitCode=$code }",
                "}",
                "function Start-Process { param($FilePath,$WindowStyle)",
                "    if (Test-Path -LiteralPath (Join-Path $env:LOCALAPPDATA 'Docker/run/dockerInference')) {",
                "        throw 'startup attempted with a stale IPC endpoint'",
                "    }",
                "    if (Test-Path -LiteralPath (Join-Path $env:LOCALAPPDATA 'docker-secrets-engine/engine.sock')) {",
                "        throw 'startup attempted with a stale Secrets Engine endpoint'",
                "    }",
                "    $script:started = $true",
                "}",
                checks,
            ]
        ),
        encoding="utf-8",
    )
    environment = {key: value for key, value in os.environ.items() if key.upper() not in {"PROGRAMFILES", "LOCALAPPDATA"}}
    environment["ProgramFiles"] = str(tmp_path / "programs")
    environment["LOCALAPPDATA"] = str(tmp_path / "local")
    return subprocess.run(
        [powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(script)],
        cwd=ROOT,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        env=environment,
        timeout=15,
        check=False,
    )


def test_cold_start_preserves_stale_ipc_in_an_archive(tmp_path: Path) -> None:
    result = run_startup(
        tmp_path,
        """
Wait-DockerDaemonReady -TimeoutSeconds 5
$archives = @(Get-ChildItem -LiteralPath (Join-Path $env:LOCALAPPDATA 'Docker') -Directory -Filter 'run.archive-*')
if ($archives.Count -ne 1) { throw 'expected exactly one recoverable IPC archive' }
foreach ($name in @('dockerInference','userAnalyticsOtlpHttp.sock')) {
    if (-not (Test-Path -LiteralPath (Join-Path $archives[0].FullName $name))) {
        throw 'an archived endpoint was lost'
    }
}
""",
    )
    assert result.returncode == 0, result.stderr
    assert "ready: Docker daemon" in result.stdout


def test_healthy_engine_is_reused_without_launching_or_archiving(tmp_path: Path) -> None:
    result = run_startup(
        tmp_path,
        """
Wait-DockerDaemonReady -TimeoutSeconds 5
if ($script:started) { throw 'healthy engine caused a redundant Desktop launch' }
if (-not (Test-Path -LiteralPath (Join-Path $env:LOCALAPPDATA 'Docker/run/dockerInference'))) {
    throw 'healthy engine runtime was moved'
}
""",
        ready=True,
    )
    assert result.returncode == 0, result.stderr


def test_cold_start_recovers_secrets_engine_ipc_too(tmp_path: Path) -> None:
    result = run_startup(
        tmp_path,
        """
$secretsDirectory = Join-Path $env:LOCALAPPDATA 'docker-secrets-engine'
New-Item -ItemType Directory -Path $secretsDirectory | Out-Null
New-Item -ItemType File -Path (Join-Path $secretsDirectory 'engine.sock') | Out-Null
Wait-DockerDaemonReady -TimeoutSeconds 5
$archives = @(Get-ChildItem -LiteralPath $env:LOCALAPPDATA -Directory -Filter 'docker-secrets-engine.archive-*')
if ($archives.Count -ne 1 -or -not (Test-Path -LiteralPath (Join-Path $archives[0].FullName 'engine.sock'))) {
    throw 'Secrets Engine IPC was not preserved'
}
""",
    )
    assert result.returncode == 0, result.stderr


@pytest.mark.parametrize(
    "setup,expected_error",
    [
        (
            "Set-Content -LiteralPath (Join-Path $env:LOCALAPPDATA 'Docker/run/unrecognized.json') -Value 'keep'",
            "unexpected entry",
        ),
        (
            "Set-Content -LiteralPath (Join-Path $env:LOCALAPPDATA 'Docker/run/dockerInference') -Value 'keep'",
            "non-socket Docker runtime content",
        ),
        (
            """
$run = Join-Path $env:LOCALAPPDATA 'Docker/run'
Move-Item -LiteralPath $run -Destination (Join-Path $env:LOCALAPPDATA 'Docker/run-saved')
New-Item -ItemType Junction -Path $run -Value (Join-Path $env:LOCALAPPDATA 'Docker/run-saved') | Out-Null
""",
            "redirected Docker runtime directory",
        ),
        (
            """
$savedLocal = Join-Path (Split-Path -Parent $env:LOCALAPPDATA) 'local-saved'
Move-Item -LiteralPath $env:LOCALAPPDATA -Destination $savedLocal
New-Item -ItemType Junction -Path $env:LOCALAPPDATA -Value $savedLocal | Out-Null
""",
            "redirected Docker runtime directory",
        ),
    ],
)
def test_cold_start_refuses_unexpected_runtime_content(
    tmp_path: Path, setup: str, expected_error: str
) -> None:
    result = run_startup(
        tmp_path,
        setup
        + """
$caught = $null
try { Wait-DockerDaemonReady -TimeoutSeconds 5 } catch { $caught = $_.Exception.Message }
if ($null -eq $caught) { throw 'unsafe runtime was accepted' }
if ($script:started) { throw 'Desktop was launched with unsafe runtime' }
$archives = @(Get-ChildItem -LiteralPath (Join-Path $env:LOCALAPPDATA 'Docker') -Directory -Filter 'run.archive-*')
if ($archives.Count -ne 0) { throw 'unsafe runtime was moved' }
Write-Output $caught
""",
    )
    assert result.returncode == 0, result.stderr
    assert expected_error in result.stdout


def test_backend_starting_does_not_launch_another_desktop(tmp_path: Path) -> None:
    result = run_startup(
        tmp_path,
        """
function Get-Process { param([string[]]$Name)
    if ($Name -contains 'com.docker.backend') { return [pscustomobject]@{Name='com.docker.backend'} }
}
$script:probes = 0
function Invoke-DockerProbe { param($DockerCli,$ProbeArguments,$ProbeTimeoutMilliseconds)
    $script:probes++
    $code = if ($script:probes -gt 1) { 0 } else { 1 }
    return [pscustomobject]@{ TimedOut=$false; ExitCode=$code }
}
Wait-DockerDaemonReady -TimeoutSeconds 5
if ($script:started) { throw 'backend in progress caused a duplicate launch' }
if (-not (Test-Path -LiteralPath (Join-Path $env:LOCALAPPDATA 'Docker/run/dockerInference'))) {
    throw 'live backend runtime was moved'
}
""",
    )
    assert result.returncode == 0, result.stderr


def test_restart_receipt_preserves_unknown_when_docker_is_unavailable(tmp_path: Path) -> None:
    powershell = shutil.which("pwsh") or shutil.which("powershell.exe")
    git = shutil.which("git")
    if os.name != "nt" or not powershell or not git:
        pytest.skip("Windows startup receipt contract")
    environment = os.environ.copy()
    environment["CODE_AGENT_RUN_DOCKER_RESTART_E2E"] = "1"
    script = tmp_path / "initial-readiness-failure.ps1"
    runner = str(ROOT / "tests/e2e/docker_desktop_restart.ps1").replace("'", "''")
    script.write_text(
        "function Fail-DockerReadiness { param($TimeoutSeconds) throw 'fixture Docker readiness unavailable' }\n"
        "Set-Alias -Name Wait-DockerDaemonReady -Value Fail-DockerReadiness\n"
        f"& '{runner}'\nexit $LASTEXITCODE\n",
        encoding="utf-8",
    )
    result = subprocess.run(
        [powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", str(script)],
        cwd=ROOT,
        env=environment,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=15,
        check=False,
    )
    assert result.returncode == 1, result.stderr
    path = next(line.removeprefix("receipt=") for line in result.stdout.splitlines() if line.startswith("receipt="))
    receipt = json.loads(Path(path).read_text(encoding="utf-8-sig"))
    assert receipt["planned_rounds"] == 3 and receipt["completed_rounds"] == 0
    assert receipt["existing_container_count"] is None
    assert receipt["existing_volume_count"] is None
    assert receipt["probe_state"] == "unknown"
    assert "verified" not in receipt["content_scope"].lower()
    assert receipt["source_unchanged"] is False
    assert receipt["failure"] == "fixture Docker readiness unavailable"
