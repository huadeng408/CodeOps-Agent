[CmdletBinding()]
param(
    [string]$ServerUrl = "http://127.0.0.1:8081",
    [string]$MinerUCommand = "D:/tools/mineru-3.4.4-cpython/Scripts/mineru.exe",
    [int]$StartupTimeoutSeconds = 600,
    [string]$ArtifactRoot = ".tmp/rag-agent-e2e"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

trap {
    $message = "$($_.Exception.Message)"
    $secret = [Environment]::GetEnvironmentVariable("CODE_AGENT_RAG_INTERNAL_SECRET")
    if (-not [string]::IsNullOrEmpty($secret)) {
        $message = $message.Replace($secret, "[REDACTED]")
    }
    Write-Error "RAG agent E2E terminated with a failure: $message"
    exit 1
}

$repoRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $PSScriptRoot "rag-agent-e2e-runtime.ps1")
$serverProcess = $null
$serverStartedHere = $false
$workerProcess = $null
$workerStartedHere = $false
$completed = $false
$runId = "{0}-{1}" -f (Get-Date -Format "yyyyMMddHHmmss"), ([Guid]::NewGuid().ToString("N").Substring(0, 8))
$serverStdout = Join-Path ([IO.Path]::GetTempPath()) "codeagent-rag-e2e-$runId.stdout.log"
$serverStderr = Join-Path ([IO.Path]::GetTempPath()) "codeagent-rag-e2e-$runId.stderr.log"
$serverExecutable = Join-Path ([IO.Path]::GetTempPath()) "codeagent-rag-e2e-$runId.exe"
$workerStdout = Join-Path ([IO.Path]::GetTempPath()) "codeagent-rag-worker-$runId.stdout.log"
$workerStderr = Join-Path ([IO.Path]::GetTempPath()) "codeagent-rag-worker-$runId.stderr.log"
$artifactDir = Join-Path (Join-Path $repoRoot $ArtifactRoot) $runId
$snapshotBefore = Join-Path $artifactDir "data-before.json"
$snapshotAfter = Join-Path $artifactDir "data-after.json"
$trackedEnvironmentNames = @(
    "CODE_AGENT_MINERU_COMMAND",
    "CODE_AGENT_MINERU_BACKEND",
    "CODE_AGENT_RUN_MINERU_E2E",
    "CODE_AGENT_RUN_RAG_E2E",
    "CODE_AGENT_RAG_SERVER_URL",
    "CODE_AGENT_RAG_INTERNAL_SECRET",
    "ORCHESTRATOR_SHARED_SECRET",
    "CODE_AGENT_RAG_USER_ID",
    "CODE_AGENT_RAG_ORG_TAG",
    "CODE_AGENT_RAG_SOURCE_ID",
    "CODE_AGENT_RAG_SOURCE_PATH_PREFIX",
    "CODE_AGENT_RAG_SOURCE_URL",
    "CODE_AGENT_RAG_SOURCE_COMMIT",
    "CODE_AGENT_RAG_TARGET_INDEX",
    "CODE_AGENT_RAG_CORPUS_GENERATION",
    "CODE_AGENT_RAG_RUN_ID"
)
$trackedEnvironment = @{}
foreach ($name in $trackedEnvironmentNames) {
    $item = Get-Item -LiteralPath ("Env:{0}" -f $name) -ErrorAction SilentlyContinue
    $trackedEnvironment[$name] = @{
        Present = $null -ne $item
        Value   = if ($null -ne $item) { $item.Value } else { $null }
    }
}

function Wait-Until {
    param(
        [Parameter(Mandatory = $true)][scriptblock]$Condition,
        [Parameter(Mandatory = $true)][string]$Description,
        [int]$TimeoutSeconds = 300
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
            # Dependencies commonly reject connections while starting.
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

function Test-ContainerRunning {
    param([Parameter(Mandatory = $true)][string]$Name)
    $state = docker inspect --format '{{.State.Running}}' $Name 2>$null
    return $LASTEXITCODE -eq 0 -and "$state".Trim() -eq "true"
}

function Test-ContainerHealthy {
    param([Parameter(Mandatory = $true)][string]$Name)
    $state = docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' $Name 2>$null
    return $LASTEXITCODE -eq 0 -and ("$state".Trim() -in @("healthy", "running"))
}

function Test-InitContainerSucceeded {
    param([Parameter(Mandatory = $true)][string]$Name)
    $state = docker inspect --format '{{.State.Status}} {{.State.ExitCode}}' $Name 2>$null
    return $LASTEXITCODE -eq 0 -and "$state".Trim() -eq "exited 0"
}

function Resolve-InternalSecret {
    $fromEnvironment = [Environment]::GetEnvironmentVariable("CODE_AGENT_RAG_INTERNAL_SECRET")
    if ([string]::IsNullOrWhiteSpace($fromEnvironment)) {
        throw "CODE_AGENT_RAG_INTERNAL_SECRET must be set in the environment"
    }
    return $fromEnvironment
}

function Test-EmbeddingReady {
    try {
        $health = Invoke-RestMethod -Uri "http://127.0.0.1:8009/health" -TimeoutSec 5
        return $health.ready -eq $true `
            -and $health.model -eq "BAAI/bge-m3" `
            -and $health.model_revision -eq "BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181" `
            -and [int]$health.dimensions -eq 1024
    }
    catch {
        return $false
    }
}

function Write-DataSnapshot {
    param([Parameter(Mandatory = $true)][string]$Path)

    $mysqlRows = & docker exec -e MYSQL_PWD=codeagent codeagent-mysql mysql -ucodeagent -Dcodeagent -N -e "SELECT 'knowledge_source',COUNT(*) FROM knowledge_source UNION ALL SELECT 'knowledge_document',COUNT(*) FROM knowledge_document UNION ALL SELECT 'document_vectors',COUNT(*) FROM document_vectors UNION ALL SELECT 'file_upload',COUNT(*) FROM file_upload UNION ALL SELECT 'chunk_info',COUNT(*) FROM chunk_info UNION ALL SELECT 'pipeline_task',COUNT(*) FROM pipeline_task;"
    if ($LASTEXITCODE -ne 0) { throw "MySQL snapshot failed" }
    $mysql = @{}
    foreach ($row in $mysqlRows) {
        $parts = "$row" -split "`t", 2
        if ($parts.Count -eq 2) { $mysql[$parts[0]] = [int64]$parts[1] }
    }
    $es = @{}
    foreach ($index in @("knowledge_base", "knowledge_base_v2_bge_m3", "conversation_memory")) {
        $es[$index] = [int64](Invoke-RestMethod -Uri "http://127.0.0.1:9200/$index/_count" -TimeoutSec 10).count
    }
    $minioText = & docker exec codeagent-minio sh -c "mc alias set local http://127.0.0.1:9000 minioadmin minioadmin >/dev/null && mc ls --recursive local/uploads | wc -l"
    if ($LASTEXITCODE -ne 0) { throw "MinIO snapshot failed" }
    [ordered]@{
        run_id = $runId
        captured_at = (Get-Date).ToUniversalTime().ToString("o")
        git_sha = (& git rev-parse HEAD).Trim()
        mysql = $mysql
        elasticsearch = $es
        minio_upload_objects = [int64]("$minioText".Trim())
    } | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $Path -Encoding utf8
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

function Invoke-RAGInternalProbe {
    param(
        [Parameter(Mandatory = $true)][string]$ServerUrl,
        [Parameter(Mandatory = $true)][string]$InternalSecret
    )

    $query = "codeagent-probe-$([Guid]::NewGuid().ToString('N'))"
    $payload = @{
        user           = @{ id = 1 }
        query          = $query
        topK           = 1
        mode           = "bm25"
        disableRerank  = $true
    } | ConvertTo-Json -Compress
    try {
        $response = Invoke-WebRequest -UseBasicParsing -Method Post `
            -Uri (("{0}/internal/orchestrator/knowledge-search" -f $ServerUrl).TrimEnd('/')) `
            -Headers @{ "X-Internal-Token" = $InternalSecret } `
            -ContentType "application/json" -Body $payload -TimeoutSec 10 -ErrorAction Stop
        if ($response.StatusCode -lt 200 -or $response.StatusCode -gt 299) {
            throw "unexpected HTTP status"
        }
        $envelope = $response.Content | ConvertFrom-Json
        if ($null -eq $envelope.code -or [int]$envelope.code -lt 200 -or [int]$envelope.code -gt 299) {
            throw "unexpected internal endpoint response"
        }
    }
    catch {
        throw "code-agent internal RAG endpoint probe failed"
    }
}

function Restore-TrackedEnvironment {
    foreach ($name in $trackedEnvironmentNames) {
        $state = $trackedEnvironment[$name]
        $path = "Env:{0}" -f $name
        if ($state.Present) {
            Set-Item -LiteralPath $path -Value $state.Value
        }
        else {
            Remove-Item -LiteralPath $path -ErrorAction SilentlyContinue
        }
    }
}

function Invoke-GoTest {
    param([Parameter(Mandatory = $true)][string[]]$Arguments)
    & go @Arguments 2>&1 | Tee-Object -Variable output
    $exitCode = $LASTEXITCODE
    if ($exitCode -ne 0) {
        throw "go $($Arguments -join ' ') failed with exit code $exitCode"
    }
}

Push-Location $repoRoot
try {
    New-Item -ItemType Directory -Force -Path $artifactDir | Out-Null
    if (-not (Test-Path -LiteralPath $MinerUCommand -PathType Leaf)) {
        throw "MinerU executable not found at $MinerUCommand"
    }
    docker info --format '{{.ServerVersion}}' | Out-Null
    if ($LASTEXITCODE -ne 0) {
        throw "Docker daemon is unavailable"
    }

    Write-Host "Starting required RAG infrastructure without removing existing containers or volumes..."
    & docker compose up -d mysql redis minio minio-init tika zookeeper kafka kafka-init es embedding
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose up failed"
    }

    Wait-Until { Test-ContainerHealthy "codeagent-mysql" } "MySQL" $StartupTimeoutSeconds
    Wait-Until { Test-ContainerHealthy "codeagent-redis" } "Redis" $StartupTimeoutSeconds
    Wait-Until { Test-ContainerRunning "codeagent-minio" } "MinIO container" $StartupTimeoutSeconds
    Wait-Until { Test-InitContainerSucceeded "codeagent-minio-init" } "MinIO bucket initialization" $StartupTimeoutSeconds
    Wait-Until { Test-ContainerRunning "codeagent-tika" } "Tika container" $StartupTimeoutSeconds
    Wait-Until { Test-ContainerRunning "codeagent-zookeeper" } "ZooKeeper" $StartupTimeoutSeconds
    Wait-Until { Test-ContainerRunning "codeagent-kafka" } "Kafka" $StartupTimeoutSeconds
    Wait-Until { Test-InitContainerSucceeded "codeagent-kafka-init" } "Kafka topic initialization" $StartupTimeoutSeconds
    Wait-Until { Test-ContainerHealthy "codeagent-es" } "Elasticsearch container" $StartupTimeoutSeconds
    Wait-Until { Test-ContainerRunning "codeagent-embedding" } "embedding container" $StartupTimeoutSeconds

    Wait-Until { Test-HttpEndpoint "http://127.0.0.1:9000/minio/health/live" } "MinIO HTTP health" $StartupTimeoutSeconds
    Wait-Until { Test-HttpEndpoint "http://127.0.0.1:9998/" } "Tika HTTP endpoint" $StartupTimeoutSeconds
    Wait-Until { Test-HttpEndpoint "http://127.0.0.1:9200/_cluster/health" } "Elasticsearch HTTP health" $StartupTimeoutSeconds
    Wait-Until { Test-EmbeddingReady } "native BGE-M3 embedding readiness" $StartupTimeoutSeconds
    Wait-Until {
        & docker compose exec -T kafka kafka-topics --bootstrap-server kafka:29092 --list 2>$null | Out-Null
        return $LASTEXITCODE -eq 0
    } "Kafka broker" $StartupTimeoutSeconds

    $env:CODE_AGENT_MINERU_COMMAND = $MinerUCommand
    $env:CODE_AGENT_MINERU_BACKEND = "pipeline"

    Write-Host "Running real MinerU OCR integration..."
    $env:CODE_AGENT_RUN_MINERU_E2E = "1"
    Invoke-GoTest @("test", "./pkg/mineru", "-run", "TestRealMinerUOCR", "-count=1", "-v")

	$internalSecret = Resolve-InternalSecret
	$loaderUser = Resolve-CorpusSetting "loader_user"
	$corpusGeneration = Resolve-CorpusSetting "generation"
	$targetIndex = Resolve-CorpusSetting "text_index"
	$sourceCommit = (& git rev-parse HEAD).Trim()
	if ($LASTEXITCODE -ne 0 -or $sourceCommit -notmatch '^[0-9a-f]{40}$') {
		throw "cannot resolve an auditable source commit from git HEAD"
	}
    if (-not (Test-HttpEndpoint "http://127.0.0.1:8090/healthz")) {
        Write-Host "Starting Python ingestion worker for this E2E run..."
        $workerProcess = Invoke-WithPaismartInternalToken -Secret $internalSecret -Action {
            Start-Process -FilePath "C:\Python312\python.exe" `
                -ArgumentList "-m", "uvicorn", "orchestrator.rag.main:app", "--host", "127.0.0.1", "--port", "8090" `
                -WorkingDirectory $repoRoot -RedirectStandardOutput $workerStdout -RedirectStandardError $workerStderr `
                -WindowStyle Hidden -PassThru
        }
        $workerStartedHere = $true
        Wait-Until { Test-HttpEndpoint "http://127.0.0.1:8090/healthz" } "Python ingestion worker" $StartupTimeoutSeconds
    }
    else {
        Write-Host "Using the Python ingestion worker already listening at http://127.0.0.1:8090"
    }
    if (-not (Test-HttpEndpoint "$ServerUrl/healthz")) {
        Write-Host "Starting Go server for this E2E run..."
        & go build -o $serverExecutable ./cmd/server
        if ($LASTEXITCODE -ne 0) {
            throw "building Go server failed"
        }
        $serverProcess = Invoke-WithOrchestratorSharedSecret -Secret $internalSecret -Action {
            Start-Process -FilePath $serverExecutable -WorkingDirectory $repoRoot -RedirectStandardOutput $serverStdout -RedirectStandardError $serverStderr -WindowStyle Hidden -PassThru
        }
        $serverStartedHere = $true
        Wait-Until { Test-HttpEndpoint "$ServerUrl/healthz" } "Go server" $StartupTimeoutSeconds
    }
    else {
        Write-Host "Using the Go server already listening at $ServerUrl"
    }
	Invoke-RAGInternalProbe -ServerUrl $ServerUrl -InternalSecret $internalSecret
	Write-DataSnapshot -Path $snapshotBefore

    $env:CODE_AGENT_RUN_RAG_E2E = "1"
    $env:CODE_AGENT_RAG_SERVER_URL = $ServerUrl
    $env:CODE_AGENT_RAG_INTERNAL_SECRET = $internalSecret
    $env:CODE_AGENT_RAG_USER_ID = "$loaderUser"
    $env:CODE_AGENT_RAG_ORG_TAG = ""
    $env:CODE_AGENT_RAG_SOURCE_ID = "localcode-rag-e2e-$runId"
    $env:CODE_AGENT_RAG_SOURCE_PATH_PREFIX = "e2e/$runId"
    # The synthetic file does not exist in the Git tree. Keep sourceUrl empty
    # instead of fabricating a repository URL that cannot locate its bytes.
    $env:CODE_AGENT_RAG_SOURCE_URL = ""
    $env:CODE_AGENT_RAG_SOURCE_COMMIT = $sourceCommit
    $env:CODE_AGENT_RAG_TARGET_INDEX = $targetIndex
    $env:CODE_AGENT_RAG_CORPUS_GENERATION = $corpusGeneration
    $env:CODE_AGENT_RAG_RUN_ID = "rag-e2e-$runId"

    Write-Host "Running real RAG ingest and SearchKnowledge integrations..."
    Invoke-GoTest @("test", "./internal/rag", "./internal/cli", "-run", "TestRealRAGIngestThenSearchKnowledge|TestRealNewAppRAGIngestThenSearchKnowledge", "-count=1", "-v")
    Write-DataSnapshot -Path $snapshotAfter
    $completed = $true
    Write-Host "Real MinerU and RAG agent E2E tests passed."
}
finally {
	try {
		if ($workerStartedHere -and $null -ne $workerProcess) {
			if (-not $workerProcess.HasExited) {
				Stop-Process -Id $workerProcess.Id -Force -ErrorAction SilentlyContinue
			}
			try { $workerProcess.WaitForExit() } catch { }
			$workerProcess.Dispose()
		}
		if ($serverStartedHere -and $null -ne $serverProcess) {
			if (-not $serverProcess.HasExited) {
				Stop-Process -Id $serverProcess.Id -Force -ErrorAction SilentlyContinue
			}
			try { $serverProcess.WaitForExit() } catch { }
			$serverProcess.Dispose()
		}
		if (Test-Path -LiteralPath $serverExecutable -PathType Leaf) {
            Remove-Item -LiteralPath $serverExecutable -Force -ErrorAction SilentlyContinue
		}
		if ($completed) {
			Remove-Item -LiteralPath $serverStdout, $serverStderr, $workerStdout, $workerStderr -Force -ErrorAction SilentlyContinue
			$remainingLogs = @(@($serverStdout, $serverStderr, $workerStdout, $workerStderr) | Where-Object { Test-Path -LiteralPath $_ })
			if ($remainingLogs.Count -ne 0) {
				throw "E2E passed, but temporary server logs could not be removed: $($remainingLogs -join ', ')"
			}
		}
		if (-not $completed) {
            Write-Host "E2E failed. Server logs were retained at:"
            Write-Host "  stdout: $serverStdout"
            Write-Host "  stderr: $serverStderr"
            Write-Host "  worker stdout: $workerStdout"
            Write-Host "  worker stderr: $workerStderr"
		}
	}
	finally {
		Restore-TrackedEnvironment
		Pop-Location
	}
}
