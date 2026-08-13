[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Output,
    [string]$ServerUrl = "http://127.0.0.1:8081",
    [string]$MinerUCommand = "D:/tools/mineru-3.4.4-cpython/Scripts/mineru.exe",
    [string]$PhoenixUrl = "http://127.0.0.1:6006",
    [int]$UserId = 0,
    [string]$OrgTag = "",
    [int]$StartupTimeoutSeconds = 600
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$repoRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot "rag-agent-e2e-runtime.ps1")
$artifactDir = $Output
$runId = "multimodal-pilot-{0}-{1}" -f (Get-Date -Format "yyyyMMddHHmmss"), ([Guid]::NewGuid().ToString("N").Substring(0, 8))
$serverProcess = $null
$serverStartedHere = $false
$workerProcess = $null
$workerStartedHere = $false
$serverExecutable = Join-Path ([IO.Path]::GetTempPath()) "codeagent-mm-pilot-$runId.exe"
$serverStdout = Join-Path ([IO.Path]::GetTempPath()) "codeagent-mm-pilot-$runId.stdout.log"
$serverStderr = Join-Path ([IO.Path]::GetTempPath()) "codeagent-mm-pilot-$runId.stderr.log"
$workerStdout = Join-Path ([IO.Path]::GetTempPath()) "codeagent-mm-worker-$runId.stdout.log"
$workerStderr = Join-Path ([IO.Path]::GetTempPath()) "codeagent-mm-worker-$runId.stderr.log"
$completed = $false
$internalSecret = $null
$marker = "MINERU REAL OCR 20260729"

function Redact-Text {
    param([AllowNull()][string]$Text)
    if ([string]::IsNullOrEmpty($Text) -or [string]::IsNullOrEmpty($internalSecret)) {
        return $Text
    }
    return $Text.Replace($internalSecret, "[REDACTED]")
}

function Redact-LogFile {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (Test-Path -LiteralPath $Path -PathType Leaf) {
        $content = Get-Content -LiteralPath $Path -Raw -ErrorAction SilentlyContinue
        if ($null -ne $content) {
            Set-Content -LiteralPath $Path -Value (Redact-Text $content) -Encoding utf8
        }
    }
}

function Wait-Until {
    param(
        [Parameter(Mandatory = $true)][scriptblock]$Condition,
        [Parameter(Mandatory = $true)][string]$Description,
        [int]$TimeoutSeconds = $StartupTimeoutSeconds
    )
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        try {
            if (& $Condition) {
                Write-Host "ready: $Description"
                return
            }
        }
        catch {
            # Services commonly reject requests while their dependencies start.
        }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)
    throw "timed out waiting for $Description"
}

function Test-HttpEndpoint {
    param([Parameter(Mandatory = $true)][string]$Url)
    try {
        $response = Invoke-WebRequest -UseBasicParsing -Uri $Url -TimeoutSec 5
        return $response.StatusCode -ge 200 -and $response.StatusCode -le 299
    }
    catch {
        return $false
    }
}

function Test-ContainerState {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [Parameter(Mandatory = $true)][string[]]$Accepted
    )
    $state = docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' $Name 2>$null
    return $LASTEXITCODE -eq 0 -and ("$state".Trim() -in $Accepted)
}

function Test-InitContainerSucceeded {
    param([Parameter(Mandatory = $true)][string]$Name)
    $state = docker inspect --format '{{.State.Status}} {{.State.ExitCode}}' $Name 2>$null
    return $LASTEXITCODE -eq 0 -and "$state".Trim() -eq "exited 0"
}

function Test-KafkaBrokerReady {
    # A container can be briefly running while its broker has no usable
    # listener. Query broker metadata before starting consumers/producers.
    docker exec codeagent-kafka kafka-topics --bootstrap-server localhost:29092 --list 1>$null 2>$null
    return $LASTEXITCODE -eq 0
}

function Resolve-CorpusSetting {
    param([Parameter(Mandatory = $true)][string]$Name)
    $configPath = Join-Path $repoRoot "configs/server.yaml"
    $pattern = '^\s+{0}:\s*["'']?([^"''#]+)' -f [Regex]::Escape($Name)
    $match = Select-String -Path $configPath -Pattern $pattern | Select-Object -Last 1
    if ($null -eq $match -or $match.Matches.Count -eq 0) {
        throw "corpus setting $Name is not configured"
    }
    return $match.Matches[0].Groups[1].Value.Trim()
}

function Resolve-InternalSecret {
    $fromEnvironment = [Environment]::GetEnvironmentVariable("CODE_AGENT_RAG_INTERNAL_SECRET")
    if ([string]::IsNullOrWhiteSpace($fromEnvironment)) {
        throw "CODE_AGENT_RAG_INTERNAL_SECRET must be set in the environment"
    }
    return $fromEnvironment
}

function Stop-OwnedProcess {
    param([AllowNull()][System.Diagnostics.Process]$Process)
    if ($null -ne $Process) {
        if (-not $Process.HasExited) {
            Stop-Process -Id $Process.Id -Force -ErrorAction SilentlyContinue
        }
        try { $Process.WaitForExit() } catch { }
        $Process.Dispose()
    }
}

# Keep the ownership-specific stop expressions visible at the call site so
# reviewers can audit that pre-existing services are never terminated.
function Stop-OwnedWorker {
    if ($workerStartedHere -and $null -ne $workerProcess -and -not $workerProcess.HasExited) {
        Stop-Process -Id $workerProcess.Id -Force -ErrorAction SilentlyContinue
    }
}

function Stop-OwnedServer {
    if ($serverStartedHere -and $null -ne $serverProcess -and -not $serverProcess.HasExited) {
        Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue
    }
}

trap {
    $message = "$($_.Exception.Message)"
    $trapSecret = [Environment]::GetEnvironmentVariable("CODE_AGENT_RAG_INTERNAL_SECRET")
    if (-not [string]::IsNullOrEmpty($trapSecret)) {
        $message = $message.Replace($trapSecret, "[REDACTED]")
    }
    [Console]::Error.WriteLine("multimodal RAG pilot failed: $message")
    exit 1
}

try {
    if ($StartupTimeoutSeconds -le 0) { throw "StartupTimeoutSeconds must be greater than zero" }
    if ($UserId -le 0) { throw "UserId must be greater than zero" }
    if ([string]::IsNullOrWhiteSpace($OrgTag)) { throw "OrgTag must be set" }
    $internalSecret = Resolve-InternalSecret
    if (-not (Test-Path -LiteralPath $MinerUCommand -PathType Leaf)) {
        throw "MinerU executable not found at $MinerUCommand"
    }

    $sourcePath = "tests/fixtures/multimodal-rag-pilot.pdf"
    $inputPdf = Join-Path $repoRoot $sourcePath
    $sourceCommit = (& git -C $repoRoot rev-parse HEAD).Trim()
    if ($LASTEXITCODE -ne 0 -or $sourceCommit -notmatch '^[0-9a-f]{40}$') {
        throw "unable to resolve a valid Git source commit"
    }
    & git -C $repoRoot cat-file -e ("{0}:{1}" -f $sourceCommit, $sourcePath)
    if ($LASTEXITCODE -ne 0) {
        throw "pilot fixture must be tracked by the current Git HEAD before execution"
    }
    $remote = (& git -C $repoRoot remote get-url origin).Trim()
    if ($LASTEXITCODE -ne 0 -or $remote -notmatch '^https://github\.com/.+?/.+?(\.git)?$') {
        throw "pilot requires an HTTPS GitHub origin for immutable source provenance"
    }
    $sourceUrl = "{0}/blob/{1}/{2}" -f ($remote -replace '\.git$', ''), $sourceCommit, $sourcePath
    $targetIndex = Resolve-CorpusSetting "text_index"
    $corpusGeneration = Resolve-CorpusSetting "generation"

    docker info --format '{{.ServerVersion}}' | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "Docker daemon is unavailable" }

    Write-Host "Starting required RAG infrastructure without removing existing containers or volumes..."
    & docker compose up -d mysql redis minio minio-init zookeeper kafka kafka-init es embedding phoenix
    if ($LASTEXITCODE -ne 0) { throw "docker compose up failed" }

    Wait-Until { Test-ContainerState "codeagent-mysql" @("healthy", "running") } "MySQL"
    Wait-Until { Test-ContainerState "codeagent-redis" @("healthy", "running") } "Redis"
    Wait-Until { Test-ContainerState "codeagent-minio" @("healthy", "running") } "MinIO container"
    Wait-Until { Test-InitContainerSucceeded "codeagent-minio-init" } "MinIO bucket initialization"
    Wait-Until { Test-ContainerState "codeagent-zookeeper" @("healthy", "running") } "ZooKeeper"
    Wait-Until { Test-ContainerState "codeagent-kafka" @("healthy", "running") } "Kafka"
    Wait-Until { Test-InitContainerSucceeded "codeagent-kafka-init" } "Kafka topic initialization"
    Wait-Until { Test-KafkaBrokerReady } "Kafka broker metadata"
    Wait-Until { Test-ContainerState "codeagent-es" @("healthy", "running") } "Elasticsearch container"
    Wait-Until { Test-ContainerState "codeagent-embedding" @("healthy", "running") } "embedding container"
    Wait-Until { Test-ContainerState "codeagent-phoenix" @("healthy", "running") } "Phoenix container"
    Wait-Until { Test-HttpEndpoint "http://127.0.0.1:9000/minio/health/live" } "MinIO HTTP health"
    Wait-Until { Test-HttpEndpoint "http://127.0.0.1:9200/_cluster/health" } "Elasticsearch HTTP health"
    Wait-Until { Test-HttpEndpoint "http://127.0.0.1:8009/health" } "embedding HTTP health"
    Wait-Until { Test-HttpEndpoint "$PhoenixUrl/healthz" } "Phoenix HTTP health"

    if (-not (Test-HttpEndpoint "http://127.0.0.1:8090/healthz")) {
        Write-Host "Starting Python ingestion worker for this pilot run..."
        $workerProcess = Invoke-WithPaismartInternalToken -Secret $internalSecret -Action {
            Start-Process -FilePath "C:\Python312\python.exe" `
                -ArgumentList "-m", "uvicorn", "orchestrator.rag.main:app", "--host", "127.0.0.1", "--port", "8090" `
                -WorkingDirectory $repoRoot -RedirectStandardOutput $workerStdout -RedirectStandardError $workerStderr `
                -WindowStyle Hidden -PassThru
        }
        $workerStartedHere = $true
        Wait-Until { Test-HttpEndpoint "http://127.0.0.1:8090/healthz" } "Python ingestion worker"
    }
    else {
        Write-Host "Using the Python ingestion worker already listening at http://127.0.0.1:8090"
    }

    if (-not (Test-HttpEndpoint "$ServerUrl/healthz")) {
        Write-Host "Starting Go server for this pilot run..."
        & go build -o $serverExecutable ./cmd/server
        if ($LASTEXITCODE -ne 0) { throw "building Go server failed" }
        $serverProcess = Invoke-WithOrchestratorSharedSecret -Secret $internalSecret -Action {
            Start-Process -FilePath $serverExecutable -WorkingDirectory $repoRoot `
                -RedirectStandardOutput $serverStdout -RedirectStandardError $serverStderr `
                -WindowStyle Hidden -PassThru
        }
        $serverStartedHere = $true
        Wait-Until { Test-HttpEndpoint "$ServerUrl/healthz" } "Go server"
    }
    else {
        Write-Host "Using the Go server already listening at $ServerUrl"
    }

    Write-Host "Running MinerU OCR and multimodal RAG pilot..."
    & C:\Python312\python.exe -m orchestrator.rag.multimodal_pilot `
        --output $Output `
        --marker $marker `
        --input-pdf $inputPdf `
        --server-url $ServerUrl `
        --user-id $UserId `
        --org-tag $OrgTag `
        --source-id "localcode-multimodal-pilot" `
        --source-path $sourcePath `
        --source-url $sourceUrl `
        --source-commit $sourceCommit `
        --run-id $runId `
        --target-index $targetIndex `
        --corpus-generation $corpusGeneration `
        --mineru-command $MinerUCommand `
        --phoenix-url $PhoenixUrl
    if ($LASTEXITCODE -ne 0) { throw "multimodal pilot failed with exit code $LASTEXITCODE" }
    if (-not (Test-Path -LiteralPath (Join-Path $Output "manifest.json") -PathType Leaf)) {
        throw "multimodal pilot completed without an artifact manifest"
    }
    $completed = $true
    Write-Host "artifact: $artifactDir"
}
finally {
    if ($workerStartedHere -and $null -ne $workerProcess) { Stop-OwnedWorker; Stop-OwnedProcess $workerProcess }
    if ($serverStartedHere -and $null -ne $serverProcess) { Stop-OwnedServer; Stop-OwnedProcess $serverProcess }
    if (Test-Path -LiteralPath $serverExecutable -PathType Leaf) {
        Remove-Item -LiteralPath $serverExecutable -Force -ErrorAction SilentlyContinue
    }
    if ($completed) {
        Remove-Item -LiteralPath $serverStdout, $serverStderr, $workerStdout, $workerStderr -Force -ErrorAction SilentlyContinue
        $remainingLogs = @($serverStdout, $serverStderr, $workerStdout, $workerStderr | Where-Object { Test-Path -LiteralPath $_ })
        if ($remainingLogs.Count -ne 0) { throw "temporary pilot logs could not be removed: $($remainingLogs -join ', ')" }
    }
    if (-not $completed) {
        foreach ($log in @($serverStdout, $serverStderr, $workerStdout, $workerStderr)) { Redact-LogFile $log }
        Write-Host "temporary logs retained:"
        Write-Host "  server stdout: $serverStdout"
        Write-Host "  server stderr: $serverStderr"
        Write-Host "  worker stdout: $workerStdout"
        Write-Host "  worker stderr: $workerStderr"
    }
}
