[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$Project,
    [Parameter(Mandatory)]
    [string]$Query,
    [string]$WorkerUrl = "http://127.0.0.1:37777",
    [Parameter(Mandatory)]
    [string]$ReceiptDirectory
)

$ErrorActionPreference = "Stop"

function Test-LoopbackWorkerUrl {
    param([string]$Value)

    try {
        $uri = [Uri]$Value
    }
    catch {
        return $false
    }

    return $uri.Scheme -eq "http" -and
        $uri.UserInfo.Length -eq 0 -and
        $uri.Host -in @("127.0.0.1", "::1", "localhost")
}

function Get-WorkerVersion {
    $cacheRoot = Join-Path $env:USERPROFILE ".claude\plugins\cache\thedotmack\claude-mem"
    if (-not (Test-Path -LiteralPath $cacheRoot)) {
        return "unknown"
    }

    $versions = Get-ChildItem -LiteralPath $cacheRoot -Directory -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -match "^\d+\.\d+\.\d+$" } |
        Sort-Object { [Version]$_.Name }
    if (-not $versions) {
        return "unknown"
    }
    return $versions[-1].Name
}

function Get-ObservationIds {
    param([object]$SearchResponse)

    $ids = [System.Collections.Generic.List[long]]::new()
    foreach ($item in @($SearchResponse.content)) {
        if ($item.type -ne "text" -or $item.text -isnot [string]) {
            continue
        }
        foreach ($match in [regex]::Matches($item.text, "(?<![A-Za-z0-9_])#([1-9][0-9]*)\b")) {
            $id = [long]$match.Groups[1].Value
            if (-not $ids.Contains($id)) {
                $ids.Add($id)
            }
            if ($ids.Count -eq 3) {
                return $ids.ToArray()
            }
        }
    }
    return $ids.ToArray()
}

if (-not (Test-LoopbackWorkerUrl $WorkerUrl)) {
    throw "WorkerUrl must be a loopback HTTP URL"
}
if ($Project -notmatch "^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$") {
    throw "Project must be a simple project identifier"
}
if ([string]::IsNullOrWhiteSpace($Query) -or $Query.Length -gt 256) {
    throw "Query must contain at most 256 characters"
}

$timer = [System.Diagnostics.Stopwatch]::StartNew()
$result = "BLOCKED"
$citationIds = @()
$failureCategory = $null

try {
    $baseUrl = $WorkerUrl.TrimEnd("/")
    $health = Invoke-RestMethod -Method Get -Uri "$baseUrl/health" -TimeoutSec 2
    if ($health.status -ne "ok") {
        throw "Worker health check did not return ok"
    }

    $encodedProject = [Uri]::EscapeDataString($Project)
    $encodedQuery = [Uri]::EscapeDataString($Query)
    $search = Invoke-RestMethod -Method Get -Uri "$baseUrl/api/search?query=$encodedQuery&project=$encodedProject&limit=3" -TimeoutSec 2
    $candidateIds = Get-ObservationIds $search
    if ($candidateIds.Count -gt 0) {
        $body = @{ ids = @($candidateIds); project = $Project } | ConvertTo-Json -Compress
        $batch = Invoke-RestMethod -Method Post -Uri "$baseUrl/api/observations/batch" -ContentType "application/json" -Body $body -TimeoutSec 2

        $records = if ($null -ne $batch.id) { @($batch) } elseif ($null -ne $batch.observations) { @($batch.observations) } else { @() }
        foreach ($record in $records) {
            if ($record.project -eq $Project -and $record.id -in $candidateIds) {
                $citationIds += [long]$record.id
            }
        }
        $citationIds = @($citationIds | Sort-Object -Unique)
        if ($citationIds.Count -gt 0) {
            $result = "READABLE"
        }
    }
}
catch {
    $failureCategory = $_.Exception.GetType().Name
}
finally {
    $timer.Stop()
    New-Item -ItemType Directory -Force -Path $ReceiptDirectory | Out-Null
    $receipt = [ordered]@{
        worker_version = Get-WorkerVersion
        endpoint = $WorkerUrl.TrimEnd("/")
        elapsed_ms = [int]$timer.ElapsedMilliseconds
        result_count = @($citationIds).Count
        citation_ids = @($citationIds)
        result = if ($failureCategory) { "BLOCKED" } else { $result }
        failure_category = $failureCategory
    }
    $stamp = Get-Date -Format "yyyyMMddHHmmss"
    $receipt | ConvertTo-Json -Depth 3 | Set-Content -LiteralPath (Join-Path $ReceiptDirectory "receipt-$stamp.json") -Encoding utf8
}

if ($failureCategory) {
    exit 1
}
