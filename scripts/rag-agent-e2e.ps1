[CmdletBinding()]
param(
    [string]$ServerUrl = "http://127.0.0.1:8081",
    [string]$MinerUCommand = "D:/tools/mineru-3.4.4-cpython/Scripts/mineru.exe",
    [int]$StartupTimeoutSeconds = 600
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

$repoRoot = Split-Path -Parent $PSScriptRoot
$serverProcess = $null
$serverStartedHere = $false
$completed = $false
$runId = "{0}-{1}" -f (Get-Date -Format "yyyyMMddHHmmss"), ([Guid]::NewGuid().ToString("N").Substring(0, 8))
$serverStdout = Join-Path ([IO.Path]::GetTempPath()) "codeagent-rag-e2e-$runId.stdout.log"
$serverStderr = Join-Path ([IO.Path]::GetTempPath()) "codeagent-rag-e2e-$runId.stderr.log"
$serverExecutable = Join-Path ([IO.Path]::GetTempPath()) "codeagent-rag-e2e-$runId.exe"
$trackedEnvironmentNames = @(
    "CODE_AGENT_MINERU_COMMAND",
    "CODE_AGENT_MINERU_BACKEND",
    "CODE_AGENT_RUN_MINERU_E2E",
    "CODE_AGENT_RUN_RAG_E2E",
    "CODE_AGENT_RAG_SERVER_URL",
    "CODE_AGENT_RAG_INTERNAL_SECRET",
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
    if (-not [string]::IsNullOrWhiteSpace($fromEnvironment)) {
        return $fromEnvironment
    }

    $configPath = Join-Path $repoRoot "configs/server.yaml"
    $match = Select-String -Path $configPath -Pattern '^\s+shared_secret:\s*["'']?([^"''#]+)' | Select-Object -Last 1
    if ($null -eq $match -or $match.Matches.Count -eq 0) {
        throw "internal shared secret is not configured; set CODE_AGENT_RAG_INTERNAL_SECRET"
    }
    return $match.Matches[0].Groups[1].Value.Trim()
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
    Wait-Until { Test-HttpEndpoint "http://127.0.0.1:8009/health" } "embedding HTTP health" $StartupTimeoutSeconds
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
	$sourceUrl = (& git remote get-url origin).Trim()
	if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($sourceUrl)) {
		throw "cannot resolve an auditable source URL from git remote origin"
	}
    if (-not (Test-HttpEndpoint "$ServerUrl/healthz")) {
        Write-Host "Starting Go server for this E2E run..."
        & go build -o $serverExecutable ./cmd/server
        if ($LASTEXITCODE -ne 0) {
            throw "building Go server failed"
        }
        $serverProcess = Start-Process -FilePath $serverExecutable -WorkingDirectory $repoRoot -RedirectStandardOutput $serverStdout -RedirectStandardError $serverStderr -WindowStyle Hidden -PassThru
        $serverStartedHere = $true
        Wait-Until { Test-HttpEndpoint "$ServerUrl/healthz" } "Go server" $StartupTimeoutSeconds
    }
    else {
        Write-Host "Using the Go server already listening at $ServerUrl"
    }
	Invoke-RAGInternalProbe -ServerUrl $ServerUrl -InternalSecret $internalSecret

    $env:CODE_AGENT_RUN_RAG_E2E = "1"
    $env:CODE_AGENT_RAG_SERVER_URL = $ServerUrl
    $env:CODE_AGENT_RAG_INTERNAL_SECRET = $internalSecret
    $env:CODE_AGENT_RAG_USER_ID = "$loaderUser"
    $env:CODE_AGENT_RAG_ORG_TAG = ""
    $env:CODE_AGENT_RAG_SOURCE_ID = "localcode-rag-e2e-$runId"
    $env:CODE_AGENT_RAG_SOURCE_PATH_PREFIX = "e2e/$runId"
    $env:CODE_AGENT_RAG_SOURCE_URL = $sourceUrl
    $env:CODE_AGENT_RAG_SOURCE_COMMIT = $sourceCommit
    $env:CODE_AGENT_RAG_TARGET_INDEX = $targetIndex
    $env:CODE_AGENT_RAG_CORPUS_GENERATION = $corpusGeneration
    $env:CODE_AGENT_RAG_RUN_ID = "rag-e2e-$runId"

    Write-Host "Running real RAG ingest and SearchKnowledge integrations..."
    Invoke-GoTest @("test", "./internal/rag", "./internal/cli", "-run", "TestRealRAGIngestThenSearchKnowledge|TestRealNewAppRAGIngestThenSearchKnowledge", "-count=1", "-v")
    $completed = $true
    Write-Host "Real MinerU and RAG agent E2E tests passed."
}
finally {
	try {
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
			Remove-Item -LiteralPath $serverStdout, $serverStderr -Force -ErrorAction SilentlyContinue
			$remainingLogs = @(@($serverStdout, $serverStderr) | Where-Object { Test-Path -LiteralPath $_ })
			if ($remainingLogs.Count -ne 0) {
				throw "E2E passed, but temporary server logs could not be removed: $($remainingLogs -join ', ')"
			}
		}
		if (-not $completed) {
            Write-Host "E2E failed. Server logs were retained at:"
            Write-Host "  stdout: $serverStdout"
            Write-Host "  stderr: $serverStderr"
		}
	}
	finally {
		Restore-TrackedEnvironment
		Pop-Location
	}
}
