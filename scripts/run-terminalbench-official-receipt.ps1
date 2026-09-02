param(
    [string]$RunId = ("current-head-" + (Get-Date -Format "yyyyMMdd-HHmmss")),
    [string]$VerifierProxy = "",
    [string]$PhoenixUrl = "http://127.0.0.1:6006",
    [string]$Python = ""
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$runIdPattern = '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}\z'
if ($RunId -notmatch $runIdPattern) {
    throw "RunId must match $runIdPattern."
}

$trackedEnvironmentNames = @(
    "LOCALCODE_REPO_ROOT", "LOCAL_LLM_API_KEY", "LOCAL_LLM_BASE_URL",
    "TERMINALBENCH_RECEIPT_PYTHON", "TERMINALBENCH_RECEIPT_REDACTION_HELPER",
    "PYTHONUTF8", "PYTHONIOENCODING",
    "TERMINALBENCH_RUN_ID", "TERMINALBENCH_INSTANCE_ID", "TERMINALBENCH_OUTPUT_DIR",
    "TERMINALBENCH_RECEIPT_ROOT", "TERMINALBENCH_RECEIPT_VERIFIER_PROXY"
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

$pythonCommand = if ([string]::IsNullOrWhiteSpace($Python)) {
    (Get-Command python -ErrorAction Stop).Source
} else {
    $Python
}
$redactionHelper = Join-Path $PSScriptRoot "lib\credential-redaction.ps1"
$runRoot = Join-Path $repoRoot "eval_results\terminalbenchofficial\$RunId"
$upstreamRoot = Join-Path $runRoot "upstream"
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
New-Item -ItemType Directory -Path $runRoot -Force | Out-Null

@'
import json
import os
import sys
import time
from datetime import datetime, timezone
from importlib.metadata import version
from pathlib import Path

repo = Path(os.environ["LOCALCODE_REPO_ROOT"])
sys.path.insert(0, str(repo))

from eval.benchmarks.terminalbench import _patch_terminal_bench_windows
from eval.benchmarks.terminalbenchofficial import (
    TERMINAL_BENCH_DATASET_SHA256,
    TERMINAL_BENCH_PACKAGE,
    TERMINAL_BENCH_PACKAGE_VERSION,
    TERMINAL_BENCH_TASK_ID,
    TERMINAL_BENCH_TASK_TREE_SHA256,
    TerminalBenchOfficialConfig,
    TerminalBenchOfficialRunner,
)
from eval.harness.official_receipt_trace import (
    OfficialReceiptTrace,
    evaluate_official_receipt_trace,
    refresh_receipt_checksums,
    write_official_receipt_trace,
)
from eval.harness.phoenix import read_run_spans
from eval.harness.trace_capture import TraceCapture
from eval.swebench_work.terminalbench_proxy import internal_harness_run_id, scoped_verifier_proxy
from terminal_bench.harness import Harness

for stream in (sys.stdout, sys.stderr):
    try:
        stream.reconfigure(encoding="utf-8", errors="replace")
    except (AttributeError, ValueError):
        pass

run_id = os.environ["TERMINALBENCH_RUN_ID"]
internal_run_id = internal_harness_run_id(run_id)
output = Path(os.environ["TERMINALBENCH_OUTPUT_DIR"])
receipt_root = Path(os.environ["TERMINALBENCH_RECEIPT_ROOT"])
dataset_root = repo / "eval" / "benchmark_data" / "terminalbench" / "tasks"


def flush_traces() -> None:
    try:
        from opentelemetry import trace as trace_api

        trace_api.get_tracer_provider().force_flush()
    except Exception:  # noqa: BLE001 - telemetry never invalidates the receipt
        pass


def run_harness_child() -> int:
    """Run only the official Harness in a separate, exit-code-visible process."""
    _patch_terminal_bench_windows()
    child_capture = TraceCapture()
    child_capture.install(os.environ.get("TERMINALBENCH_PHOENIX_OTLP_ENDPOINT", ""))
    try:
        with scoped_verifier_proxy(
            os.environ.get("TERMINALBENCH_RECEIPT_VERIFIER_PROXY") or None,
            receipt_root,
        ):
            harness = Harness(
                output_path=output,
                run_id=internal_run_id,
                agent_import_path="eval.swebench_work.deepseek_tb_agent:DeepSeekTBAgent",
                model_name="gpt-5.6-sol",
                agent_kwargs={
                    "api_key": os.environ["LOCAL_LLM_API_KEY"],
                    "base_url": os.environ["LOCAL_LLM_BASE_URL"],
                    "model": "gpt-5.6-sol",
                    "wire_api": "responses",
                },
                dataset_path=dataset_root,
                task_ids=[TERMINAL_BENCH_TASK_ID],
                n_concurrent_trials=1,
                n_attempts=1,
                cleanup=False,
            )
            results = harness.run()
        print(json.dumps({
            "run_id": run_id,
            "harness_run_id": internal_run_id,
            "n_resolved": results.n_resolved,
            "n_unresolved": results.n_unresolved,
            "accuracy": results.accuracy,
            "results": [
                {
                    "task_id": item.task_id,
                    "is_resolved": item.is_resolved,
                    "failure_mode": str(item.failure_mode),
                    "tokens_in": item.total_input_tokens,
                    "tokens_out": item.total_output_tokens,
                }
                for item in results.results
            ],
        }, ensure_ascii=True))
        return 0
    finally:
        flush_traces()
        child_capture.stop()


if "--harness-child" in sys.argv:
    raise SystemExit(run_harness_child())


installed_package_version = version(TERMINAL_BENCH_PACKAGE)
if installed_package_version != TERMINAL_BENCH_PACKAGE_VERSION:
    raise RuntimeError(
        "installed Terminal-Bench version does not match the production pin: "
        f"{installed_package_version} != {TERMINAL_BENCH_PACKAGE_VERSION}"
    )
receipt_runner = TerminalBenchOfficialRunner(
    TerminalBenchOfficialConfig(
        dataset_root=dataset_root,
        dataset_sha256=TERMINAL_BENCH_DATASET_SHA256,
        package_version=TERMINAL_BENCH_PACKAGE_VERSION,
        model="openai/gpt-5.6-sol",
        task_id=TERMINAL_BENCH_TASK_ID,
        max_concurrency=1,
        n_attempts=1,
        expected_task_tree_sha256=TERMINAL_BENCH_TASK_TREE_SHA256,
        official_command=(sys.executable, str(Path(__file__).resolve()), "--harness-child"),
    )
)
receipt_runner.validate_input_pins()
output.mkdir(parents=True, exist_ok=False)
capture = TraceCapture()
capture.install(os.environ.get("TERMINALBENCH_PHOENIX_OTLP_ENDPOINT", ""))
phoenix_start_time = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
os.environ["TERMINALBENCH_PHOENIX_START_TIME"] = phoenix_start_time


def phoenix_readback_with_retry(*args):
    last_error = None
    for attempt in range(5):
        try:
            spans = read_run_spans(*args)
            report = evaluate_official_receipt_trace(
                spans, run_id, TERMINAL_BENCH_TASK_ID
            )
            if report["verdict"] == "PASS":
                return spans
            last_error = RuntimeError(
                f"Phoenix receipt trace is incomplete: {report['verdict']}"
            )
        except Exception as exc:  # noqa: BLE001 - final writer records the gap
            last_error = exc
        if attempt < 4:
            time.sleep(1)
    assert last_error is not None
    raise last_error


with OfficialReceiptTrace(run_id, TERMINAL_BENCH_TASK_ID) as trace:
    official_run_dir = output / internal_run_id
    execution = receipt_runner.prepare_run(official_run_dir)
    with trace.worker_environment():
        completed = execution.run(
            [sys.executable, str(Path(__file__).resolve()), "--harness-child"],
            cwd=repo,
            env=os.environ.copy(),
        )

    with trace.scorer():
        receipt = receipt_runner.collect_receipt(execution, receipt_root)

flush_traces()
trace_report = write_official_receipt_trace(
    receipt_root,
    capture,
    run_id,
    TERMINAL_BENCH_TASK_ID,
    phoenix_url=os.environ.get("TERMINALBENCH_PHOENIX_URL", ""),
    phoenix_start_time=os.environ.get("TERMINALBENCH_PHOENIX_START_TIME", ""),
    phoenix_reader=phoenix_readback_with_retry,
)
refresh_receipt_checksums(receipt_root)
capture.stop()
print(json.dumps({"receipt_status": receipt["status"]}, ensure_ascii=True))
print(json.dumps({"trace_verdict": trace_report["verdict"]}, ensure_ascii=True))
if trace_report["verdict"] != "PASS":
    raise SystemExit(1)
raise SystemExit(completed.returncode)
'@ | Set-Content -LiteralPath $driverPath -Encoding utf8

@'
param(
    [string]$DriverPath,
    [string]$ExitPath,
    [string]$PhoenixUrl
)

$env:TERMINALBENCH_PHOENIX_URL = $PhoenixUrl.TrimEnd("/")
$env:TERMINALBENCH_PHOENIX_OTLP_ENDPOINT = "$($env:TERMINALBENCH_PHOENIX_URL)/v1/traces"
. $env:TERMINALBENCH_RECEIPT_REDACTION_HELPER
try {
    $driverExitCode = 0
    Invoke-RedactedNativeCommand `
        -FilePath $env:TERMINALBENCH_RECEIPT_PYTHON `
        -ArgumentList @($DriverPath) `
        -Secrets @($env:LOCAL_LLM_API_KEY) `
        -ExitCode ([ref]$driverExitCode)
    $driverExitCode | Set-Content -LiteralPath $ExitPath -Encoding ascii
    if (Test-Path -LiteralPath (Join-Path $env:TERMINALBENCH_RECEIPT_ROOT "receipt.json")) {
        $refreshExitCode = 0
        Invoke-RedactedNativeCommand `
            -FilePath $env:TERMINALBENCH_RECEIPT_PYTHON `
            -ArgumentList @("-c", "from eval.harness.official_receipt_trace import refresh_receipt_checksums; import os; refresh_receipt_checksums(os.environ['TERMINALBENCH_RECEIPT_ROOT'])") `
            -Secrets @($env:LOCAL_LLM_API_KEY) `
            -ExitCode ([ref]$refreshExitCode)
        if ($refreshExitCode -ne 0) {
            exit $refreshExitCode
        }
    }
    exit $driverExitCode
} finally {
    Remove-Item Env:TERMINALBENCH_PHOENIX_URL -ErrorAction SilentlyContinue
    Remove-Item Env:TERMINALBENCH_PHOENIX_OTLP_ENDPOINT -ErrorAction SilentlyContinue
    Remove-Item Env:TERMINALBENCH_PHOENIX_START_TIME -ErrorAction SilentlyContinue
    Remove-Item Env:TERMINALBENCH_INSTANCE_ID -ErrorAction SilentlyContinue
}
'@ | Set-Content -LiteralPath $finalizerPath -Encoding utf8

try {
$env:LOCALCODE_REPO_ROOT = $repoRoot
$env:LOCAL_LLM_API_KEY = $apiKey
$env:LOCAL_LLM_BASE_URL = $baseUrl
$env:TERMINALBENCH_RECEIPT_PYTHON = $pythonCommand
$env:TERMINALBENCH_RECEIPT_REDACTION_HELPER = $redactionHelper
$env:PYTHONUTF8 = "1"
$env:PYTHONIOENCODING = "utf-8"
$env:TERMINALBENCH_RUN_ID = $RunId
$env:TERMINALBENCH_INSTANCE_ID = "break-filter-js-from-html"
$env:TERMINALBENCH_OUTPUT_DIR = $upstreamRoot
$env:TERMINALBENCH_RECEIPT_ROOT = $runRoot
if ($VerifierProxy) {
    $env:TERMINALBENCH_RECEIPT_VERIFIER_PROXY = $VerifierProxy
} else {
    Remove-Item Env:TERMINALBENCH_RECEIPT_VERIFIER_PROXY -ErrorAction SilentlyContinue
}

$process = Start-Process -FilePath "powershell.exe" `
    -ArgumentList @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $finalizerPath, "-DriverPath", $driverPath, "-ExitPath", $exitPath, "-PhoenixUrl", $PhoenixUrl) `
    -RedirectStandardOutput $stdoutPath `
    -RedirectStandardError $stderrPath `
    -WindowStyle Hidden `
    -PassThru
$process.Id | Set-Content -LiteralPath $pidPath -Encoding ascii

Write-Output "Started Terminal-Bench official receipt: $RunId"
Write-Output "PID: $($process.Id)"
Write-Output "Receipt root: $runRoot"
}
finally {
    Restore-ReceiptEnvironment
}
