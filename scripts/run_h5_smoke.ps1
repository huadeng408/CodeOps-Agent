# H5 1-instance official SWE-bench smoke.
#
# Requires the caller to inject LOCAL_LLM_API_KEY into this process. The value
# is never echoed, written to disk, or added to artifacts.
#
# Exit code is captured to <out>/run.rc so the verdict is read from the process
# itself rather than from parsed stdout.

param(
    [string]$OutDir = "",
    [string]$Model  = "deepseek-v4-pro",
    [string]$BaseUrl = "https://api.deepseek.com/v1",
    [string]$Python = ""
)

$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot
$pythonCommand = if ([string]::IsNullOrWhiteSpace($Python)) {
    (Get-Command python -ErrorAction Stop).Source
} else {
    $Python
}
$redactionHelper = Join-Path $PSScriptRoot "lib\credential-redaction.ps1"
$trackedEnvironmentNames = @(
    "LOCAL_LLM_API_KEY", "DEEPSEEK_API_KEY", "SWEBENCH_MODEL_NAME",
    "EVAL_BUDGET_SECONDS", "EVAL_BUDGET_TOKENS", "EVAL_BUDGET_COST",
    "EVAL_BUDGET_OUTPUT_BYTES"
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

Push-Location $repoRoot
try {

if ([string]::IsNullOrWhiteSpace($OutDir)) {
    $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
    $OutDir = "eval_results\h5-smoke-$stamp"
}
if ([string]::IsNullOrWhiteSpace($env:LOCAL_LLM_API_KEY)) {
    Write-Output "FATAL: LOCAL_LLM_API_KEY is not configured"
    exit 2
}
$key = $env:LOCAL_LLM_API_KEY
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$env:LOCAL_LLM_API_KEY   = $key
$env:DEEPSEEK_API_KEY    = $key
$env:SWEBENCH_MODEL_NAME = $Model

# Budgets: one instance, generous wall clock for clone + agent + docker scoring.
$env:EVAL_BUDGET_SECONDS      = "5400"
$env:EVAL_BUDGET_TOKENS       = "500000"
$env:EVAL_BUDGET_COST         = "10.0"
$env:EVAL_BUDGET_OUTPUT_BYTES = "5000000"

Write-Output ("[h5] out_dir  = " + $OutDir)
Write-Output ("[h5] model    = " + $Model)
Write-Output ("[h5] base_url = " + $BaseUrl)
Write-Output ("[h5] started  = " + (Get-Date -Format o))

    . $redactionHelper
    $rc = 0
    Invoke-RedactedNativeCommand `
        -FilePath $pythonCommand `
        -ArgumentList @("-m", "eval.run", "-b", "swebench", "-m", $Model, "--smoke", "--base-url", $BaseUrl, "-o", $OutDir) `
        -Secrets @($key) `
        -ExitCode ([ref]$rc)

    Set-Content -LiteralPath (Join-Path $OutDir "run.rc") -Value $rc -Encoding UTF8
    Write-Output ("[h5] finished = " + (Get-Date -Format o))
    Write-Output ("[h5] exit_code = " + $rc)
    exit $rc
}
finally {
    Restore-ReceiptEnvironment
    Pop-Location
}
