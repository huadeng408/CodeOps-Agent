# preflight-cutover.ps1 -- READ-ONLY cutover gate checks (plan Task 9.2).
#
# Verifies, before any alias cutover:
#   - MySQL document count (read-only SELECT, command configurable)
#   - MinIO object count (read-only listing, command configurable)
#   - ES cluster health, legacy/v2 index existence, v2 chunk count
#   - ES mapping/model contract: v2 `vector` must be dense_vector with
#     exactly -ExpectedVectorDims dims (KnowledgeV2Mapping(1024))
#   - alias state: -ReadAlias must currently point at -ExpectedAliasTarget
#     (pre-cutover expectation is the legacy index)
#   - source consistency / orphans: distinct document_id in v2 vs MySQL
#     document count (approximate via a size-capped terms aggregation)
#   - embedding service /health: dimensions and pinned model_revision
#   - contamination report (-ContaminationReport): blocking if any
#     high-similarity un-reviewed item exists
#   - metrics/trace backend reachability (-PhoenixUrl): SKIPs gracefully
#     when unreachable (never blocks on it)
#
# The script performs ONLY read operations: HTTP GET/HEAD, SELECT queries,
# object listing. It never writes to MySQL, MinIO, Elasticsearch, the
# aliases endpoint, or any file. Pipe the output through Tee-Object to save
# evidence (see docs/releases/RAG-CUTOVER-techdocs-v2.md section 2).
#
# Contamination report format (JSONL, one object per line):
#   {"id": "<chunk or doc id>", "source": "<path>",
#    "similarity": 0.91, "reviewed": false}
# Any line with similarity >= -ContaminationSimilarityThreshold and
# reviewed != true blocks the cutover.
#
# Usage (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts/rag/preflight-cutover.ps1 `
#       -ContaminationReport results/contamination/report-2026-08-02.jsonl
#
# Exit code: 0 when every check PASSes (SKIP allowed), 1 otherwise. The
# checklist of PASS/FAIL/SKIP items is printed at the end.

param(
    [string]$EsBaseUrl = "http://127.0.0.1:9200",
    [string]$LegacyIndex = "knowledge_base",
    [string]$V2Index = "knowledge_base_v2_bge_m3",
    [string]$ReadAlias = "knowledge_base_current",
    [string]$ExpectedAliasTarget = "knowledge_base",
    [int]$ExpectedVectorDims = 1024,
    [int]$MinEsChunkCount = 1,
    [string]$EmbeddingServiceUrl = "http://127.0.0.1:8009",
    # Pinned immutable BGE-M3 revision (verified against local weights,
    # 2026-08-03: HF commit 5617a9f6, 5 config files SHA-256 MATCH).
    [string]$ExpectedModelRevision = "BAAI/bge-m3@5617a9f61b028005a4858fdac845db406aefb181",
    [string]$MySqlHost = "127.0.0.1",
    [int]$MySqlPort = 3306,
    [string]$MySqlUser = "codeagent",
    [string]$MySqlPassword = "codeagent",
    [string]$MySqlDatabase = "codeagent",
    [string]$MySqlClient = "mysql",
    [string[]]$MySqlClientExtraArgs = @(),
    [string]$MySqlCountQuery = "SELECT COUNT(*) FROM knowledge_document",
    [string]$MinioEndpoint = "http://127.0.0.1:9000",
    [string]$MinioAlias = "localcode-preflight",
    [string]$MinioBucket = "uploads",
    [string]$MinioCountCommand = "mc",
    [switch]$SkipMinio,
    [string]$ContaminationReport = "",
    [double]$ContaminationSimilarityThreshold = 0.85,
    [string]$PhoenixUrl = "http://127.0.0.1:6006",
    [int]$HttpTimeoutSeconds = 10
)

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"

# Evidence counters shared by the consistency check.
$script:mysqlCount = -1
$script:minioCount = -1
$script:distinctDocs = -1

$results = New-Object System.Collections.Generic.List[object]

function Add-CheckResult {
    param([string]$Name, [string]$Status, [string]$Detail)
    $results.Add(@{ Name = $Name; Status = $Status; Detail = $Detail })
}

function New-EsGet {
    # Read-only GET returning the parsed body, or $null on failure.
    param([string]$Path)
    try {
        return Invoke-RestMethod -Method Get -Uri "$EsBaseUrl$Path" -TimeoutSec $HttpTimeoutSeconds
    }
    catch {
        return $null
    }
}

Write-Host "=== RAG cutover PREFLIGHT (read-only) ==="
Write-Host "  es      : $EsBaseUrl"
Write-Host "  legacy  : $LegacyIndex"
Write-Host "  v2      : $V2Index"
Write-Host "  alias   : $ReadAlias -> $ExpectedAliasTarget"
Write-Host "  dims    : $ExpectedVectorDims"
Write-Host "  model   : $ExpectedModelRevision"
Write-Host "  report  : $ContaminationReport"
Write-Host ""

# --- ES cluster health ------------------------------------------------------
$health = New-EsGet "/_cluster/health"
if ($null -eq $health) {
    Add-CheckResult "es cluster health" "FAIL" "unreachable at $EsBaseUrl"
}
elseif ([string]$health.status -eq "red") {
    Add-CheckResult "es cluster health" "FAIL" "cluster status red"
}
else {
    Add-CheckResult "es cluster health" "PASS" "status=$($health.status) nodes=$($health.number_of_nodes)"
}

# --- ES index existence -----------------------------------------------------
function Test-EsIndexExists {
    param([string]$Index)
    try {
        $null = Invoke-WebRequest -UseBasicParsing -Method Head -Uri "$EsBaseUrl/$Index" -TimeoutSec $HttpTimeoutSeconds
        return $true
    }
    catch {
        return $false
    }
}

if (Test-EsIndexExists $LegacyIndex) {
    Add-CheckResult "legacy index exists" "PASS" $LegacyIndex
}
else {
    Add-CheckResult "legacy index exists" "FAIL" "$LegacyIndex not found (HEAD $EsBaseUrl/$LegacyIndex)"
}

if (Test-EsIndexExists $V2Index) {
    Add-CheckResult "v2 index exists" "PASS" $V2Index
}
else {
    Add-CheckResult "v2 index exists" "FAIL" "$V2Index not found (HEAD $EsBaseUrl/$V2Index)"
}

# --- v2 mapping/model contract ---------------------------------------------
$mapping = New-EsGet "/$V2Index/_mapping"
if ($null -eq $mapping) {
    Add-CheckResult "v2 mapping" "FAIL" "could not read mapping for $V2Index"
}
else {
    $props = $mapping."$V2Index".mappings.properties
    $vectorType = [string]$props.vector.type
    $vectorDims = [int]$props.vector.dims
    if ($vectorType -eq "dense_vector" -and $vectorDims -eq $ExpectedVectorDims) {
        Add-CheckResult "v2 mapping/model" "PASS" "vector=dense_vector dims=$vectorDims (expect $ExpectedVectorDims)"
    }
    else {
        Add-CheckResult "v2 mapping/model" "FAIL" "vector type=$vectorType dims=$vectorDims, want dense_vector/$ExpectedVectorDims (KnowledgeV2Mapping)"
    }
}

# --- alias state ------------------------------------------------------------
$aliasResp = New-EsGet "/_alias/$ReadAlias"
if ($null -eq $aliasResp) {
    Add-CheckResult "alias state" "FAIL" "alias $ReadAlias not found or ES unreachable"
}
else {
    $targets = @($aliasResp.PSObject.Properties.Name | Sort-Object)
    if ($targets.Count -eq 0) {
        Add-CheckResult "alias state" "FAIL" "alias $ReadAlias exists but targets no index"
    }
    elseif ($targets -contains $ExpectedAliasTarget) {
        Add-CheckResult "alias state" "PASS" "$ReadAlias -> $($targets -join ', ')"
    }
    else {
        Add-CheckResult "alias state" "FAIL" "$ReadAlias -> $($targets -join ', '), expected $ExpectedAliasTarget (already cut over?)"
    }
}

# --- ES counts --------------------------------------------------------------
$legacyCount = -1
$legacyResp = New-EsGet "/$LegacyIndex/_count"
if ($null -eq $legacyResp) {
    Add-CheckResult "legacy es count" "FAIL" "could not read $LegacyIndex/_count"
}
else {
    $legacyCount = [long]$legacyResp.count
    Add-CheckResult "legacy es count" "PASS" "$legacyCount chunks"
}

$v2Count = -1
$v2Resp = New-EsGet "/$V2Index/_count"
if ($null -eq $v2Resp) {
    Add-CheckResult "v2 es count" "FAIL" "could not read $V2Index/_count"
}
else {
    $v2Count = [long]$v2Resp.count
    if ($v2Count -ge $MinEsChunkCount) {
        Add-CheckResult "v2 es count" "PASS" "$v2Count chunks (min $MinEsChunkCount)"
    }
    else {
        Add-CheckResult "v2 es count" "FAIL" "$v2Count chunks < min $MinEsChunkCount -- corpus not imported"
    }
}

# --- MySQL count (read-only SELECT) ------------------------------------------
try {
    $mysqlArgs = $MySqlClientExtraArgs + @(
        "-h", $MySqlHost, "-P", "$MySqlPort", "-u", $MySqlUser,
        "--password=$MySqlPassword", "-N", "-e", $MySqlCountQuery, $MySqlDatabase
    )
    $mysqlOut = @(& $MySqlClient @mysqlArgs 2>$null)
    if ($LASTEXITCODE -ne 0) {
        Add-CheckResult "mysql count" "FAIL" "client exit code $LASTEXITCODE (read-only SELECT)"
    }
    elseif (-not [long]::TryParse((($mysqlOut -join "").Trim()), [ref]$script:mysqlCount)) {
        Add-CheckResult "mysql count" "FAIL" "unexpected output: '$($mysqlOut -join ' ')'"
    }
    else {
        Add-CheckResult "mysql count" "PASS" "$script:mysqlCount rows via '$MySqlCountQuery'"
    }
}
catch {
    Add-CheckResult "mysql count" "FAIL" $_.Exception.Message
}

# --- MinIO count (read-only listing) -----------------------------------------
if ($SkipMinio) {
    Add-CheckResult "minio count" "SKIP" "disabled via -SkipMinio"
}
else {
    try {
        $minioOut = @(& $MinioCountCommand ls --recursive --json "$MinioAlias/$MinioBucket" 2>$null)
        if ($LASTEXITCODE -ne 0) {
            Add-CheckResult "minio count" "FAIL" "client exit code $LASTEXITCODE; configure 'mc alias set $MinioAlias $MinioEndpoint' first"
        }
        else {
            $script:minioCount = 0
            foreach ($line in $minioOut) {
                if (-not [string]::IsNullOrWhiteSpace($line)) { $script:minioCount++ }
            }
            Add-CheckResult "minio count" "PASS" "$script:minioCount objects in $MinioBucket"
        }
    }
    catch {
        Add-CheckResult "minio count" "FAIL" "$($_.Exception.Message) (install mc or pass -SkipMinio)"
    }
}

# --- source consistency / orphans (v2 distinct document_id vs MySQL) ---------
# Read-only: one size-capped terms aggregation via curl.exe (GET with body is
# rejected by PowerShell's Invoke-RestMethod). Approximate beyond 20000 docs.
try {
    $aggBody = '{"size":0,"aggs":{"distinct_documents":{"terms":{"field":"document_id","size":20000}}}}'
    $aggRaw = & curl.exe -s -X GET "$EsBaseUrl/$V2Index/_search" -H "Content-Type: application/json" -d $aggBody
    if ($LASTEXITCODE -ne 0) {
        Add-CheckResult "orphan check" "FAIL" "curl.exe aggregation failed with exit code $LASTEXITCODE"
    }
    else {
        $agg = $aggRaw | ConvertFrom-Json
        $buckets = $agg.aggregations.distinct_documents.buckets
        $script:distinctDocs = if ($null -eq $buckets) { 0 } else { @($buckets).Count }
        if ($script:mysqlCount -ge 0) {
            if ($script:distinctDocs -eq $script:mysqlCount) {
                Add-CheckResult "orphan check" "PASS" "v2 distinct document_id=$script:distinctDocs == mysql=$script:mysqlCount"
            }
            else {
                Add-CheckResult "orphan check" "FAIL" "v2 distinct document_id=$script:distinctDocs != mysql=$script:mysqlCount -- possible orphans or un-imported sources"
            }
        }
        else {
            Add-CheckResult "orphan check" "SKIP" "mysql count unavailable; recorded v2 distinct document_id=$script:distinctDocs"
        }
    }
}
catch {
    Add-CheckResult "orphan check" "FAIL" $_.Exception.Message
}

# --- embedding model contract -------------------------------------------------
try {
    $embed = Invoke-RestMethod -Method Get -Uri "$EmbeddingServiceUrl/health" -TimeoutSec $HttpTimeoutSeconds
    $actualDims = if ($null -eq $embed.dimensions) { -1 } else { [int]$embed.dimensions }
    $actualRev = if ($null -eq $embed.model_revision) { "" } else { [string]$embed.model_revision }
    if ($actualDims -eq $ExpectedVectorDims -and $actualRev -eq $ExpectedModelRevision) {
        Add-CheckResult "embedding model" "PASS" "dims=$actualDims revision=$actualRev"
    }
    else {
        Add-CheckResult "embedding model" "FAIL" "dims=$actualDims revision='$actualRev', want dims=$ExpectedVectorDims revision='$ExpectedModelRevision'"
    }
}
catch {
    Add-CheckResult "embedding model" "FAIL" "embedding service unreachable at $EmbeddingServiceUrl"
}

# --- contamination report ------------------------------------------------------
if ([string]::IsNullOrWhiteSpace($ContaminationReport)) {
    Add-CheckResult "contamination" "FAIL" "-ContaminationReport is required"
}
elseif (-not (Test-Path -LiteralPath $ContaminationReport -PathType Leaf)) {
    Add-CheckResult "contamination" "FAIL" "report not found: $ContaminationReport"
}
else {
    $total = 0
    $blocking = 0
    $unparsable = 0
    foreach ($line in Get-Content -LiteralPath $ContaminationReport) {
        if ([string]::IsNullOrWhiteSpace($line)) { continue }
        $total++
        try {
            $item = $line | ConvertFrom-Json
        }
        catch {
            $unparsable++
            continue
        }
        $sim = [double]$item.similarity
        $reviewed = [bool]$item.reviewed
        if ($sim -ge $ContaminationSimilarityThreshold -and -not $reviewed) { $blocking++ }
    }
    if ($unparsable -gt 0) {
        Add-CheckResult "contamination" "FAIL" "$unparsable unparsable line(s) in $ContaminationReport"
    }
    elseif ($blocking -gt 0) {
        Add-CheckResult "contamination" "FAIL" "$blocking high-similarity un-reviewed item(s) block the cutover (threshold $ContaminationSimilarityThreshold)"
    }
    else {
        Add-CheckResult "contamination" "PASS" "$total record(s), 0 blocking (threshold $ContaminationSimilarityThreshold)"
    }
}

# --- metrics/trace basics ------------------------------------------------------
try {
    $null = Invoke-RestMethod -Method Get -Uri $PhoenixUrl -TimeoutSec 5
    Add-CheckResult "metrics/trace" "PASS" "Phoenix reachable at $PhoenixUrl"
}
catch {
    Add-CheckResult "metrics/trace" "SKIP" "Phoenix unreachable at $PhoenixUrl -- skipped gracefully (does not block cutover)"
}

# --- evidence summary ----------------------------------------------------------
Write-Host "=== EVIDENCE SUMMARY (read-only counts) ==="
Write-Host "  mysql knowledge_document : $script:mysqlCount"
Write-Host "  minio objects            : $script:minioCount"
Write-Host "  es legacy chunks         : $legacyCount"
Write-Host "  es v2 chunks             : $v2Count"
Write-Host "  es v2 distinct docs      : $script:distinctDocs"
Write-Host ""

# --- checklist ---------------------------------------------------------------
Write-Host "=== PREFLIGHT CHECKLIST ==="
$failCount = 0
foreach ($r in $results) {
    if ($r.Status -eq "FAIL") { $failCount++ }
    Write-Host ("  {0,-5} {1}: {2}" -f $r.Status, $r.Name, $r.Detail)
}
Write-Host ""
if ($failCount -gt 0) {
    Write-Host "PREFLIGHT FAILED: $failCount failing check(s). Cutover is BLOCKED."
    exit 1
}
Write-Host "PREFLIGHT PASSED: all checks pass."
exit 0
