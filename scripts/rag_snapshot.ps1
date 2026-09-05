[CmdletBinding()]
param(
    [string]$OutputPath = (Join-Path $PWD ("rag-snapshot-{0}.json" -f (Get-Date -Format "yyyyMMdd-HHmmss"))),
    [int]$DockerTimeoutSeconds = 300
)

. (Join-Path $PSScriptRoot 'rag-agent-e2e-runtime.ps1')
Wait-DockerDaemonReady -TimeoutSeconds $DockerTimeoutSeconds

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

function Invoke-ReadOnlyCommand {
    param([Parameter(Mandatory = $true)][scriptblock]$Command)
    $previousPreference = $ErrorActionPreference
    try {
        $ErrorActionPreference = "Continue"
        $output = & $Command 2>&1
        $exitCode = $LASTEXITCODE
        if ($exitCode -ne 0) {
            $detail = (@($output) | ForEach-Object { "$_" }) -join "`n"
            return @{ error = "command failed with exit code $exitCode"; detail = $detail }
        }
        return @($output | ForEach-Object { "$_" } | Where-Object {
            $_ -notmatch '^mysql: \[Warning\]' -and
            $_ -notmatch '^\s*Container\s+\S+\s+(Creating|Created|Starting|Started|Removing|Removed)\s*$'
        })
    }
    catch {
        return @{ error = $_.Exception.Message }
    }
    finally {
        $ErrorActionPreference = $previousPreference
    }
}

function Invoke-ESGet {
    param([Parameter(Mandatory = $true)][string]$Path)
    try {
        return Invoke-RestMethod -Method Get -Uri ("http://127.0.0.1:9200{0}" -f $Path) -TimeoutSec 20
    }
    catch {
        return @{ error = $_.Exception.Message; path = $Path }
    }
}

function ConvertTo-RedactedObject {
    param([Parameter(Mandatory = $true)]$Value)
    if ($Value -is [System.Collections.IDictionary]) {
        $clean = [ordered]@{}
        foreach ($key in $Value.Keys) {
            if ("$key" -match '(?i)(password|secret|api.?key|access.?key|token|dsn)') {
                $clean[$key] = "[REDACTED]"
            }
            else {
                $clean[$key] = ConvertTo-RedactedObject $Value[$key]
            }
        }
        return $clean
    }
    if ($Value -is [pscustomobject]) {
        $clean = [ordered]@{}
        foreach ($property in $Value.PSObject.Properties) {
            if ($property.Name -match '(?i)(password|secret|api.?key|access.?key|token|dsn)') {
                $clean[$property.Name] = "[REDACTED]"
            }
            else {
                $clean[$property.Name] = ConvertTo-RedactedObject $property.Value
            }
        }
        return $clean
    }
    if ($Value -is [System.Collections.IEnumerable] -and $Value -isnot [string]) {
        return @($Value | ForEach-Object { ConvertTo-RedactedObject $_ })
    }
    return $Value
}

$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    $mysqlRows = Invoke-ReadOnlyCommand {
        docker compose exec -T mysql mysql -ucodeagent -pcodeagent -N -B codeagent -e `
            "SELECT 'knowledge_source', COUNT(*) FROM knowledge_source UNION ALL SELECT 'knowledge_document', COUNT(*) FROM knowledge_document UNION ALL SELECT 'document_vectors', COUNT(*) FROM document_vectors;"
    }
    $mysqlCounts = [ordered]@{}
    if ($mysqlRows -is [System.Collections.IDictionary]) {
        $mysqlCounts = $mysqlRows
    }
    else {
        foreach ($row in $mysqlRows) {
            $parts = "$row" -split "`t", 2
            if ($parts.Count -eq 2) {
                $mysqlCounts[$parts[0]] = [int64]$parts[1]
            }
        }
    }

    $minioRows = Invoke-ReadOnlyCommand {
        docker compose run --rm --no-deps --entrypoint sh minio-init -c `
            "mc alias set snapshot http://minio:9000 minioadmin minioadmin >/dev/null && mc ls --json --recursive snapshot/uploads"
    }
    $minioKeys = @()
    if ($minioRows -is [System.Collections.IDictionary]) {
        $minioKeys = $minioRows
    }
    else {
        foreach ($row in $minioRows) {
            try {
                $item = "$row" | ConvertFrom-Json
                if ($null -ne $item.key) {
                    $minioKeys += [ordered]@{ key = $item.key; size = $item.size; lastModified = $item.lastModified }
                }
            }
            catch { }
        }
    }

    $snapshot = [ordered]@{
        capturedAt = (Get-Date).ToUniversalTime().ToString("o")
        branch = (git branch --show-current).Trim()
        commit = (git rev-parse HEAD).Trim()
        mysql = @{ counts = $mysqlCounts }
        elasticsearch = @{
            oldIndexCount = Invoke-ESGet "/knowledge_base/_count"
            textV2Count = Invoke-ESGet "/knowledge_base_v2_bge_m3/_count"
            mappings = Invoke-ESGet "/_mapping"
            aliases = Invoke-ESGet "/_alias"
        }
        minio = @{ bucket = "uploads"; objects = $minioKeys }
    }

    $redacted = ConvertTo-RedactedObject $snapshot
    $parent = Split-Path -Parent $OutputPath
    if (-not [string]::IsNullOrWhiteSpace($parent)) {
        New-Item -ItemType Directory -Force -Path $parent | Out-Null
    }
    $redacted | ConvertTo-Json -Depth 100 | Set-Content -LiteralPath $OutputPath -Encoding utf8
    Write-Output (Resolve-Path -LiteralPath $OutputPath).Path
}
finally {
    Pop-Location
}
