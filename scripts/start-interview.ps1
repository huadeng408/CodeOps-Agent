param(
    [string]$ProviderConfig = (Join-Path $env:USERPROFILE ("Desktop\api$([char]0x5bc6)$([char]0x94a5).txt")),
    [string]$ProviderProfile = '',
    [switch]$RestartOrchestrator,
    [int]$DockerTimeoutSeconds = 120
)

$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root
. "$PSScriptRoot\rag-agent-e2e-runtime.ps1"

# Configuration is inherited by child processes, never written into launch logs.
foreach ($file in @('.env', '.env.local')) {
    if (!(Test-Path -LiteralPath $file)) { continue }
    foreach ($line in Get-Content -LiteralPath $file) {
        if ($line -match '^\s*([A-Za-z_][A-Za-z_0-9]*)\s*=\s*(.*)$') {
            [Environment]::SetEnvironmentVariable($Matches[1], $Matches[2].Trim().Trim('"').Trim("'"), 'Process')
        }
    }
}
# The Go Harness and Python orchestrator must share one per-launch secret for
# continuation callbacks (including SpawnAgent/worktree lifecycle events).
# Generate it only in this process tree when the caller did not provide one;
# never persist or print the value.
if ([string]::IsNullOrWhiteSpace($env:ORCHESTRATOR_SHARED_SECRET)) {
    $env:ORCHESTRATOR_SHARED_SECRET = [guid]::NewGuid().ToString('N')
}
# Direct interview launches must opt the Python runner into the Harness
# SpawnAgent/worktree callback contract (ProcessManager normally injects this
# for managed launches). Keep it process-scoped and never persist the value.
$env:CODE_AGENT_REQUIRE_HARNESS_WORKTREE = '1'
if ($ProviderConfig) {
    $env:CODE_AGENT_PROVIDER_CONFIG = (Resolve-Path -LiteralPath $ProviderConfig).Path
    $env:CODE_AGENT_PROVIDER_PROFILE = $ProviderProfile
    & python -m orchestrator.config.provider_file $env:CODE_AGENT_PROVIDER_CONFIG --profile="$ProviderProfile"
    if ($LASTEXITCODE -ne 0) { throw 'Provider configuration validation failed' }
}
$env:CODE_AGENT_SERVER_PORT = '8081'
$env:CODE_AGENT_ORCHESTRATOR_ADDR = '127.0.0.1:50051'

function Listening([int]$Port) {
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $task = $client.ConnectAsync('127.0.0.1', $Port)
        return ($task.Wait(1000) -and $client.Connected)
    } catch { return $false }
    finally { $client.Dispose() }
}

Wait-DockerDaemonReady -TimeoutSeconds $DockerTimeoutSeconds
& docker start codeagent-mysql codeagent-redis codeagent-minio
if ($LASTEXITCODE -ne 0) { throw 'Required project containers could not start' }
$deadline = (Get-Date).AddSeconds(60)
while (!(Listening 3306) -or !(Listening 6379)) {
    if ((Get-Date) -ge $deadline) { throw 'Database ports did not become ready' }
    Start-Sleep -Seconds 1
}
New-Item -ItemType Directory -Force '.tmp' | Out-Null
if ($RestartOrchestrator -and (Listening 50051)) {
    $listener = Get-NetTCPConnection -LocalPort 50051 -State Listen -ErrorAction Stop | Select-Object -First 1
    $process = Get-CimInstance Win32_Process -Filter "ProcessId=$($listener.OwningProcess)"
    if ($process.Name -notmatch '^python(?:w)?(?:3(?:\.\d+)?)?\.exe$' -or $process.CommandLine -notmatch '-m\s+orchestrator\.server(?:\s|$)') {
        throw 'Port 50051 does not belong to the expected Python orchestrator; refusing to stop it'
    }
    Stop-Process -Id $listener.OwningProcess -ErrorAction Stop
    Wait-Process -Id $listener.OwningProcess -Timeout 10 -ErrorAction SilentlyContinue
}
if (!(Listening 50051)) {
    Start-Process python -ArgumentList '-m','orchestrator.server' -WorkingDirectory $root -WindowStyle Hidden -RedirectStandardOutput '.tmp/interview-orchestrator.out.log' -RedirectStandardError '.tmp/interview-orchestrator.err.log' | Out-Null
}
if (!(Listening 8081)) {
    & go build -o .tmp/interview-server.exe ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw 'Server build failed' }
    Start-Process '.tmp/interview-server.exe' -WorkingDirectory $root -WindowStyle Hidden -RedirectStandardOutput '.tmp/interview-server.out.log' -RedirectStandardError '.tmp/interview-server.err.log' | Out-Null
}
if (!(Listening 3000)) {
    Start-Process cmd.exe -ArgumentList '/c','npm --prefix frontend run dev -- --host 127.0.0.1' -WorkingDirectory $root -WindowStyle Hidden -RedirectStandardOutput '.tmp/interview-frontend.out.log' -RedirectStandardError '.tmp/interview-frontend.err.log' | Out-Null
}
$deadline = (Get-Date).AddSeconds(60)
do {
    try {
        $health = Invoke-RestMethod 'http://127.0.0.1:8081/healthz' -TimeoutSec 3
        if ($health.continuation.attached -and (Listening 50051) -and (Listening 3000)) {
            Write-Output 'Workbench ready: http://127.0.0.1:3000/'
            return
        }
    } catch { }
    Start-Sleep -Seconds 1
} while ((Get-Date) -lt $deadline)
throw 'Workbench did not become ready; inspect local .tmp/interview-*.log files'
