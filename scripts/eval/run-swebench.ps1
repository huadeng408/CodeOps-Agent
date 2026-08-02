# SWE-bench Verified runner (plan Task 8.2).
#
# Generates official-format predictions and invokes the OFFICIAL SWE-bench
# Docker harness for scoring. Never modifies tests or the scorer.
#
# Usage (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts/eval/run-swebench.ps1 `
#       -ModelName deepseek-v4-pro -Limit 10 -RunId swebench-001
#
# Stages: 1 -> fixed 10 -> approved 100 (default 10). Requires Docker for
# the official scoring step.

param(
    [string]$ModelName = "deepseek-v4-pro",
    [int]$Limit = 10,
    [string]$RunId = "swebench-pilot",
    [string]$OutputDir = "results/swebench"
)

$ErrorActionPreference = "Stop"

New-Item -ItemType Directory -Force -Path $OutputDir | Out-Null

$PredictionsPath = Join-Path $OutputDir "$RunId-predictions.jsonl"

Write-Host "=== SWE-bench Verified: generating predictions ==="
Write-Host "  model      : $ModelName"
Write-Host "  limit      : $Limit"
Write-Host "  run_id     : $RunId"

python -m eval.benchmarks.swebench --max-instances $Limit --output-dir $OutputDir
if ($LASTEXITCODE -ne 0) {
    Write-Error "prediction generation failed with exit code $LASTEXITCODE"
    exit $LASTEXITCODE
}

Write-Host ""
Write-Host "=== SWE-bench Verified: official scoring (Docker harness) ==="
Write-Host "  predictions: $PredictionsPath"

python -m swebench.harness.run_evaluation `
    --dataset_name princeton-nlp/SWE-bench_Verified `
    --predictions_path $PredictionsPath `
    --max_workers 4 `
    --run_id $RunId

if ($LASTEXITCODE -ne 0) {
    Write-Error "official SWE-bench harness failed with exit code $LASTEXITCODE"
    exit $LASTEXITCODE
}

Write-Host ""
Write-Host "Scoring complete. Results: $OutputDir"
