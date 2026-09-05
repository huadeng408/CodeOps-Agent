param(
    [string]$RunId = ("current-head-" + (Get-Date -Format "yyyyMMdd-HHmmss")),
    [string]$CheckoutPath = "",
    [string]$PhoenixUrl = "http://127.0.0.1:6006",
    [string]$ExpectedCommit = "fc0055dc4e0a316c3f83133267fbd6faaa770992",
    [ValidateSet("llm_agent", "llm_agent_solo")]
    [string]$Agent = "llm_agent",
    [ValidateSet("user_simulator", "dummy_user")]
    [string]$User = "user_simulator",
    [string]$Python = ""
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$runIdPattern = '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}\z'
if ($RunId -notmatch $runIdPattern) {
    throw "RunId must match $runIdPattern."
}

$trackedEnvironmentNames = @(
    "LOCALCODE_REPO_ROOT",
    "TAU2_RECEIPT_RUN_ID", "TAU2_RECEIPT_ROOT", "TAU2_RECEIPT_CHECKOUT",
    "TAU2_RECEIPT_EXPECTED_COMMIT", "TAU2_RECEIPT_API_KEY", "TAU2_RECEIPT_BASE_URL",
    "TAU2_RECEIPT_PYTHON", "TAU2_RECEIPT_AGENT", "TAU2_RECEIPT_USER",
    "TAU2_RECEIPT_REDACTION_HELPER",
    "PYTHONUTF8", "PYTHONIOENCODING"
)
$previousEnvironment = @{}
foreach ($name in $trackedEnvironmentNames) {
    $item = Get-Item -LiteralPath ("Env:{0}" -f $name) -ErrorAction SilentlyContinue
    $previousEnvironment[$name] = if ($null -ne $item) { $item.Value } else { $null }
}

function Restore-ReceiptEnvironment {
    foreach ($name in $trackedEnvironmentNames) {
        if ($null -eq $previousEnvironment[$name]) {
            Remove-Item -LiteralPath ("Env:{0}" -f $name) -ErrorAction SilentlyContinue
        }
        else {
            Set-Item -LiteralPath ("Env:{0}" -f $name) -Value $previousEnvironment[$name]
        }
    }
}

$CheckoutPath = if ([string]::IsNullOrWhiteSpace($CheckoutPath)) {
    if ([string]::IsNullOrWhiteSpace($env:TAU2_CHECKOUT_PATH)) {
        Join-Path (Split-Path -Parent $repoRoot) "tau2-bench-v1.0.1"
    } else {
        $env:TAU2_CHECKOUT_PATH
    }
} else {
    $CheckoutPath
}
$pythonCommand = if ([string]::IsNullOrWhiteSpace($Python)) {
    (Get-Command python -ErrorAction Stop).Source
} else {
    $Python
}
$redactionHelper = Join-Path $PSScriptRoot "lib\credential-redaction.ps1"
$runRoot = Join-Path $repoRoot "eval_results\tau2official\$RunId"
$driverPath = Join-Path $runRoot "official_driver.py"
$finalizerPath = Join-Path $runRoot "finalize_driver.ps1"
$stdoutPath = Join-Path $runRoot "stdout.log"
$stderrPath = Join-Path $runRoot "stderr.log"
$exitPath = Join-Path $runRoot "exit-code.txt"
$pidPath = Join-Path $runRoot "pid.txt"

if ([string]::IsNullOrWhiteSpace($env:LOCAL_LLM_API_KEY)) {
    throw "LOCAL_LLM_API_KEY is not configured."
}
$apiKey = $env:LOCAL_LLM_API_KEY
$baseUrl = if ([string]::IsNullOrWhiteSpace($env:LOCAL_LLM_BASE_URL)) {
    "https://beeapi.ai/v1"
} else {
    $env:LOCAL_LLM_BASE_URL
}

if (Test-Path -LiteralPath $runRoot) {
    throw "Refusing to overwrite existing receipt directory: $runRoot"
}
if (-not (Test-Path -LiteralPath $CheckoutPath)) {
    throw "Pinned tau2 checkout does not exist: $CheckoutPath"
}
New-Item -ItemType Directory -Path $runRoot -Force | Out-Null

@'
import hashlib
import json
import os
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

repo = Path(os.environ["LOCALCODE_REPO_ROOT"])
sys.path.insert(0, str(repo))

from eval.benchmarks.tau2official import (
    TAU2_V101_COMMIT,
    TAU2_V101_DATA_TREE_SHA256,
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
        expected_data_tree_sha256=TAU2_V101_DATA_TREE_SHA256,
        require_clean_checkout=True,
    )
)
if problems := runner.validate():
    raise RuntimeError("; ".join(problems))

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
        execution = runner.prepare_run(run_name)
        completed = execution.run(command, env={**os.environ, **child_env})
        (receipt_root / "process.json").write_text(
            json.dumps({"returncode": completed.returncode}, indent=2) + "\n",
            encoding="utf-8",
        )
        with trace.scorer():
            receipt = runner.collect_receipt(execution, receipt_root)
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
    refresh_receipt_checksums(receipt_root)
    print(json.dumps({"receipt_status": receipt["status"]}, ensure_ascii=True))
    print(json.dumps({"trace_verdict": trace_report["verdict"]}, ensure_ascii=True))
    if trace_report["verdict"] != "PASS":
        raise SystemExit(1)
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
. $env:TAU2_RECEIPT_REDACTION_HELPER
try {
    $driverExitCode = 0
    Invoke-RedactedNativeCommand `
        -FilePath $env:TAU2_RECEIPT_PYTHON `
        -ArgumentList @($DriverPath) `
        -Secrets @($env:TAU2_RECEIPT_API_KEY) `
        -ExitCode ([ref]$driverExitCode)
    $driverExitCode | Set-Content -LiteralPath $ExitPath -Encoding ascii
    if (Test-Path -LiteralPath (Join-Path $env:TAU2_RECEIPT_ROOT "receipt.json")) {
        $refreshExitCode = 0
        Invoke-RedactedNativeCommand `
            -FilePath $env:TAU2_RECEIPT_PYTHON `
            -ArgumentList @("-c", "from eval.harness.official_receipt_trace import refresh_receipt_checksums; import os; refresh_receipt_checksums(os.environ['TAU2_RECEIPT_ROOT'])") `
            -Secrets @($env:TAU2_RECEIPT_API_KEY) `
            -ExitCode ([ref]$refreshExitCode)
        if ($refreshExitCode -ne 0) {
            exit $refreshExitCode
        }
    }
    exit $driverExitCode
} finally {
    Remove-Item Env:TAU2_PHOENIX_URL -ErrorAction SilentlyContinue
    Remove-Item Env:TAU2_PHOENIX_OTLP_ENDPOINT -ErrorAction SilentlyContinue
}
'@ | Set-Content -LiteralPath $finalizerPath -Encoding utf8

try {
$env:LOCALCODE_REPO_ROOT = $repoRoot
$env:TAU2_RECEIPT_RUN_ID = $RunId
$env:TAU2_RECEIPT_ROOT = $runRoot
$env:TAU2_RECEIPT_CHECKOUT = $CheckoutPath
$env:TAU2_RECEIPT_EXPECTED_COMMIT = $ExpectedCommit
$env:TAU2_RECEIPT_API_KEY = $apiKey
$env:TAU2_RECEIPT_BASE_URL = $baseUrl
$env:TAU2_RECEIPT_PYTHON = $pythonCommand
$env:TAU2_RECEIPT_REDACTION_HELPER = $redactionHelper
$env:TAU2_RECEIPT_AGENT = $Agent
$env:TAU2_RECEIPT_USER = $User
$env:PYTHONUTF8 = "1"
$env:PYTHONIOENCODING = "utf-8"

$isWindowsHost = [System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT
$powerShellPath = if ($isWindowsHost) {
    "powershell.exe"
} else {
    (Get-Command pwsh -ErrorAction Stop).Source
}
$startProcess = @{
    FilePath = $powerShellPath
    ArgumentList = @(
        "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $finalizerPath,
        "-DriverPath", $driverPath, "-ExitPath", $exitPath, "-PhoenixUrl", $PhoenixUrl
    )
    RedirectStandardOutput = $stdoutPath
    RedirectStandardError = $stderrPath
    PassThru = $true
}
if ($isWindowsHost) {
    $startProcess.WindowStyle = "Hidden"
} else {
    # POSIX pwsh closes asynchronous redirection handles when the parent exits
    # before the child flushes.  Wait only on POSIX so receipt logs are durable;
    # Windows retains the intended background behaviour.
    $startProcess.Wait = $true
}
$process = Start-Process @startProcess
$process.Id | Set-Content -LiteralPath $pidPath -Encoding ascii

Write-Output "Started tau2 official receipt: $RunId"
Write-Output "PID: $($process.Id)"
Write-Output "Receipt root: $runRoot"
}
finally {
    Restore-ReceiptEnvironment
}
