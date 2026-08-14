param(
    [string]$RunId = ("current-head-" + (Get-Date -Format "yyyyMMdd-HHmmss")),
    [string]$CheckoutPath = "D:\vscode\tau2-bench-v1.0.1",
    [string]$PhoenixUrl = "http://127.0.0.1:6006",
    [string]$ExpectedCommit = "fc0055dc4e0a316c3f83133267fbd6faaa770992",
    [ValidateSet("llm_agent", "llm_agent_solo")]
    [string]$Agent = "llm_agent",
    [ValidateSet("user_simulator", "dummy_user")]
    [string]$User = "user_simulator"
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$projectProgress = [string]::Concat([char[]](0x9879, 0x76ee, 0x8fdb, 0x5c55))
$keyFile = Join-Path "D:\Obsidian\code-autogrowth" "$projectProgress\api-key.md"
$runRoot = Join-Path $repoRoot "eval_results\tau2official\$RunId"
$driverPath = Join-Path $runRoot "official_driver.py"
$finalizerPath = Join-Path $runRoot "finalize_driver.ps1"
$stdoutPath = Join-Path $runRoot "stdout.log"
$stderrPath = Join-Path $runRoot "stderr.log"
$exitPath = Join-Path $runRoot "exit-code.txt"
$pidPath = Join-Path $runRoot "pid.txt"

if (Test-Path -LiteralPath $runRoot) {
    throw "Refusing to overwrite existing receipt directory: $runRoot"
}
if (-not (Test-Path -LiteralPath $CheckoutPath)) {
    throw "Pinned tau2 checkout does not exist: $CheckoutPath"
}
New-Item -ItemType Directory -Path $runRoot -Force | Out-Null

$lines = Get-Content -LiteralPath $keyFile
$keyHeader = [array]::IndexOf($lines, ($lines | Where-Object { $_ -match '(?i)beeapi.*apikey' } | Select-Object -First 1))
if ($keyHeader -lt 0 -or $keyHeader + 1 -ge $lines.Count) {
    throw "The approved key file has no BeeAPI key value after its labeled field."
}
$apiKey = $lines[$keyHeader + 1].Trim()
if (-not $apiKey) {
    throw "The approved key file is missing the BeeAPI API key."
}

@'
import hashlib
import json
import os
import subprocess
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

repo = Path(os.environ["LOCALCODE_REPO_ROOT"])
sys.path.insert(0, str(repo))

from eval.benchmarks.tau2official import (
    TAU2_V101_COMMIT,
    Tau2OfficialConfig,
    Tau2OfficialRunner,
    source_data_tree_sha256,
)
from eval.harness.official_receipt_trace import (
    OfficialReceiptTrace,
    refresh_receipt_checksums,
    write_official_receipt_trace,
)
from eval.harness.phoenix import read_run_spans
from eval.harness.trace_capture import TraceCapture


def phoenix_readback_with_retry(*args):
    last_error = None
    for attempt in range(5):
        try:
            spans = read_run_spans(*args)
            if spans:
                return spans
            last_error = RuntimeError("Phoenix returned no spans for receipt run")
        except Exception as exc:  # noqa: BLE001 - final writer records the gap
            last_error = exc
        if attempt < 4:
            time.sleep(1)
    assert last_error is not None
    raise last_error


run_id = os.environ["TAU2_RECEIPT_RUN_ID"]
checkout = Path(os.environ["TAU2_RECEIPT_CHECKOUT"])
receipt_root = Path(os.environ["TAU2_RECEIPT_ROOT"])
expected_commit = os.environ["TAU2_RECEIPT_EXPECTED_COMMIT"]
if expected_commit != TAU2_V101_COMMIT:
    raise RuntimeError("receipt script pin does not match Tau2OfficialRunner pin")
data_hash = source_data_tree_sha256(checkout)
runner = Tau2OfficialRunner(
    Tau2OfficialConfig(
        checkout=checkout,
        source_commit=expected_commit,
        data_tree_sha256=data_hash,
        model="openai/gpt-5.6-sol",
        seed=42,
        max_concurrency=1,
        domain="mock",
        agent=os.environ["TAU2_RECEIPT_AGENT"],
        user=os.environ["TAU2_RECEIPT_USER"],
    )
)
if problems := runner.validate():
    raise RuntimeError("; ".join(problems))
actual_commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=checkout, text=True).strip()
if actual_commit != expected_commit:
    raise RuntimeError("pinned tau2 checkout source commit does not match")

capture = TraceCapture()
capture.install(os.environ.get("TAU2_PHOENIX_OTLP_ENDPOINT", ""))
phoenix_start_time = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
run_name = f"localcode-{run_id}"
child_env = {
    "OPENAI_API_KEY": os.environ["TAU2_RECEIPT_API_KEY"],
    "OPENAI_API_BASE": os.environ["TAU2_RECEIPT_BASE_URL"],
    "PYTHONUTF8": "1",
}
try:
    with OfficialReceiptTrace(run_id, "mock/1") as trace:
        command = runner.command(run_name)
        assert command[:4] == ["uv", "run", "tau2", "run"]
        completed = subprocess.run(command, cwd=checkout, env={**os.environ, **child_env}, check=False)
        with trace.scorer():
            receipt = runner.collect_receipt(run_name, receipt_root)
    try:
        from opentelemetry import trace as trace_api

        trace_api.get_tracer_provider().force_flush()
    except Exception:  # noqa: BLE001 - telemetry never invalidates the receipt
        pass
    trace_report = write_official_receipt_trace(
        receipt_root,
        capture,
        run_id,
        "mock/1",
        phoenix_url=os.environ.get("TAU2_PHOENIX_URL", ""),
        phoenix_start_time=phoenix_start_time,
        phoenix_reader=phoenix_readback_with_retry,
    )
    (receipt_root / "process.json").write_text(
        json.dumps({"returncode": completed.returncode}, indent=2) + "\n", encoding="utf-8"
    )
    refresh_receipt_checksums(receipt_root)
    print(json.dumps({"receipt_status": receipt["status"]}, ensure_ascii=True))
    print(json.dumps({"trace_verdict": trace_report["verdict"]}, ensure_ascii=True))
    raise SystemExit(completed.returncode)
finally:
    capture.stop()
'@ | Set-Content -LiteralPath $driverPath -Encoding utf8

@'
param(
    [string]$DriverPath,
    [string]$ExitPath,
    [string]$PhoenixUrl
)

$env:TAU2_PHOENIX_URL = $PhoenixUrl.TrimEnd("/")
$env:TAU2_PHOENIX_OTLP_ENDPOINT = "$($env:TAU2_PHOENIX_URL)/v1/traces"
try {
    & "C:\Python312\python.exe" $DriverPath
    $driverExitCode = $LASTEXITCODE
    $driverExitCode | Set-Content -LiteralPath $ExitPath -Encoding ascii
    if (Test-Path -LiteralPath (Join-Path $env:TAU2_RECEIPT_ROOT "receipt.json")) {
        & "C:\Python312\python.exe" -c "from eval.harness.official_receipt_trace import refresh_receipt_checksums; import os; refresh_receipt_checksums(os.environ['TAU2_RECEIPT_ROOT'])"
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }
    }
    exit $driverExitCode
} finally {
    Remove-Item Env:TAU2_PHOENIX_URL -ErrorAction SilentlyContinue
    Remove-Item Env:TAU2_PHOENIX_OTLP_ENDPOINT -ErrorAction SilentlyContinue
}
'@ | Set-Content -LiteralPath $finalizerPath -Encoding utf8

$env:LOCALCODE_REPO_ROOT = $repoRoot
$env:TAU2_RECEIPT_RUN_ID = $RunId
$env:TAU2_RECEIPT_ROOT = $runRoot
$env:TAU2_RECEIPT_CHECKOUT = $CheckoutPath
$env:TAU2_RECEIPT_EXPECTED_COMMIT = $ExpectedCommit
$env:TAU2_RECEIPT_API_KEY = $apiKey
$env:TAU2_RECEIPT_BASE_URL = "https://beeapi.ai/v1"
$env:TAU2_RECEIPT_AGENT = $Agent
$env:TAU2_RECEIPT_USER = $User
$env:PYTHONUTF8 = "1"
$env:PYTHONIOENCODING = "utf-8"

$process = Start-Process -FilePath "powershell.exe" `
    -ArgumentList @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $finalizerPath, "-DriverPath", $driverPath, "-ExitPath", $exitPath, "-PhoenixUrl", $PhoenixUrl) `
    -RedirectStandardOutput $stdoutPath `
    -RedirectStandardError $stderrPath `
    -PassThru
$process.Id | Set-Content -LiteralPath $pidPath -Encoding ascii

Write-Output "Started tau2 official receipt: $RunId"
Write-Output "PID: $($process.Id)"
Write-Output "Receipt root: $runRoot"
