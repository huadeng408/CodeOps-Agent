# switch-alias.ps1 -- ONE atomic ES aliases API call (plan Task 9.2).
#
# Moves -Alias from the -Previous target(s) to -Target using a single
# POST /_aliases call with remove + add actions in the same body. The
# operation is atomic: either the whole body applies or nothing does.
#
# This script NEVER issues a DELETE and never removes physical indices: the
# previous indices stay in place as the rollback source (the inverse of this
# call is scripts/rag/rollback-alias.ps1).
#
# The full request body and the full response are printed for evidence. Use
# Tee-Object to save them (see docs/releases/RAG-CUTOVER-techdocs-v2.md
# sections 3 and 4).
#
# Usage (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts/rag/switch-alias.ps1 `
#       -Alias knowledge_base_current -Target knowledge_base_v2_bge_m3 `
#       -Previous @("knowledge_base")
#
# Drill on a test alias (no previous target -- use -NoPrevious so the empty
# target list survives invocation through `powershell -File`):
#   powershell -ExecutionPolicy Bypass -File scripts/rag/switch-alias.ps1 `
#       -Alias knowledge_base_cutover_drill -Target knowledge_base_v2_bge_m3 `
#       -NoPrevious
#
# Exit code: 0 when the switch applied and the read-back confirms -Target,
# 1 otherwise.

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Alias,
    [Parameter(Mandatory = $true)][string]$Target,
    [string[]]$Previous = @("knowledge_base"),
    [switch]$NoPrevious,
    [string]$EsBaseUrl = "http://127.0.0.1:9200",
    [int]$HttpTimeoutSeconds = 10
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

# Normalize: -Previous '' (or an empty array) means "no previous targets",
# e.g. when pointing a fresh test alias at an index. Prefer the explicit
# -NoPrevious switch: an empty string argument is dropped when the script is
# invoked through `powershell -File` from a PowerShell console.
$Previous = @($Previous | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
if ($NoPrevious) { $Previous = @() }

Write-Host "=== ES ALIAS SWITCH (single atomic call) ==="
Write-Host "  alias   : $Alias"
Write-Host "  target  : $Target"
Write-Host "  previous: $($Previous -join ', ')"
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
    Write-Host "current alias state: $Alias does not exist yet (fresh alias)."
}
elseif ($readCode -ge 200 -and $readCode -lt 300) {
    Write-Host "current alias state:"
    Write-Host $readBody
}
else {
    Write-Host "current alias state: HTTP $readCode (non-fatal; proceeding with switch)"
    Write-Host $readBody
}
Write-Host ""

# --- build the ONE atomic body: remove previous + add target ------------------
# NOTE: loop variables must not collide with parameter names -- PowerShell
# variables are case-insensitive, so `foreach ($previous in $Previous)` would
# mutate the collection mid-iteration and serialize index as an array.
$actions = New-Object System.Collections.ArrayList
foreach ($previousIndex in $Previous) {
    $null = $actions.Add(@{ "remove" = @{ "index" = $previousIndex; "alias" = $Alias } })
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
    Write-Host "SWITCH FAILED: HTTP $httpCode. Nothing was applied (atomic call)."
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
    Write-Host "SWITCH VERIFY FAILED: HTTP $verifyCode reading $Alias."
    exit 1
}
$parsed = $verifyBody | ConvertFrom-Json
$actualTargets = @($parsed.PSObject.Properties.Name | Sort-Object)
# Strict verification: the target must be attached AND every previous target
# must have been detached (a failed remove silently leaves the old target in
# place -- treat that as a failed switch).
$staleRemaining = @($actualTargets | Where-Object { $Previous -contains $_ })
if ($actualTargets -contains $Target -and $staleRemaining.Count -eq 0) {
    Write-Host "VERIFIED: $Alias now points at $($actualTargets -join ', ')"
    Write-Host "SWITCH OK."
    exit 0
}
Write-Host "VERIFY FAILED: $Alias points at $($actualTargets -join ', '), target=$Target missing=$(-not ($actualTargets -contains $Target)) stalePrevious=$($staleRemaining -join ', ')"
exit 1
