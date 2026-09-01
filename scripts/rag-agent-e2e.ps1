[CmdletBinding()]
param(
    [string]$ServerUrl = "http://127.0.0.1:8082",
    [int]$WorkerPort = 8092,
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
$e2eKafkaTopics = $null
$kafkaContainerName = $null
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
    "CODE_AGENT_RAG_RUN_ID",
    "CODE_AGENT_SERVER_PORT",
    "PAISMART_GO_BASE_URL",
    "PAISMART_INGESTION_BASE_URL",
    "PAISMART_PORT",
    "CODE_AGENT_RAG_READ_ALIAS",
    "CODE_AGENT_KAFKA_PARSE_TOPIC",
    "CODE_AGENT_KAFKA_CHUNK_TOPIC",
    "CODE_AGENT_KAFKA_EMBED_TOPIC",
    "CODE_AGENT_KAFKA_INDEX_TOPIC",
    "CODE_AGENT_KAFKA_DLQ_TOPIC",
    "CODE_AGENT_KAFKA_CONSUMER_GROUP_PREFIX"
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

function Test-KafkaReady {
    param([Parameter(Mandatory = $true)][string]$ContainerName)
    & docker exec $ContainerName kafka-topics --bootstrap-server localhost:9092 --list 2>$null | Out-Null
    return $LASTEXITCODE -eq 0
}

function New-E2EKafkaTopics {
    param(
        [Parameter(Mandatory = $true)][string]$ContainerName,
        [Parameter(Mandatory = $true)][hashtable]$Topics
    )

    foreach ($topic in $Topics.Values) {
        & docker exec $ContainerName kafka-topics --bootstrap-server localhost:9092 --create --if-not-exists --topic $topic 2>$null | Out-Null
        if ($LASTEXITCODE -ne 0) {
            throw "failed to create isolated Kafka topic $topic"
        }
    }
}

function Remove-E2EKafkaTopics {
    param(
        [Parameter(Mandatory = $true)][string]$ContainerName,
        [Parameter(Mandatory = $true)][hashtable]$Topics
    )

    foreach ($topic in $Topics.Values) {
        & docker exec $ContainerName kafka-topics --bootstrap-server localhost:9092 --delete --if-exists --topic $topic 2>$null | Out-Null
        if ($LASTEXITCODE -ne 0) {
            Write-Warning "could not remove isolated Kafka topic $topic"
        }
    }
}

function Remove-E2EKafkaGroups {
    param(
        [Parameter(Mandatory = $true)][string]$ContainerName,
        [Parameter(Mandatory = $true)][string]$GroupPrefix
    )

    foreach ($stage in @("parse", "chunk", "embed", "index")) {
        $group = "$GroupPrefix-$stage"
        $result = @(& docker exec $ContainerName kafka-consumer-groups --bootstrap-server localhost:9092 --delete --group $group 2>&1)
        $exitCode = $LASTEXITCODE
        $text = ($result -join "`n")
        if ($text -match "GroupIdNotFoundException|group id does not exist|does not exist") {
            Write-Host "Kafka consumer group already absent: $group"
            continue
        }
        if ($exitCode -ne 0 -or $text -match "could not be deleted|failed") {
            throw "could not remove isolated Kafka consumer group ${group}: $text"
        }
    }
}

function Resolve-MinIOContainerName {
    if (Test-ContainerRunning "codeagent-minio") {
        return "codeagent-minio"
    }
    if (Test-ContainerRunning "minio") {
        return "minio"
    }
    return $null
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
    $minioText = & docker exec $minioContainerName sh -c "mc alias set local http://127.0.0.1:9000 minioadmin minioadmin >/dev/null && mc ls --recursive local/uploads | wc -l"
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
    # Reuse an already healthy Tika endpoint (for example, a separately
    # managed local stack) instead of competing for the fixed host port 9998.
    # All other services remain owned by this compose project and keep their
    # existing startup/readiness checks below.
    $tikaAlreadyAvailable = Test-HttpEndpoint "http://127.0.0.1:9998/"
    $minioAlreadyAvailable = Test-HttpEndpoint "http://127.0.0.1:9000/minio/health/live"
    $esAlreadyAvailable = Test-HttpEndpoint "http://127.0.0.1:9200/_cluster/health"
    $kafkaContainerName = if (Test-KafkaReady "kafka") { "kafka" } else { "codeagent-kafka" }
    $kafkaAlreadyAvailable = $kafkaContainerName -eq "kafka"
    $minioContainerName = Resolve-MinIOContainerName
    if ($minioAlreadyAvailable -and $null -eq $minioContainerName) {
        throw "MinIO endpoint is already listening but no supported Docker container is available for the required snapshot"
    }
    $composeServices = @("mysql", "redis", "zookeeper", "embedding")
    if (-not $minioAlreadyAvailable) {
        $composeServices += @("minio", "minio-init")
    }
    if (-not $kafkaAlreadyAvailable) {
        $composeServices += @("kafka", "kafka-init")
    }
    if (-not $esAlreadyAvailable) {
        $composeServices += "es"
    }
    if (-not $tikaAlreadyAvailable) {
        $composeServices += "tika"
    }
    & docker compose up -d @composeServices
    if ($LASTEXITCODE -ne 0) {
        throw "docker compose up failed"
    }

    Wait-Until { Test-ContainerHealthy "codeagent-mysql" } "MySQL" $StartupTimeoutSeconds
    Wait-Until { Test-ContainerHealthy "codeagent-redis" } "Redis" $StartupTimeoutSeconds
    if ($minioAlreadyAvailable) {
        Write-Host "Using the MinIO endpoint already listening at http://127.0.0.1:9000"
    }
    else {
        Wait-Until { Test-ContainerRunning "codeagent-minio" } "MinIO container" $StartupTimeoutSeconds
        Wait-Until { Test-InitContainerSucceeded "codeagent-minio-init" } "MinIO bucket initialization" $StartupTimeoutSeconds
        $minioContainerName = "codeagent-minio"
    }
    if ($tikaAlreadyAvailable) {
        Write-Host "Using the Tika endpoint already listening at http://127.0.0.1:9998"
    }
    else {
        Wait-Until { Test-ContainerRunning "codeagent-tika" } "Tika container" $StartupTimeoutSeconds
    }
    Wait-Until { Test-ContainerRunning "codeagent-zookeeper" } "ZooKeeper" $StartupTimeoutSeconds
    if ($kafkaAlreadyAvailable) {
        Write-Host "Using the Kafka broker already listening at localhost:9092"
    }
    else {
        Wait-Until { Test-ContainerRunning "codeagent-kafka" } "Kafka" $StartupTimeoutSeconds
        Wait-Until { Test-InitContainerSucceeded "codeagent-kafka-init" } "Kafka topic initialization" $StartupTimeoutSeconds
    }
    if ($esAlreadyAvailable) {
        Write-Host "Using the Elasticsearch endpoint already listening at http://127.0.0.1:9200"
    }
    else {
        Wait-Until { Test-ContainerHealthy "codeagent-es" } "Elasticsearch container" $StartupTimeoutSeconds
    }
    Wait-Until { Test-ContainerRunning "codeagent-embedding" } "embedding container" $StartupTimeoutSeconds

    Wait-Until { Test-HttpEndpoint "http://127.0.0.1:9000/minio/health/live" } "MinIO HTTP health" $StartupTimeoutSeconds
    Wait-Until { Test-HttpEndpoint "http://127.0.0.1:9998/" } "Tika HTTP endpoint" $StartupTimeoutSeconds
    Wait-Until { Test-HttpEndpoint "http://127.0.0.1:9200/_cluster/health" } "Elasticsearch HTTP health" $StartupTimeoutSeconds
    Wait-Until { Test-EmbeddingReady } "native BGE-M3 embedding readiness" $StartupTimeoutSeconds
    Wait-Until { Test-KafkaReady $kafkaContainerName } "Kafka broker" $StartupTimeoutSeconds

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
    $kafkaTopicPrefix = "codeagent-rag-e2e-$runId"
    $e2eKafkaTopics = [ordered]@{
        Parse = "$kafkaTopicPrefix-parse"
        Chunk = "$kafkaTopicPrefix-chunk"
        Embed = "$kafkaTopicPrefix-embed"
        Index = "$kafkaTopicPrefix-index"
        DLQ = "$kafkaTopicPrefix-dlq"
    }
    New-E2EKafkaTopics -ContainerName $kafkaContainerName -Topics $e2eKafkaTopics
    $env:CODE_AGENT_KAFKA_PARSE_TOPIC = $e2eKafkaTopics.Parse
    $env:CODE_AGENT_KAFKA_CHUNK_TOPIC = $e2eKafkaTopics.Chunk
    $env:CODE_AGENT_KAFKA_EMBED_TOPIC = $e2eKafkaTopics.Embed
    $env:CODE_AGENT_KAFKA_INDEX_TOPIC = $e2eKafkaTopics.Index
    $env:CODE_AGENT_KAFKA_DLQ_TOPIC = $e2eKafkaTopics.DLQ
    $env:CODE_AGENT_KAFKA_CONSUMER_GROUP_PREFIX = "codeagent-rag-e2e-$runId"
    $workerUrl = "http://127.0.0.1:{0}" -f $WorkerPort
    if (Test-HttpEndpoint "$workerUrl/healthz") {
        throw "refusing to reuse an existing Python ingestion worker at $workerUrl; choose a free -WorkerPort for an isolated E2E run"
    }
    Write-Host "Starting Python ingestion worker for this E2E run..."
    $env:PAISMART_PORT = "$WorkerPort"
    $env:PAISMART_GO_BASE_URL = $ServerUrl
    $workerProcess = Invoke-WithPaismartInternalToken -Secret $internalSecret -Action {
        Start-Process -FilePath "C:\Python312\python.exe" `
            -ArgumentList "-m", "uvicorn", "orchestrator.rag.main:app", "--host", "127.0.0.1", "--port", "$WorkerPort" `
            -WorkingDirectory $repoRoot -RedirectStandardOutput $workerStdout -RedirectStandardError $workerStderr `
            -WindowStyle Hidden -PassThru
    }
    $workerStartedHere = $true
    Wait-Until { Test-HttpEndpoint "$workerUrl/healthz" } "Python ingestion worker" $StartupTimeoutSeconds
    if (Test-HttpEndpoint "$ServerUrl/healthz") {
        throw "refusing to reuse an existing Go server at $ServerUrl; choose a free -ServerUrl for an isolated E2E run"
    }
    Write-Host "Starting Go server for this E2E run..."
    & go build -o $serverExecutable ./cmd/server
    if ($LASTEXITCODE -ne 0) {
        throw "building Go server failed"
    }
	$serverPort = ([Uri]$ServerUrl).Port
    $env:CODE_AGENT_SERVER_PORT = "$serverPort"
    $env:PAISMART_INGESTION_BASE_URL = $workerUrl
    # This run must not create or retarget the shared read alias. Search
    # the existing isolated write index directly for its private marker.
    $env:CODE_AGENT_RAG_READ_ALIAS = $targetIndex
    $serverProcess = Invoke-WithOrchestratorSharedSecret -Secret $internalSecret -Action {
        Start-Process -FilePath $serverExecutable -WorkingDirectory $repoRoot -RedirectStandardOutput $serverStdout -RedirectStandardError $serverStderr -WindowStyle Hidden -PassThru
    }
    $serverStartedHere = $true
    Wait-Until { Test-HttpEndpoint "$ServerUrl/healthz" } "Go server" $StartupTimeoutSeconds
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
		if ($null -ne $e2eKafkaTopics -and -not [string]::IsNullOrWhiteSpace($kafkaContainerName)) {
			Remove-E2EKafkaGroups -ContainerName $kafkaContainerName -GroupPrefix "codeagent-rag-e2e-$runId"
			Remove-E2EKafkaTopics -ContainerName $kafkaContainerName -Topics $e2eKafkaTopics
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
