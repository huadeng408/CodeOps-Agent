[CmdletBinding()]
param(
    [string]$OutputDir = "eval_results/o3",
    [string]$ServerUrl = "http://127.0.0.1:8081",
    [string]$PhoenixUrl = "http://127.0.0.1:6006",
    [int]$StartupTimeoutSeconds = 120,
    [string]$Python = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

# This is deliberately a one-receipt runner. Credentials must already be in
# the caller's environment and remain process-scoped for the short-lived Go
# and Python children.
$repoRoot = Split-Path -Parent $PSScriptRoot
$endpointHelper = Join-Path $PSScriptRoot "lib\receipt-endpoints.ps1"
$redactionHelper = Join-Path $PSScriptRoot "lib\credential-redaction.ps1"
. $endpointHelper
$serverEndpoint = Resolve-LoopbackHttpEndpoint -Url $ServerUrl
$ServerUrl = $serverEndpoint.Url
$serverPort = [int]$serverEndpoint.Port
$pythonCommand = if ([string]::IsNullOrWhiteSpace($Python)) {
    (Get-Command python -ErrorAction Stop).Source
} else {
    $Python
}
$serverProcess = $null
$runToken = [guid]::NewGuid().ToString("N")
$serverExe = Join-Path $repoRoot ".tmp\o3-receipt-server-$runToken.exe"
$serverRunner = Join-Path $repoRoot ".tmp\o3-receipt-server-$runToken.ps1"
$receiptTempRoot = [IO.Path]::GetTempPath()
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    foreach ($candidate in @($env:TMPDIR, $env:TEMP, $env:TMP)) {
        if (-not [string]::IsNullOrWhiteSpace($candidate) -and
            (Test-Path -LiteralPath $candidate -PathType Container)) {
            $receiptTempRoot = $candidate
            break
        }
    }
}
$serverOut = Join-Path $receiptTempRoot "codeagent-o3-server.$runToken.stdout.log"
$serverErr = Join-Path $receiptTempRoot "codeagent-o3-server.$runToken.stderr.log"
$powerShellCommand = [System.Diagnostics.Process]::GetCurrentProcess().MainModule.FileName
$trackedNames = @(
    "LOCAL_LLM_BASE_URL", "LOCAL_LLM_API_KEY", "LOCAL_LLM_MODEL",
    "CODE_AGENT_RAG_SERVER_URL", "CODE_AGENT_RAG_INTERNAL_SECRET",
    "CODE_AGENT_RAG_USER_ID", "ORCHESTRATOR_SHARED_SECRET",
    "CODE_AGENT_RAG_TIMEOUT_SECONDS",
    "CODE_AGENT_SERVER_PORT", "CODE_AGENT_O3_SERVER_EXE",
    "CODE_AGENT_O3_SERVER_WORKDIR", "CODE_AGENT_REDACTION_HELPER",
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

function Test-LocalPortListening {
    [CmdletBinding()]
    param([Parameter(Mandatory = $true)][int]$Port)

    # ``Get-NetTCPConnection`` is only shipped with the Windows networking
    # module.  The receipt runner also executes under PowerShell 7 on Linux
    # CI, so use the cross-platform .NET socket table first and retain the
    # cmdlet as a Windows fallback for older hosts.
    try {
        $listeners = [System.Net.NetworkInformation.IPGlobalProperties]::GetIPGlobalProperties().GetActiveTcpListeners()
        if (@($listeners | Where-Object { $_.Port -eq $Port }).Count -gt 0) {
            return $true
        }
        return $false
    }
    catch {
        $netTcp = Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue
        if ($null -eq $netTcp) {
            return $false
        }
        return @(
            Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
        ).Count -gt 0
    }
}

function Stop-ProcessTree {
    param([System.Diagnostics.Process]$Process)

    if ($null -eq $Process -or $Process.HasExited) {
        return
    }
    if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
        # Start-Process does not create a process group on POSIX. Kill direct
        # descendants first so the wrapper's merged stdout/stderr pipeline
        # observes EOF and flushes the final redacted records before it exits.
        $psPath = if (Test-Path -LiteralPath '/bin/ps') { '/bin/ps' } else { 'ps' }
        $killPath = if (Test-Path -LiteralPath '/bin/kill') { '/bin/kill' } else { 'kill' }
        $rows = @(& $psPath -eo 'pid=,ppid=' 2>$null)
        $children = @{}
        foreach ($row in $rows) {
            $parts = ([string]$row).Trim() -split '\s+'
            if ($parts.Count -lt 2) { continue }
            $childPid = 0
            $parentPid = 0
            if ([int]::TryParse($parts[0], [ref]$childPid) -and
                [int]::TryParse($parts[1], [ref]$parentPid)) {
                if (-not $children.ContainsKey($parentPid)) { $children[$parentPid] = @() }
                $children[$parentPid] += $childPid
            }
        }
        $pending = [System.Collections.Generic.Queue[int]]::new()
        $seen = [System.Collections.Generic.HashSet[int]]::new()
        $pending.Enqueue($Process.Id)
        while ($pending.Count -gt 0) {
            $parentPid = $pending.Dequeue()
            if (-not $children.ContainsKey($parentPid)) { continue }
            foreach ($childPid in $children[$parentPid]) {
                if ($seen.Add($childPid)) { $pending.Enqueue($childPid) }
            }
        }
        foreach ($childPid in @($seen)) {
            & $killPath -KILL $childPid 2>$null | Out-Null
        }
        # The wrapper owns the redaction pipeline. Give it a short window to
        # observe child EOF and flush captured records before terminating it.
        try {
            if ($Process.WaitForExit(1000)) { return }
        }
        catch { }
    }
    if ([System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT -and
        $null -ne (Get-Command taskkill.exe -ErrorAction SilentlyContinue)) {
        & taskkill.exe /PID $Process.Id /T /F 2>$null | Out-Null
    }
    else {
        try { $Process.Kill() } catch { }
    }
    try { $Process.WaitForExit(10000) | Out-Null } catch { }
}

function Protect-ReceiptLogFile {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [AllowEmptyCollection()][string[]]$Secrets = @()
    )

    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        return
    }
    $raw = [IO.File]::ReadAllText($Path)
    $protected = Protect-CredentialText -Text $raw -Secrets $Secrets
    [IO.File]::WriteAllText($Path, $protected, [Text.UTF8Encoding]::new($false))
}

Push-Location $repoRoot
try {
    if ([string]::IsNullOrWhiteSpace($env:LOCAL_LLM_API_KEY)) {
        throw "LOCAL_LLM_API_KEY is not configured."
    }
    $key = $env:LOCAL_LLM_API_KEY
    $secret = New-InternalSecret
    if ([string]::IsNullOrWhiteSpace($env:LOCAL_LLM_BASE_URL)) {
        $env:LOCAL_LLM_BASE_URL = "https://beeapi.ai/v1"
    }
    $env:LOCAL_LLM_API_KEY = $key
    $env:LOCAL_LLM_MODEL = "gpt-5.6-sol"
    $env:CODE_AGENT_RAG_SERVER_URL = $ServerUrl
    $env:CODE_AGENT_RAG_INTERNAL_SECRET = $secret
    $env:CODE_AGENT_RAG_USER_ID = "1"
    $env:CODE_AGENT_RAG_TIMEOUT_SECONDS = "120"
    $env:CODE_AGENT_SERVER_PORT = [string]$serverPort
    $env:ORCHESTRATOR_SHARED_SECRET = $secret
    $env:CODE_AGENT_STRICT_TRACE_PINS = "true"
    $env:PHOENIX_URL = $PhoenixUrl
    # The Go OTLP/HTTP exporter appends /v1/traces itself; Python's exporter
    # receives its full endpoint through the harness, so these settings remain
    # intentionally process-scoped and producer-specific.
    $env:OTEL_EXPORTER_OTLP_ENDPOINT = $PhoenixUrl.TrimEnd('/')
    $env:OTEL_SERVICE_NAME = "code-agent-o3-rag"

    if (Test-LocalPortListening -Port $serverPort) {
        throw "refusing to reuse an existing process on :$serverPort"
    }
    New-Item -ItemType Directory -Path (Split-Path -Parent $serverExe) -Force | Out-Null
    . $redactionHelper
    $buildExitCode = 0
    Invoke-RedactedNativeCommand `
        -FilePath "go" `
        -ArgumentList @("build", "-o", $serverExe, "./cmd/server") `
        -Secrets @($key, $secret) `
        -ExitCode ([ref]$buildExitCode)
    if ($buildExitCode -ne 0) { throw "Go RAG server build failed" }

    $env:CODE_AGENT_O3_SERVER_EXE = $serverExe
    $env:CODE_AGENT_O3_SERVER_WORKDIR = $repoRoot
    $env:CODE_AGENT_REDACTION_HELPER = $redactionHelper
    $onWindows = [System.Environment]::OSVersion.Platform -eq [System.PlatformID]::Win32NT
    if ($onWindows) {
        @'
$ErrorActionPreference = "Stop"
. $env:CODE_AGENT_REDACTION_HELPER
Push-Location $env:CODE_AGENT_O3_SERVER_WORKDIR
try {
    $exitCode = 0
    Invoke-RedactedNativeCommand `
        -FilePath $env:CODE_AGENT_O3_SERVER_EXE `
        -Secrets @(
            $env:LOCAL_LLM_API_KEY,
            $env:CODE_AGENT_RAG_INTERNAL_SECRET,
            $env:ORCHESTRATOR_SHARED_SECRET
        ) `
        -ExitCode ([ref]$exitCode)
    exit $exitCode
}
finally {
    Pop-Location
}
'@ | Set-Content -LiteralPath $serverRunner -Encoding utf8
        $startServer = @{
            FilePath = $powerShellCommand
            WorkingDirectory = $repoRoot
            ArgumentList = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", $serverRunner)
            RedirectStandardOutput = $serverOut
            RedirectStandardError = $serverErr
            PassThru = $true
            WindowStyle = "Hidden"
        }
    }
    else {
        # POSIX PowerShell does not reliably carry a nested pipeline through
        # Start-Process redirection. Start the Go child directly, then redact
        # its bounded log files after shutdown in the parent runner.
        $startServer = @{
            FilePath = $serverExe
            WorkingDirectory = $repoRoot
            RedirectStandardOutput = $serverOut
            RedirectStandardError = $serverErr
            PassThru = $true
        }
    }
    $serverProcess = Start-Process @startServer
    Wait-ForHealth -Url "$($ServerUrl.TrimEnd('/'))/healthz" -TimeoutSeconds $StartupTimeoutSeconds

    $evalExitCode = 0
    Invoke-RedactedNativeCommand `
        -FilePath $pythonCommand `
        -ArgumentList @("-m", "eval.run_o3", "--execute", "--output-dir", $OutputDir) `
        -Secrets @($key, $secret) `
        -ExitCode ([ref]$evalExitCode)
    exit $evalExitCode
}
finally {
    if ($null -ne $serverProcess) {
        Stop-ProcessTree $serverProcess
        $serverProcess.Dispose()
    }
    Protect-ReceiptLogFile -Path $serverOut -Secrets @($key, $secret)
    Protect-ReceiptLogFile -Path $serverErr -Secrets @($key, $secret)
    Remove-Item -LiteralPath $serverExe, $serverRunner -Force -ErrorAction SilentlyContinue
    Restore-Environment
    Pop-Location
}
