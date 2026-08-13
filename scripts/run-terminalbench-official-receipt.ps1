param(
    [string]$RunId = ("current-head-" + (Get-Date -Format "yyyyMMdd-HHmmss")),
    [string]$VerifierProxy = ""
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
from pathlib import Path

repo = Path(os.environ["LOCALCODE_REPO_ROOT"])
sys.path.insert(0, str(repo))

from eval.benchmarks.terminalbench import _patch_terminal_bench_windows
from eval.benchmarks.terminalbenchofficial import TerminalBenchOfficialConfig, TerminalBenchOfficialRunner
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
with scoped_verifier_proxy(
    os.environ.get("TERMINALBENCH_RECEIPT_VERIFIER_PROXY") or None,
    Path(os.environ["TERMINALBENCH_RECEIPT_ROOT"]),
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
).collect_receipt(output / internal_run_id, Path(os.environ["TERMINALBENCH_RECEIPT_ROOT"]))
print(json.dumps({"receipt_status": receipt["status"]}, ensure_ascii=True))
'@ | Set-Content -LiteralPath $driverPath -Encoding utf8

@'
param(
    [string]$DriverPath,
    [string]$ExitPath
)

& "C:\Python312\python.exe" $DriverPath
$LASTEXITCODE | Set-Content -LiteralPath $ExitPath -Encoding ascii
exit $LASTEXITCODE
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
    -ArgumentList @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $finalizerPath, "-DriverPath", $driverPath, "-ExitPath", $exitPath) `
    -RedirectStandardOutput $stdoutPath `
    -RedirectStandardError $stderrPath `
    -PassThru
$process.Id | Set-Content -LiteralPath $pidPath -Encoding ascii

Write-Output "Started Terminal-Bench official receipt: $RunId"
Write-Output "PID: $($process.Id)"
Write-Output "Receipt root: $runRoot"
