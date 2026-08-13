[CmdletBinding()]
param(
    [string]$OutputDir = "eval_results/o3",
    [string]$ServerUrl = "http://127.0.0.1:8081",
    [string]$PhoenixUrl = "http://127.0.0.1:6006",
    [int]$StartupTimeoutSeconds = 120
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

# This is deliberately a one-receipt runner.  Values read here remain only in
# the process environment inherited by the short-lived Go and Python children.
$repoRoot = Split-Path -Parent $PSScriptRoot
# Build the non-ASCII folder name from code points so this runner works even
# when a Windows host invokes it with an incompatible script code page.
$progressFolder = "{0}{1}{2}{3}" -f [char]0x9879, [char]0x76EE, [char]0x8FDB, [char]0x5C55
$keyFile = Join-Path "D:\Obsidian\code-autogrowth" (Join-Path $progressFolder "api-key.md")
$serverProcess = $null
$serverExe = Join-Path $repoRoot ".tmp\o3-receipt-server.exe"
$serverOut = Join-Path ([IO.Path]::GetTempPath()) "codeagent-o3-server.stdout.log"
$serverErr = Join-Path ([IO.Path]::GetTempPath()) "codeagent-o3-server.stderr.log"
$trackedNames = @(
    "LOCAL_LLM_BASE_URL", "LOCAL_LLM_API_KEY", "LOCAL_LLM_MODEL",
    "CODE_AGENT_RAG_SERVER_URL", "CODE_AGENT_RAG_INTERNAL_SECRET",
    "CODE_AGENT_RAG_USER_ID", "ORCHESTRATOR_SHARED_SECRET",
    "CODE_AGENT_RAG_TIMEOUT_SECONDS",
    "CODE_AGENT_STRICT_TRACE_PINS", "PHOENIX_URL",
    "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_SERVICE_NAME"
)
$previous = @{}
foreach ($name in $trackedNames) {
    $item = Get-Item -LiteralPath ("Env:{0}" -f $name) -ErrorAction SilentlyContinue
    $previous[$name] = if ($null -ne $item) { $item.Value } else { $null }
}

function Restore-Environment {
    foreach ($name in $trackedNames) {
        if ($null -eq $previous[$name]) {
            Remove-Item -LiteralPath ("Env:{0}" -f $name) -ErrorAction SilentlyContinue
        }
        else {
            Set-Item -LiteralPath ("Env:{0}" -f $name) -Value $previous[$name]
        }
    }
}

function Get-BeeApiKey {
    $lines = Get-Content -LiteralPath $keyFile -Encoding UTF8
    $beeIndex = -1
    for ($i = 0; $i -lt $lines.Count; $i++) {
        if ($lines[$i] -match "(?i)^\s*beeapi\b") { $beeIndex = $i; break }
    }
    if ($beeIndex -lt 0) { throw "BeeAPI credential label was not found" }
    for ($i = $beeIndex + 1; $i -lt [Math]::Min($beeIndex + 4, $lines.Count); $i++) {
        $candidate = $lines[$i].Trim()
        if ($candidate -match "^[A-Za-z0-9_\-]{20,}$") { return $candidate }
    }
    throw "BeeAPI credential was not found below its label"
}

function New-InternalSecret {
    $bytes = New-Object byte[] 32
    $rng = New-Object System.Security.Cryptography.RNGCryptoServiceProvider
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return [Convert]::ToBase64String($bytes)
}

function Wait-ForHealth {
    param([string]$Url, [int]$TimeoutSeconds)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $Url -TimeoutSec 5
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300) { return }
        }
        catch { }
        Start-Sleep -Seconds 2
    }
    throw "timed out waiting for short-lived O3 RAG server"
}

Push-Location $repoRoot
try {
    $key = Get-BeeApiKey
    $secret = New-InternalSecret
    $env:LOCAL_LLM_BASE_URL = "https://beeapi.ai/v1"
    $env:LOCAL_LLM_API_KEY = $key
    $env:LOCAL_LLM_MODEL = "gpt-5.6-sol"
    $env:CODE_AGENT_RAG_SERVER_URL = $ServerUrl
    $env:CODE_AGENT_RAG_INTERNAL_SECRET = $secret
    $env:CODE_AGENT_RAG_USER_ID = "1"
    $env:CODE_AGENT_RAG_TIMEOUT_SECONDS = "120"
    $env:ORCHESTRATOR_SHARED_SECRET = $secret
    $env:CODE_AGENT_STRICT_TRACE_PINS = "true"
    $env:PHOENIX_URL = $PhoenixUrl
    # The Go OTLP/HTTP exporter appends /v1/traces itself; Python's exporter
    # receives its full endpoint through the harness, so these settings remain
    # intentionally process-scoped and producer-specific.
    $env:OTEL_EXPORTER_OTLP_ENDPOINT = $PhoenixUrl.TrimEnd('/')
    $env:OTEL_SERVICE_NAME = "code-agent-o3-rag"

    if (Get-NetTCPConnection -LocalPort 8081 -State Listen -ErrorAction SilentlyContinue) {
        throw "refusing to reuse an existing process on :8081"
    }
    & go build -o $serverExe ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw "Go RAG server build failed" }
    $serverProcess = Start-Process -FilePath $serverExe -WorkingDirectory $repoRoot `
        -RedirectStandardOutput $serverOut -RedirectStandardError $serverErr -WindowStyle Hidden -PassThru
    Wait-ForHealth -Url "$($ServerUrl.TrimEnd('/'))/healthz" -TimeoutSeconds $StartupTimeoutSeconds

    & C:\Python312\python.exe -m eval.run_o3 --execute --output-dir $OutputDir
    exit $LASTEXITCODE
}
finally {
    if ($null -ne $serverProcess) {
        Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue
        try { $serverProcess.WaitForExit() } catch { }
        $serverProcess.Dispose()
    }
    Remove-Item -LiteralPath $serverExe -Force -ErrorAction SilentlyContinue
    Restore-Environment
    Pop-Location
}
