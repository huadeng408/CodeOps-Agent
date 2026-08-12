param(
    [string]$OutputDir = "eval_results/demo",
    [string]$RunId = "offline-harness-demo"
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $RepoRoot
try {
    & python -m eval.demo --output-dir $OutputDir --run-id $RunId
    exit $LASTEXITCODE
}
finally {
    Pop-Location
}
