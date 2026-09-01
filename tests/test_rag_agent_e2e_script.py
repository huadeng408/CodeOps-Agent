from __future__ import annotations

import subprocess
from pathlib import Path


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


def test_e2e_script_fails_nonzero_after_cleanup() -> None:
    result = subprocess.run(
        [
            "powershell.exe",
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
