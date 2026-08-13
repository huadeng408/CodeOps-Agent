param(
    [string]$RunId = ("current-head-" + (Get-Date -Format "yyyyMMdd-HHmmss")),
    [string]$VerifierProxy = "",
    [string]$PhoenixUrl = "http://127.0.0.1:6006"
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent $PSScriptRoot
$projectProgress = [string]::Concat([char[]](0x9879, 0x76ee, 0x8fdb, 0x5c55))
$keyFile = Join-Path "D:\Obsidian\code-autogrowth" "$projectProgress\api-key.md"
$runRoot = Join-Path $repoRoot "eval_results\terminalbenchofficial\$RunId"
$upstreamRoot = Join-Path $runRoot "upstream"
$driverPath = Join-Path $runRoot "official_driver.py"
$finalizerPath = Join-Path $runRoot "finalize_driver.ps1"
$stdoutPath = Join-Path $runRoot "stdout.log"
$stderrPath = Join-Path $runRoot "stderr.log"
$exitPath = Join-Path $runRoot "exit-code.txt"
$pidPath = Join-Path $runRoot "pid.txt"

if (Test-Path -LiteralPath $runRoot) {
    throw "Refusing to overwrite existing receipt directory: $runRoot"
}
New-Item -ItemType Directory -Path $runRoot -Force | Out-Null

$lines = Get-Content -LiteralPath $keyFile
$keyHeader = [array]::IndexOf($lines, ($lines | Where-Object { $_ -match '(?i)beeapi.*apikey' } | Select-Object -First 1))
if ($keyHeader -lt 0 -or $keyHeader + 1 -ge $lines.Count) {
    throw "The approved key file has no BeeAPI key value after its labeled field."
}
$apiKey = $lines[$keyHeader + 1].Trim()
$baseUrl = "https://beeapi.ai/v1"
if (-not $apiKey) {
    throw "The approved key file is missing the BeeAPI API key."
}

@'
import json
import os
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

repo = Path(os.environ["LOCALCODE_REPO_ROOT"])
sys.path.insert(0, str(repo))

from eval.benchmarks.terminalbench import _patch_terminal_bench_windows
from eval.benchmarks.terminalbenchofficial import TerminalBenchOfficialConfig, TerminalBenchOfficialRunner
from eval.harness.official_receipt_trace import (
    OfficialReceiptTrace,
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
output.mkdir(parents=True, exist_ok=False)
_patch_terminal_bench_windows()
receipt_root = Path(os.environ["TERMINALBENCH_RECEIPT_ROOT"])
capture = TraceCapture()
capture.install(os.environ.get("TERMINALBENCH_PHOENIX_OTLP_ENDPOINT", ""))
phoenix_start_time = datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")
os.environ["TERMINALBENCH_PHOENIX_START_TIME"] = phoenix_start_time


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


with OfficialReceiptTrace(run_id, "break-filter-js-from-html") as trace:
    with scoped_verifier_proxy(
        os.environ.get("TERMINALBENCH_RECEIPT_VERIFIER_PROXY") or None,
        receipt_root,
    ):
        harness = Harness(
            output_path=output,
            run_id=internal_run_id,
            agent_import_path="eval.swebench_work.deepseek_tb_agent:DeepSeekTBAgent",
            agent_kwargs={
                "api_key": os.environ["LOCAL_LLM_API_KEY"],
                "base_url": os.environ["LOCAL_LLM_BASE_URL"],
                "model": "gpt-5.6-sol",
                "wire_api": "responses",
            },
            dataset_path=repo / "eval" / "benchmark_data" / "terminalbench" / "tasks",
            task_ids=["break-filter-js-from-html"],
            n_concurrent_trials=1,
            n_attempts=1,
            cleanup=False,
        )
        results = harness.run()

    # Archive only the official Harness output for this short internal run id.
    dataset_file = repo / "eval" / "benchmark_data" / "terminalbench" / "terminalbench_2.jsonl"
    dataset_sha256 = __import__("hashlib").sha256(dataset_file.read_bytes()).hexdigest()
    receipt = TerminalBenchOfficialRunner(
        TerminalBenchOfficialConfig(
            dataset_root=repo / "eval" / "benchmark_data" / "terminalbench" / "tasks",
            dataset_sha256=dataset_sha256,
            package_version="0.2.18",
            model="openai/gpt-5.6-sol",
            task_id="break-filter-js-from-html",
            max_concurrency=1,
        )
    )
    with trace.scorer():
        receipt = receipt.collect_receipt(output / internal_run_id, receipt_root)

try:
    from opentelemetry import trace as trace_api

    trace_api.get_tracer_provider().force_flush()
except Exception:  # noqa: BLE001 - telemetry never invalidates the receipt
    pass
trace_report = write_official_receipt_trace(
    receipt_root,
    capture,
    run_id,
    "break-filter-js-from-html",
    phoenix_url=os.environ.get("TERMINALBENCH_PHOENIX_URL", ""),
    phoenix_start_time=os.environ.get("TERMINALBENCH_PHOENIX_START_TIME", ""),
    phoenix_reader=phoenix_readback_with_retry,
)
refresh_receipt_checksums(receipt_root)
capture.stop()
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
print(json.dumps({"receipt_status": receipt["status"]}, ensure_ascii=True))
print(json.dumps({"trace_verdict": trace_report["verdict"]}, ensure_ascii=True))
'@ | Set-Content -LiteralPath $driverPath -Encoding utf8

@'
param(
    [string]$DriverPath,
    [string]$ExitPath,
    [string]$PhoenixUrl
)

$env:TERMINALBENCH_PHOENIX_URL = $PhoenixUrl.TrimEnd("/")
$env:TERMINALBENCH_PHOENIX_OTLP_ENDPOINT = "$($env:TERMINALBENCH_PHOENIX_URL)/v1/traces"
try {
    & "C:\Python312\python.exe" $DriverPath
    $driverExitCode = $LASTEXITCODE
    $driverExitCode | Set-Content -LiteralPath $ExitPath -Encoding ascii
    if (Test-Path -LiteralPath (Join-Path $env:TERMINALBENCH_RECEIPT_ROOT "receipt.json")) {
        & "C:\Python312\python.exe" -c "from eval.harness.official_receipt_trace import refresh_receipt_checksums; import os; refresh_receipt_checksums(os.environ['TERMINALBENCH_RECEIPT_ROOT'])"
        if ($LASTEXITCODE -ne 0) {
            exit $LASTEXITCODE
        }
    }
    exit $driverExitCode
} finally {
    Remove-Item Env:TERMINALBENCH_PHOENIX_URL -ErrorAction SilentlyContinue
    Remove-Item Env:TERMINALBENCH_PHOENIX_OTLP_ENDPOINT -ErrorAction SilentlyContinue
    Remove-Item Env:TERMINALBENCH_PHOENIX_START_TIME -ErrorAction SilentlyContinue
}
'@ | Set-Content -LiteralPath $finalizerPath -Encoding utf8

$env:LOCALCODE_REPO_ROOT = $repoRoot
$env:LOCAL_LLM_API_KEY = $apiKey
$env:LOCAL_LLM_BASE_URL = $baseUrl
$env:PYTHONUTF8 = "1"
$env:PYTHONIOENCODING = "utf-8"
$env:TERMINALBENCH_RUN_ID = $RunId
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
    -PassThru
$process.Id | Set-Content -LiteralPath $pidPath -Encoding ascii

Write-Output "Started Terminal-Bench official receipt: $RunId"
Write-Output "PID: $($process.Id)"
Write-Output "Receipt root: $runRoot"
