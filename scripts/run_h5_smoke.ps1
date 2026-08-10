# H5 1-instance official SWE-bench smoke.
#
# Reads the DeepSeek official key from the local notes file into this process's
# environment ONLY: never echoed, never written to disk, never into artifacts.
# The driver (eval/driver_headless.py) reads LOCAL_LLM_API_KEY, so that is the
# variable that must be set -- exporting only DEEPSEEK_API_KEY would silently
# fall back to the literal "ollama".
#
# Exit code is captured to <out>/run.rc so the verdict is read from the process
# itself rather than from parsed stdout.

param(
    [string]$OutDir = "",
    [string]$Model  = "deepseek-v4-pro",
    [string]$BaseUrl = "https://api.deepseek.com/v1"
)

$ErrorActionPreference = "Stop"
Set-Location -LiteralPath "D:\vscode\localcode"

if ([string]::IsNullOrWhiteSpace($OutDir)) {
    $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
    $OutDir = "eval_results\h5-smoke-$stamp"
}
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

# ---- key: label-anchored extraction, masked reporting only ----
$keyFile = 'D:\Obsidian\code-autogrowth\项目进展\api-key.md'
$lines = Get-Content -LiteralPath $keyFile -Encoding UTF8
$idx = -1
for ($i = 0; $i -lt $lines.Count; $i++) {
    if ($lines[$i] -match 'deepseek') { $idx = $i; break }
}
if ($idx -lt 0) { Write-Output "FATAL: deepseek label not found"; exit 2 }
$key = $null
for ($j = $idx; $j -lt [Math]::Min($idx + 3, $lines.Count); $j++) {
    $m = [regex]::Match($lines[$j], 'sk-[A-Za-z0-9_\-]{20,}')
    if ($m.Success) { $key = $m.Value; break }
}
if (-not $key) { Write-Output "FATAL: deepseek key not found"; exit 2 }
Write-Output ("[h5] key loaded: length=" + $key.Length + " prefix=" + $key.Substring(0,6))

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

& python -m eval.run -b swebench -m $Model --smoke --base-url $BaseUrl -o $OutDir 2>&1
$rc = $LASTEXITCODE

Set-Content -LiteralPath (Join-Path $OutDir "run.rc") -Value $rc -Encoding UTF8
Write-Output ("[h5] finished = " + (Get-Date -Format o))
Write-Output ("[h5] exit_code = " + $rc)
exit $rc
