from __future__ import annotations

import os
import shutil
import subprocess
from pathlib import Path

import pytest


ROOT = Path(__file__).parents[1]
SCRIPT = ROOT / "scripts" / "rag-agent-e2e.ps1"


def test_e2e_script_maps_client_secret_to_server_environment() -> None:
    source = SCRIPT.read_text(encoding="utf-8")
    assert "Invoke-WithOrchestratorSharedSecret -Secret $internalSecret" in source
    assert '"ORCHESTRATOR_SHARED_SECRET"' in source
    assert '$env:ORCHESTRATOR_SHARED_SECRET = $internalSecret' not in source


def test_server_config_has_no_tracked_internal_secret_fallback() -> None:
    source = (ROOT / "configs" / "server.yaml").read_text(encoding="utf-8")
    assert 'shared_secret: "${ORCHESTRATOR_SHARED_SECRET:}"' in source
    assert "codeagent-internal-dev" not in source


def test_snapshot_does_not_put_mysql_password_on_command_line() -> None:
    source = SCRIPT.read_text(encoding="utf-8")
    assert "docker exec -e MYSQL_PWD=codeagent codeagent-mysql mysql -ucodeagent" in source
    assert "mysql -ucodeagent -pcodeagent" not in source


def test_e2e_starts_and_cleans_up_the_python_ingestion_worker() -> None:
    source = SCRIPT.read_text(encoding="utf-8")
    runtime = (ROOT / "scripts" / "rag-agent-e2e-runtime.ps1").read_text(encoding="utf-8")
    assert "orchestrator.rag.main:app" in source
    assert 'Test-HttpEndpoint "$workerUrl/healthz"' in source
    assert '"--port", "$WorkerPort"' in source
    assert "$workerStartedHere" in source
    assert "Stop-Process -Id $workerProcess.Id" in source
    assert "Invoke-WithPaismartInternalToken -Secret $internalSecret" in source
    assert 'PAISMART_EMBEDDING_BASE_URL = "http://127.0.0.1:8009"' in runtime
    assert 'PAISMART_EMBEDDING_MODEL = "BAAI/bge-m3"' in runtime
    assert 'PAISMART_EMBEDDING_DIMENSIONS = "1024"' in runtime


def test_e2e_waits_for_docker_desktop_readiness_with_a_shared_helper() -> None:
    source = SCRIPT.read_text(encoding="utf-8")
    runtime = (ROOT / "scripts" / "rag-agent-e2e-runtime.ps1").read_text(encoding="utf-8")
    assert "Wait-DockerDaemonReady -TimeoutSeconds $StartupTimeoutSeconds" in source
    assert "function Wait-DockerDaemonReady" in runtime
    assert "Invoke-DockerProbe" in runtime
    assert "-ProbeTimeoutMilliseconds $probeTimeoutMilliseconds" in runtime
    assert "Start-Sleep -Seconds 2" in runtime
    assert "docker info --format '{{.ServerVersion}}' | Out-Null" not in source


def test_interview_script_uses_shared_docker_readiness_helper() -> None:
    source = (ROOT / "scripts" / "start-interview.ps1").read_text(encoding="utf-8")
    assert "rag-agent-e2e-runtime.ps1" in source
    assert "Wait-DockerDaemonReady -TimeoutSeconds" in source
    assert "docker info --format '{{.ServerVersion}}' | Out-Null" not in source


def test_interview_script_bootstraps_ephemeral_orchestrator_secret() -> None:
    source = (ROOT / "scripts" / "start-interview.ps1").read_text(encoding="utf-8")
    assert "ORCHESTRATOR_SHARED_SECRET" in source
    assert "[guid]::NewGuid().ToString('N')" in source
    assert "CODE_AGENT_PROVIDER_CONFIG" in source


def test_interview_script_enables_harness_worktree_callbacks() -> None:
    source = (ROOT / "scripts" / "start-interview.ps1").read_text(encoding="utf-8")
    assert "CODE_AGENT_REQUIRE_HARNESS_WORKTREE" in source
    assert "$env:CODE_AGENT_REQUIRE_HARNESS_WORKTREE = '1'" in source


def test_runtime_resolves_and_starts_docker_desktop_before_readiness_poll() -> None:
    runtime = (ROOT / "scripts" / "rag-agent-e2e-runtime.ps1").read_text(encoding="utf-8")
    snapshot = (ROOT / "scripts" / "rag_snapshot.ps1").read_text(encoding="utf-8")
    assert "function Resolve-DockerCli" in runtime
    assert "Docker\\Docker\\resources\\bin\\docker.exe" in runtime
    assert "function Start-DockerDesktopIfNeeded" in runtime
    assert "Start-Process -FilePath $desktop -WindowStyle Hidden" in runtime
    assert "$probeArguments = @('info', '--format', '{{.ServerVersion}}')" in runtime
    assert "rag-agent-e2e-runtime.ps1" in snapshot
    assert "[int]$DockerTimeoutSeconds = 300" in snapshot
    assert "Wait-DockerDaemonReady -TimeoutSeconds $DockerTimeoutSeconds" in snapshot


def test_runtime_allows_a_long_cold_start_but_keeps_explicit_probe_budgets() -> None:
    runtime = (ROOT / "scripts" / "rag-agent-e2e-runtime.ps1").read_text(encoding="utf-8")
    assert "[int]$TimeoutSeconds = 300" in runtime
    assert "Callers can still pass a shorter budget" in runtime


def test_release_gate_pins_setup_python_for_go_subprocess_e2e() -> None:
    workflow = (ROOT / ".github" / "workflows" / "release-gate.yml").read_text(
        encoding="utf-8"
    )
    assert 'PYTHON_EXECUTABLE=${pythonLocation}/bin/python' in workflow


def test_runtime_ignores_stale_docker_host_for_desktop_linux() -> None:
    powershell = shutil.which("pwsh") or shutil.which("powershell.exe")
    if not powershell:
        pytest.skip("PowerShell is unavailable")
    environment = os.environ.copy()
    environment["DOCKER_HOST"] = "npipe:////./pipe/code-agent-stale-docker"
    environment["DOCKER_CONTEXT"] = "default"
    environment["DOCKER_TLS_VERIFY"] = "1"
    environment["DOCKER_CERT_PATH"] = r"C:\missing-docker-certs"
    command = (
        f". '{ROOT / 'scripts' / 'rag-agent-e2e-runtime.ps1'}'; "
        "Wait-DockerDaemonReady -TimeoutSeconds 10; "
        "Write-Output ('context=' + $env:DOCKER_CONTEXT); "
        "Write-Output ('host=' + [string]$env:DOCKER_HOST)"
    )
    result = subprocess.run(
        [powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command],
        cwd=ROOT,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        env=environment,
        timeout=20,
        check=False,
    )
    if result.returncode != 0 and "Docker CLI was not found" in result.stderr:
        pytest.skip("Docker CLI is unavailable")
    if result.returncode != 0 and "timed out waiting for Docker daemon" in result.stderr:
        pytest.skip("Docker daemon is unavailable")
    assert result.returncode == 0, result.stderr
    assert "ready: Docker daemon" in result.stdout
    # Docker Desktop exposes desktop-linux on Windows/WSL. Hosted Linux
    # runners use the default Unix-socket context, but must still clear the
    # stale host/TLS variables before probing it.
    context_probe_env = {
        key: value
        for key, value in environment.items()
        if key not in {"DOCKER_HOST", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"}
    }
    context_probe = subprocess.run(
        ["docker", "context", "ls", "--format", "{{.Name}}"],
        env=context_probe_env,
        capture_output=True,
        text=True,
        check=False,
    )
    expected_context = (
        "desktop-linux"
        if "desktop-linux" in context_probe.stdout.splitlines()
        else environment.get("DOCKER_CONTEXT", "default")
    )
    assert f"context={expected_context}" in result.stdout
    assert "host=" in result.stdout
    assert "code-agent-stale-docker" not in result.stdout


def test_runtime_bounds_each_docker_probe_and_reaps_hung_process() -> None:
    runtime = (ROOT / "scripts" / "rag-agent-e2e-runtime.ps1").read_text(
        encoding="utf-8"
    )

    assert "function Invoke-DockerProbe" in runtime
    assert "WaitForExit($ProbeTimeoutMilliseconds)" in runtime
    assert "Stop-ProcessTree $process" in runtime
    assert "docker probe timed out" in runtime
    assert "$Process.WaitForExit()" not in runtime


def test_runtime_reaps_a_hung_docker_probe_within_its_timeout() -> None:
    powershell = shutil.which("pwsh") or shutil.which("powershell.exe")
    if not powershell:
        pytest.skip("PowerShell is unavailable")
    runtime = (ROOT / "scripts" / "rag-agent-e2e-runtime.ps1").as_posix()
    runtime_literal = runtime.replace("'", "''")
    powershell_literal = powershell.replace("'", "''")
    command = (
        f". '{runtime_literal}'; "
        f"$result = Invoke-DockerProbe -DockerCli '{powershell_literal}' "
        "-ProbeArguments @('-NoProfile','-Command','Start-Sleep -Seconds 30') "
        "-ProbeTimeoutMilliseconds 250; "
        "Write-Output ('timed_out=' + $result.TimedOut)"
    )
    result = subprocess.run(
        [powershell, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command],
        cwd=ROOT,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        timeout=10,
        check=False,
    )

    assert result.returncode == 0, result.stderr
    assert "timed_out=True" in result.stdout


def test_e2e_defaults_to_isolated_go_and_python_ports() -> None:
    source = SCRIPT.read_text(encoding="utf-8")
    assert '[string]$ServerUrl = "http://127.0.0.1:8082"' in source
    assert '[int]$WorkerPort = 8092' in source


def test_e2e_rejects_reusing_existing_app_processes() -> None:
    source = SCRIPT.read_text(encoding="utf-8")
    assert 'throw "refusing to reuse an existing Python ingestion worker' in source
    assert 'throw "refusing to reuse an existing Go server' in source


def test_e2e_resolves_the_actual_minio_container_and_cleans_topics() -> None:
    source = SCRIPT.read_text(encoding="utf-8")
    assert "function Resolve-MinIOContainerName" in source
    assert "codeagent-minio" in source
    assert "function Remove-E2EKafkaTopics" in source
    assert "function Remove-E2EKafkaGroups" in source
    assert "Remove-E2EKafkaTopics" in source[source.index("finally {"):]
    assert "Remove-E2EKafkaGroups" in source[source.index("finally {"):]


def test_e2e_treats_missing_kafka_groups_as_idempotent_cleanup() -> None:
    source = SCRIPT.read_text(encoding="utf-8")
    cleanup = source[source.index("function Remove-E2EKafkaGroups"):source.index("function Resolve-MinIOContainerName")]
    assert "2>&1" in cleanup
    assert "GroupIdNotFoundException" in cleanup
    assert "does not exist" in cleanup


def test_e2e_retries_busy_kafka_group_deletion_until_success() -> None:
    source = SCRIPT.read_text(encoding="utf-8")
    cleanup = source[source.index("function Remove-E2EKafkaGroups"):source.index("function Resolve-MinIOContainerName")]
    assert "GroupNotEmptyException" in cleanup
    assert "Start-Sleep -Seconds 2" in cleanup
    assert "AddSeconds(180)" in cleanup
    assert "was successful" in cleanup


def test_e2e_script_fails_nonzero_after_cleanup() -> None:
    powershell = shutil.which("pwsh") or shutil.which("powershell.exe")
    if not powershell:
        pytest.skip("PowerShell is unavailable")
    result = subprocess.run(
        [
            powershell,
            "-NoProfile",
            "-File",
            str(SCRIPT),
            "-MinerUCommand",
            r"Z:\definitely-missing\mineru.exe",
            "-StartupTimeoutSeconds",
            "1",
        ],
        cwd=ROOT,
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )
    assert result.returncode != 0
    assert "E2E failed" in result.stdout
    assert "MinerU executable not found" in result.stderr
