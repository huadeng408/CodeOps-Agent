# rollback-alias.ps1 -- inverse of scripts/rag/switch-alias.ps1 (plan Task 9.2).
#
# Restores -Alias to the -Target (previous) index by removing it from the
# -Current target(s) and re-adding it to -Target in ONE atomic POST /_aliases
# call (remove + add in the same body). This is the exact inverse of a prior
# switch: after a switch to the v2 index, run this with
#   -Target <previous index> -Current @(<v2 index>).
#
# This script NEVER issues a DELETE and never removes physical indices:
# neither the v2 index nor the legacy index is ever deleted -- rollback is an
# alias-only operation. Both indices remain as data.
#
# The full request body and the full response are printed for evidence. Use
# Tee-Object to save them (see docs/releases/RAG-CUTOVER-techdocs-v2.md
# section 5).
#
# Usage (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts/rag/rollback-alias.ps1 `
#       -Alias knowledge_base_current -Target knowledge_base `
#       -Current @("knowledge_base_v2_bge_m3")
#
# Exit code: 0 when the rollback applied and the read-back confirms -Target,
# 1 otherwise.

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Alias,
    [Parameter(Mandatory = $true)][string]$Target,
    [string[]]$Current = @("knowledge_base_v2_bge_m3"),
    [string]$EsBaseUrl = "http://127.0.0.1:9200",
    [int]$HttpTimeoutSeconds = 10
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

# Normalize: -Current '' (or an empty array) means "no current targets to
# remove", e.g. when rolling back a fresh test alias. When invoked through
# `powershell -File`, @() cannot be passed; an empty string is the portable
# spelling.
$Current = @($Current | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })

Write-Host "=== ES ALIAS ROLLBACK (single atomic call, inverse of switch) ==="
Write-Host "  alias   : $Alias"
Write-Host "  target  : $Target"
Write-Host "  current : $($Current -join ', ')"
Write-Host "  es      : $EsBaseUrl"
Write-Host ""

# --- evidence: current alias state (read-only) --------------------------------
$readBack = & curl.exe -s -w "`n%{http_code}" -X GET "$EsBaseUrl/_alias/$Alias" --max-time $HttpTimeoutSeconds
if ($LASTEXITCODE -ne 0) {
    Write-Error "could not read current alias state (curl exit code $LASTEXITCODE)"
    exit 1
}
$lines = $readBack -split "`n"
$readCode = [int]$lines[-1]
$readBody = ($lines[0..($lines.Count - 2)] -join "`n")
if ($readCode -eq 404) {
    Write-Host "current alias state: $Alias does not exist (nothing to roll back; exiting OK)."
    exit 0
}
elseif ($readCode -ge 200 -and $readCode -lt 300) {
    Write-Host "current alias state:"
    Write-Host $readBody
}
else {
    Write-Host "current alias state: HTTP $readCode (non-fatal; proceeding with rollback)"
    Write-Host $readBody
}
Write-Host ""

# --- build the ONE atomic body: remove current + add target -------------------
# NOTE: loop variables must not collide with parameter names -- PowerShell
# variables are case-insensitive, so `foreach ($current in $Current)` would
# mutate the collection mid-iteration and serialize index as an array.
$actions = New-Object System.Collections.ArrayList
foreach ($currentIndex in $Current) {
    $null = $actions.Add(@{ "remove" = @{ "index" = $currentIndex; "alias" = $Alias } })
}
$null = $actions.Add(@{ "add" = @{ "index" = $Target; "alias" = $Alias } })
$requestBody = @{ "actions" = $actions } | ConvertTo-Json -Depth 6

Write-Host "=== REQUEST ==="
Write-Host "POST $EsBaseUrl/_aliases"
Write-Host "Content-Type: application/json"
Write-Host $requestBody
Write-Host ""

# --- send it -------------------------------------------------------------------
# The body is written to a temp file and sent with --data-binary @file:
# PowerShell 5.1 does not escape embedded double quotes in native command
# arguments, which would corrupt the JSON when passed via -d "$body".
$tmpDir = Join-Path ([IO.Path]::GetTempPath()) ("rag-alias-" + [Guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null
$bodyFile = Join-Path $tmpDir "request.json"
$respFile = Join-Path $tmpDir "response.txt"
[IO.File]::WriteAllText($bodyFile, $requestBody)
$httpCode = (& curl.exe -s -o $respFile -w "%{http_code}" -X POST "$EsBaseUrl/_aliases" -H "Content-Type: application/json" --data-binary "@$bodyFile" --max-time $HttpTimeoutSeconds)
$curlExit = $LASTEXITCODE
$responseBody = if (Test-Path -LiteralPath $respFile) { [IO.File]::ReadAllText($respFile) } else { "" }
Remove-Item -LiteralPath $tmpDir -Recurse -Force

if ($curlExit -ne 0) {
    Write-Error "aliases API call failed (curl exit code $curlExit)"
    Write-Host "=== RESPONSE ==="
    Write-Host $responseBody
    exit 1
}

Write-Host "=== RESPONSE ==="
Write-Host "HTTP $httpCode"
Write-Host $responseBody
Write-Host ""

if ($httpCode -lt 200 -or $httpCode -ge 300) {
    Write-Host "ROLLBACK FAILED: HTTP $httpCode. Nothing was applied (atomic call)."
    exit 1
}

# --- verify by read-back ---------------------------------------------------------
$verify = & curl.exe -s -w "`n%{http_code}" -X GET "$EsBaseUrl/_alias/$Alias" --max-time $HttpTimeoutSeconds
if ($LASTEXITCODE -ne 0) {
    Write-Error "verification read-back failed (curl exit code $LASTEXITCODE)"
    exit 1
}
$lines = $verify -split "`n"
$verifyCode = [int]$lines[-1]
$verifyBody = ($lines[0..($lines.Count - 2)] -join "`n")
if ($verifyCode -lt 200 -or $verifyCode -ge 300) {
    Write-Host "ROLLBACK VERIFY FAILED: HTTP $verifyCode reading $Alias."
    exit 1
}
$parsed = $verifyBody | ConvertFrom-Json
$actualTargets = @($parsed.PSObject.Properties.Name | Sort-Object)
# Strict verification: the target must be restored AND every current target
# must have been detached (a failed remove silently leaves v2 in place --
# treat that as a failed rollback).
$staleRemaining = @($actualTargets | Where-Object { $Current -contains $_ })
if ($actualTargets -contains $Target -and $staleRemaining.Count -eq 0) {
    Write-Host "VERIFIED: $Alias now points at $($actualTargets -join ', ')"
    Write-Host "ROLLBACK OK."
    exit 0
}
Write-Host "VERIFY FAILED: $Alias points at $($actualTargets -join ', '), target=$Target missing=$(-not ($actualTargets -contains $Target)) staleCurrent=$($staleRemaining -join ', ')"
exit 1
