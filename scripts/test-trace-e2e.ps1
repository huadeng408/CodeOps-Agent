#Requires -Version 5.1

<#
.SYNOPSIS
Runs the explicit Go-to-Python Phoenix trace integration test.

.DESCRIPTION
Starts Docker Phoenix when needed, calls the real DeepSeek OpenAI-compatible
API, runs the real Go agent and Python orchestrator, and verifies their spans
through the Phoenix REST API. This script is intentionally excluded from the
default Go and Python test suites.

.EXAMPLE
$env:OPENAI_API_KEY = '<injected by an approved secret manager>'
powershell -NoProfile -File scripts/test-trace-e2e.ps1 -Model deepseek-v4-pro
#>

[CmdletBinding()]
param(
    [string]$Model = 'deepseek-v4-pro',
    [string]$OpenAIBaseUrl = 'https://api.deepseek.com',
    [string]$PhoenixUrl = 'http://127.0.0.1:6006',
    [string]$PhoenixProject = 'default',
    [int]$TurnTimeoutSeconds = 120,
    [int]$TraceTimeoutSeconds = 45,
    [int]$PhoenixStartupTimeoutSeconds = 300,
    [switch]$ValidateApiKeyOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$endpointHelper = Join-Path $PSScriptRoot 'lib\receipt-endpoints.ps1'
. $endpointHelper
$otlpTraceEndpoint = Resolve-OtlpHttpTraceEndpoint -PhoenixUrl $PhoenixUrl

function Get-ScrubbedText {
    param(
        [AllowEmptyString()][string]$Text,
        [AllowEmptyString()][string]$Secret
    )

    if ([string]::IsNullOrEmpty($Secret)) {
        return $Text
    }
    return $Text.Replace($Secret, '<redacted>')
}

function Get-BoundedText {
    param(
        [AllowEmptyString()][string]$Text,
        [int]$MaxCharacters = 20000
    )

    if ($Text.Length -le $MaxCharacters) {
        return $Text
    }
    return "[earlier output truncated]`n" +
        $Text.Substring($Text.Length - $MaxCharacters)
}

function Get-OpenAIApiKey {
    $fromEnvironment = [Environment]::GetEnvironmentVariable('OPENAI_API_KEY')
    if ([string]::IsNullOrWhiteSpace($fromEnvironment)) {
        throw 'OPENAI_API_KEY is required'
    }
    return $fromEnvironment.Trim()
}

function Assert-Command {
    param([string]$Name)

    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($null -eq $command) {
        throw "required command is unavailable: $Name"
    }
    return $command.Source
}

function Get-FreeTcpPort {
    $listener = [System.Net.Sockets.TcpListener]::new(
        [System.Net.IPAddress]::Loopback,
        0
    )
    try {
        $listener.Start()
        return ([System.Net.IPEndPoint]$listener.LocalEndpoint).Port
    }
    finally {
        $listener.Stop()
    }
}

function ConvertTo-NativeArgument {
    param([AllowEmptyString()][string]$Argument)

    if ($Argument.Length -gt 0 -and $Argument -notmatch '[\s"]') {
        return $Argument
    }

    $builder = [System.Text.StringBuilder]::new()
    $null = $builder.Append('"')
    $backslashes = 0
    foreach ($character in $Argument.ToCharArray()) {
        if ($character -eq '\') {
            $backslashes++
            continue
        }
        if ($character -eq '"') {
            $escaped = ('\' * ($backslashes * 2 + 1)) -join ''
            $null = $builder.Append($escaped)
            $null = $builder.Append('"')
            $backslashes = 0
            continue
        }
        if ($backslashes -gt 0) {
            $literal = ('\' * $backslashes) -join ''
            $null = $builder.Append($literal)
            $backslashes = 0
        }
        $null = $builder.Append($character)
    }
    if ($backslashes -gt 0) {
        $trailing = ('\' * ($backslashes * 2)) -join ''
        $null = $builder.Append($trailing)
    }
    $null = $builder.Append('"')
    return $builder.ToString()
}

function Stop-ProcessTree {
    param([System.Diagnostics.Process]$Process)

    if ($null -eq $Process -or $Process.HasExited) {
        return
    }
    & taskkill.exe /PID $Process.Id /T /F 2>$null | Out-Null
    if (-not $Process.WaitForExit(10000)) {
        $Process.Kill()
        $Process.WaitForExit()
    }
}

function Invoke-CapturedProcess {
    param(
        [Parameter(Mandatory)][string]$FilePath,
        [string[]]$ArgumentList = @(),
        [Parameter(Mandatory)][string]$WorkingDirectory,
        [int]$TimeoutSeconds = 120,
        [hashtable]$Environment = @{},
        [string[]]$RemoveEnvironment = @(),
        [AllowEmptyString()][string]$Secret = ''
    )

    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $FilePath
    $startInfo.WorkingDirectory = $WorkingDirectory
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.Arguments = (
        $ArgumentList | ForEach-Object { ConvertTo-NativeArgument $_ }
    ) -join ' '
    foreach ($name in $RemoveEnvironment) {
        $startInfo.EnvironmentVariables.Remove($name)
    }
    foreach ($entry in $Environment.GetEnumerator()) {
        $startInfo.EnvironmentVariables[$entry.Key] = [string]$entry.Value
    }

    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    try {
        if (-not $process.Start()) {
            throw "failed to start $FilePath"
        }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        $timeoutMilliseconds = [Math]::Max(1, $TimeoutSeconds) * 1000
        if (-not $process.WaitForExit($timeoutMilliseconds)) {
            Stop-ProcessTree $process
            $stdout = $stdoutTask.GetAwaiter().GetResult()
            $stderr = $stderrTask.GetAwaiter().GetResult()
            $detail = Get-BoundedText (Get-ScrubbedText "$stdout`n$stderr" $Secret)
            throw "process timed out after ${TimeoutSeconds}s: $FilePath`n$detail"
        }

        $stdout = $stdoutTask.GetAwaiter().GetResult()
        $stderr = $stderrTask.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0) {
            $detail = Get-BoundedText (Get-ScrubbedText "$stdout`n$stderr" $Secret)
            throw "process exited $($process.ExitCode): $FilePath`n$detail"
        }
        return [pscustomobject]@{
            ExitCode = $process.ExitCode
            Stdout = $stdout
            Stderr = $stderr
        }
    }
    finally {
        $process.Dispose()
    }
}

function Invoke-PythonHelper {
    param(
        [Parameter(Mandatory)][string]$PythonPath,
        [Parameter(Mandatory)][string]$HelperPath,
        [Parameter(Mandatory)][string]$RepositoryRoot,
        [string[]]$Arguments = @(),
        [hashtable]$Environment = @{},
        [string[]]$RemoveEnvironment = @(),
        [int]$TimeoutSeconds = 60,
        [AllowEmptyString()][string]$Secret = ''
    )

    return Invoke-CapturedProcess `
        -FilePath $PythonPath `
        -ArgumentList (@($HelperPath) + $Arguments) `
        -WorkingDirectory $RepositoryRoot `
        -TimeoutSeconds $TimeoutSeconds `
        -Environment $Environment `
        -RemoveEnvironment $RemoveEnvironment `
        -Secret $Secret
}

function Assert-PythonTraceDependencies {
    param(
        [Parameter(Mandatory)][string]$PythonPath,
        [Parameter(Mandatory)][string]$RepositoryRoot
    )

    $probe = @'
import grpc
from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
'@
    try {
        $null = Invoke-CapturedProcess `
            -FilePath $PythonPath `
            -ArgumentList @('-c', $probe) `
            -WorkingDirectory $RepositoryRoot `
            -TimeoutSeconds 15
    }
    catch {
        throw 'Python trace dependencies are unavailable. Install them with: ' +
            'python -m pip install -e ".[trace-e2e]"'
    }
}

function Wait-Phoenix {
    param(
        [string]$BaseUrl,
        [int]$TimeoutSeconds = 60
    )

    $deadline = [DateTimeOffset]::UtcNow.AddSeconds($TimeoutSeconds)
    $uri = "$($BaseUrl.TrimEnd('/'))/v1/projects?limit=1"
    do {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $uri -Method Get -TimeoutSec 5
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300) {
                return
            }
        }
        catch {
            if ([DateTimeOffset]::UtcNow -ge $deadline) {
                throw "Phoenix did not become ready at $uri within ${TimeoutSeconds}s"
            }
        }
        Start-Sleep -Milliseconds 500
    } while ([DateTimeOffset]::UtcNow -lt $deadline)

    throw "Phoenix did not become ready at $uri within ${TimeoutSeconds}s"
}

function Remove-OwnedTempDirectory {
    param([string]$Path)

    if ([string]::IsNullOrWhiteSpace($Path) -or -not (Test-Path -LiteralPath $Path)) {
        return
    }
    $fullPath = [System.IO.Path]::GetFullPath($Path)
    $tempPrefix = [System.IO.Path]::GetFullPath(
        [System.IO.Path]::GetTempPath()
    ).TrimEnd([System.IO.Path]::DirectorySeparatorChar) +
        [System.IO.Path]::DirectorySeparatorChar
    $leaf = [System.IO.Path]::GetFileName($fullPath)
    if (
        -not $fullPath.StartsWith(
            $tempPrefix,
            [System.StringComparison]::OrdinalIgnoreCase
        ) -or
        -not $leaf.StartsWith('code-agent-trace-e2e-')
    ) {
        throw "refusing to remove unowned temporary directory: $fullPath"
    }
    Remove-Item -LiteralPath $fullPath -Recurse -Force
}

$repositoryRoot = [System.IO.Path]::GetFullPath(
    (Join-Path $PSScriptRoot '..')
)
$helperPath = Join-Path $repositoryRoot 'tests\integration\trace_e2e.py'
$pythonPath = ''
$goPath = ''
$dockerPath = ''

$stage = 'preflight'
$apiKey = ''
$tempDirectory = ''
$phoenixStarted = $false
$agentProcess = $null
$agentStderrTask = $null
$pendingStdoutTask = $null
$agentOutput = [System.Collections.Generic.List[string]]::new()
$agentStderr = ''
$failure = $null
$summary = $null
$stopwatch = [System.Diagnostics.Stopwatch]::StartNew()

try {
    $apiKey = Get-OpenAIApiKey
    if ($ValidateApiKeyOnly) {
        Write-Output 'api_key=valid'
        return
    }
    $pythonPath = Assert-Command 'python'
    Assert-PythonTraceDependencies `
        -PythonPath $pythonPath `
        -RepositoryRoot $repositoryRoot
    $goPath = Assert-Command 'go'
    $dockerPath = Assert-Command 'docker'
    $modelCheck = Invoke-PythonHelper `
        -PythonPath $pythonPath `
        -HelperPath $helperPath `
        -RepositoryRoot $repositoryRoot `
        -Arguments @(
            'check-model',
            '--base-url', $OpenAIBaseUrl,
            '--model', $Model
        ) `
        -Environment @{ OPENAI_API_KEY = $apiKey } `
        -TimeoutSeconds 30 `
        -Secret $apiKey
    $null = $modelCheck.Stdout | ConvertFrom-Json

    $stage = 'docker-phoenix'
    $runningServices = Invoke-CapturedProcess `
        -FilePath $dockerPath `
        -ArgumentList @('compose', 'ps', '--status', 'running', '--services') `
        -WorkingDirectory $repositoryRoot `
        -TimeoutSeconds 30
    $phoenixWasRunning = @(
        $runningServices.Stdout -split "`r?`n" |
            ForEach-Object { $_.Trim() } |
            Where-Object { $_ }
    ) -contains 'phoenix'
    if (-not $phoenixWasRunning) {
        $null = Invoke-CapturedProcess `
            -FilePath $dockerPath `
            -ArgumentList @('compose', 'up', '-d', 'phoenix') `
            -WorkingDirectory $repositoryRoot `
            -TimeoutSeconds $PhoenixStartupTimeoutSeconds
        $phoenixStarted = $true
    }
    Wait-Phoenix -BaseUrl $PhoenixUrl -TimeoutSeconds 60

    $stage = 'temporary-workspace'
    $runId = [guid]::NewGuid().ToString('N')
    $tempDirectory = Join-Path `
        ([System.IO.Path]::GetTempPath()) `
        "code-agent-trace-e2e-$runId"
    $settingsDirectory = Join-Path $tempDirectory '.agent'
    $null = New-Item -ItemType Directory -Path $settingsDirectory -Force
    $orchestratorPort = Get-FreeTcpPort
    $settings = @{
        model = $Model
        model_fast = 'disabled'
        orchestrator_addr = "127.0.0.1:$orchestratorPort"
        orchestrator_auto_start = $true
        orchestrator_command = $pythonPath
        orchestrator_args = @('-m', 'orchestrator.server')
        orchestrator_startup_timeout_seconds = 30
        orchestrator_conversation_timeout_seconds = $TurnTimeoutSeconds
        permissions = @{
            allow = @(@{ tool = 'Read'; pattern = '**' })
            deny = @()
        }
    }
    $utf8WithoutBom = [System.Text.UTF8Encoding]::new($false)
    [System.IO.File]::WriteAllText(
        (Join-Path $settingsDirectory 'settings.json'),
        ($settings | ConvertTo-Json -Depth 8),
        $utf8WithoutBom
    )
    $fixtureName = "trace-e2e-$runId.txt"
    $fixtureMarker = "TRACE_E2E_FIXTURE:$runId"
    [System.IO.File]::WriteAllText(
        (Join-Path $tempDirectory $fixtureName),
        $fixtureMarker,
        $utf8WithoutBom
    )

    $stage = 'go-build'
    $agentBinary = Join-Path $tempDirectory 'code-agent-e2e.exe'
    $null = Invoke-CapturedProcess `
        -FilePath $goPath `
        -ArgumentList @('build', '-o', $agentBinary, './cmd/agent') `
        -WorkingDirectory $repositoryRoot `
        -TimeoutSeconds 120

    $stage = 'agent-turn'
    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $agentBinary
    $startInfo.WorkingDirectory = $tempDirectory
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardInput = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $childEnvironment = @{
        LLM_PROVIDER = 'openai'
        OPENAI_API_KEY = $apiKey
        OPENAI_BASE_URL = $OpenAIBaseUrl
        OPENAI_MODEL = $Model
        OPENAI_MAX_RETRIES = '1'
        OPENAI_TIMEOUT = '90'
        THINKING_ENABLED = 'false'
        MODEL_FAST = 'disabled'
        OTEL_SERVICE_NAME = 'code-agent-orchestrator'
        OTEL_EXPORTER_OTLP_ENDPOINT = $otlpTraceEndpoint
        PYTHONPATH = $repositoryRoot
    }
    foreach ($entry in $childEnvironment.GetEnumerator()) {
        $startInfo.EnvironmentVariables[$entry.Key] = [string]$entry.Value
    }

    $agentProcess = [System.Diagnostics.Process]::new()
    $agentProcess.StartInfo = $startInfo
    if (-not $agentProcess.Start()) {
        throw 'failed to start the E2E agent process'
    }
    $agentStderrTask = $agentProcess.StandardError.ReadToEndAsync()
    $pendingStdoutTask = $agentProcess.StandardOutput.ReadLineAsync()
    $turnStart = [DateTimeOffset]::UtcNow.ToString('o')
    $successMarker = "TRACE_E2E_OK:$runId"
    $prompt = @"
Use the Read tool exactly once to read $fixtureName. After receiving the tool result, reply with exactly $successMarker and no other text.
"@.Trim()
    $agentProcess.StandardInput.WriteLine($prompt)
    $agentProcess.StandardInput.Flush()

    $turnDeadline = [DateTimeOffset]::UtcNow.AddSeconds($TurnTimeoutSeconds)
    $markerObserved = $false
    while ([DateTimeOffset]::UtcNow -lt $turnDeadline) {
        if ($pendingStdoutTask.Wait(200)) {
            $line = $pendingStdoutTask.GetAwaiter().GetResult()
            if ($null -eq $line) {
                break
            }
            $agentOutput.Add($line)
            if ($line.Contains($successMarker)) {
                $markerObserved = $true
                break
            }
            $pendingStdoutTask = $agentProcess.StandardOutput.ReadLineAsync()
        }
        if ($agentProcess.HasExited) {
            break
        }
    }
    if (-not $markerObserved) {
        throw "agent did not emit $successMarker within ${TurnTimeoutSeconds}s"
    }

    $stage = 'phoenix-verification'
    $verification = Invoke-PythonHelper `
        -PythonPath $pythonPath `
        -HelperPath $helperPath `
        -RepositoryRoot $repositoryRoot `
        -Arguments @(
            'verify-phoenix',
            '--phoenix-url', $PhoenixUrl,
            '--project', $PhoenixProject,
            '--start-time', $turnStart,
            '--run-id', $runId,
            '--timeout', $TraceTimeoutSeconds.ToString()
        ) `
        -RemoveEnvironment @('OPENAI_API_KEY') `
        -TimeoutSeconds ($TraceTimeoutSeconds + 15)
    $summary = $verification.Stdout | ConvertFrom-Json

    $stage = 'agent-shutdown'
    $agentProcess.StandardInput.Close()
    if (-not $agentProcess.WaitForExit(15000)) {
        throw 'agent did not exit cleanly within 15s after stdin closed'
    }
    $remainingOutput = $agentProcess.StandardOutput.ReadToEnd()
    if (-not [string]::IsNullOrWhiteSpace($remainingOutput)) {
        $agentOutput.Add($remainingOutput)
    }
    $agentStderr = $agentStderrTask.GetAwaiter().GetResult()
    if ($agentProcess.ExitCode -ne 0) {
        throw "agent exited with code $($agentProcess.ExitCode)"
    }
}
catch {
    $failure = $_
}
finally {
    if ($null -ne $agentProcess) {
        try {
            if (-not $agentProcess.StandardInput.BaseStream.CanWrite) {
                # Already closed during normal shutdown.
            }
            else {
                $agentProcess.StandardInput.Close()
            }
        }
        catch {
        }
        try {
            if (-not $agentProcess.HasExited) {
                Stop-ProcessTree $agentProcess
            }
        }
        catch {
        }
        if ($null -ne $agentStderrTask) {
            try {
                $agentStderr = $agentStderrTask.GetAwaiter().GetResult()
            }
            catch {
            }
        }
        $agentProcess.Dispose()
    }

    if (-not [string]::IsNullOrWhiteSpace($tempDirectory)) {
        try {
            Remove-OwnedTempDirectory $tempDirectory
        }
        catch {
            if ($null -eq $failure) {
                $failure = $_
            }
        }
    }

    if ($phoenixStarted) {
        try {
            $null = Invoke-CapturedProcess `
                -FilePath $dockerPath `
                -ArgumentList @('compose', 'stop', 'phoenix') `
                -WorkingDirectory $repositoryRoot `
                -TimeoutSeconds 60
        }
        catch {
            if ($null -eq $failure) {
                $failure = $_
            }
        }
    }
}

$stopwatch.Stop()
if ($null -ne $failure) {
    $stdout = $agentOutput -join "`n"
    $diagnostic = @"
Trace E2E failed during stage: $stage
$($failure.Exception.Message)

Agent stdout:
$stdout

Agent/orchestrator stderr:
$agentStderr
"@
    [Console]::Error.WriteLine((Get-ScrubbedText $diagnostic $apiKey))
    exit 1
}

Write-Host "Trace E2E passed"
Write-Host "run_id: $($summary.run_id)"
Write-Host "trace_id: $($summary.trace_id)"
Write-Host "model: $Model"
Write-Host ("elapsed_seconds: {0:N2}" -f $stopwatch.Elapsed.TotalSeconds)
$summary.spans |
    Select-Object name, runtime, span_id, parent_id |
    Format-Table -AutoSize
