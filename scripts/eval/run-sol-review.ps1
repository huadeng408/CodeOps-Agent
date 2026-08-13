# GPT-5.6 Sol dual-pass independent AI review — Phase 3 driver.
#
# Runs two independent review passes (A + B) over every query/qrel pair using
# GPT-5.6 Sol via BeeAPI (CC-Switch local proxy), then deterministically
# arbitrates the two passes into AI_REVIEWED / DISPUTED statuses.
#
# By default runs the full pipeline: Pass A → Pass B → Arbitrate → Summary.
# Use -Only to run a single stage (useful when the LLM or ES fails mid-pass
# and you need to resume or just re-arbitrate).
#
# PREREQUISITES:
#   1. Elasticsearch running with the target index populated.
#   2. BeeAPI key valid and CC-Switch local proxy running at https://beeapi.ai.
#
# Usage (from repo root):
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/eval/run-sol-review.ps1
#
#   # Resume a failed pass:
#   powershell ... -File scripts/eval/run-sol-review.ps1 -Only passA -Resume

param(
    [string]$QrelsPath = "data/eval/techdocs/qrels.text.jsonl",
    [string]$QueriesPath = "data/eval/techdocs/queries.text.jsonl",
    [string]$PassAOut = "data/eval/techdocs/qrels.sol-review-pass-a.jsonl",
    [string]$PassBOut = "data/eval/techdocs/qrels.sol-review-pass-b.jsonl",
    [string]$ArbitratedOut = "data/eval/techdocs/qrels.sol-review.jsonl",
    [string]$SummaryPath = "results/eval/techdocs/sol-review-summary.json",
    [string]$EsUrl = "http://127.0.0.1:9200",
    [string]$Index = "knowledge_base_v2_bge_m3",
    [int]$MaxEvidenceChars = 6000,
    [string]$Model = "gpt-5.6-sol",
    [string]$Revision = "unknown",
    [double]$ConfidenceThreshold = 0.7,
    [ValidateRange(1, 10)]
    [int]$Concurrency = 1,
    [string]$Only = $null,          # "passA" | "passB" | "arbitrate" | $null (full pipeline)
    [switch]$Resume = $true,        # skip already-completed rows
    [switch]$DryRun = $false,
    [switch]$Reset = $false         # delete existing sidecars and start fresh
)

$ErrorActionPreference = "Stop"

# ---- Resolve BeeAPI credentials ----

$ApiKey = $env:OPENAI_API_KEY
if (-not $ApiKey) {
    Write-Error "OPENAI_API_KEY environment variable is not set. Set it to your BeeAPI auth token."
    exit 2
}

$BaseUrl = $env:OPENAI_BASE_URL
if (-not $BaseUrl) {
    Write-Error "OPENAI_BASE_URL environment variable is not set. Set it to https://beeapi.ai/v1."
    exit 2
}

# ---- Guard: repo root ----

if (-not (Test-Path "orchestrator/eval/sol_reviewer.py")) {
    Write-Error "orchestrator/eval/sol_reviewer.py not found; run this script from the repo root."
    exit 2
}

if (-not (Test-Path $QrelsPath)) {
    Write-Error "qrels file not found: $QrelsPath"
    exit 2
}
if (-not (Test-Path $QueriesPath)) {
    Write-Error "queries file not found: $QueriesPath"
    exit 2
}

# ---- Reset if requested ----

if ($Reset) {
    foreach ($p in @($PassAOut, $PassBOut, $ArbitratedOut, $SummaryPath)) {
        if (Test-Path $p) {
            Remove-Item $p -Force
        }
    }
    Write-Host "Reset: deleted existing sidecar files"
}

# ---- Ensure output directories ----

foreach ($p in @($PassAOut, $PassBOut, $ArbitratedOut, $SummaryPath)) {
    $dir = Split-Path $p -Parent
    if ($dir) {
        New-Item -ItemType Directory -Force -Path $dir | Out-Null
    }
}

# ---- Build arguments ----

$pyArgs = @(
    "-m", "orchestrator.eval.sol_reviewer",
    "--qrels-path", $QrelsPath,
    "--queries-path", $QueriesPath,
    "--pass-a-out", $PassAOut,
    "--pass-b-out", $PassBOut,
    "--arbitrated-out", $ArbitratedOut,
    "--summary-path", $SummaryPath,
    "--es-url", $EsUrl,
    "--index", $Index,
    "--max-evidence-chars", $MaxEvidenceChars,
    "--model", $Model,
    "--revision", $Revision,
    "--confidence-threshold", $ConfidenceThreshold,
    "--concurrency", $Concurrency
)

if ($Only) {
    $pyArgs += "--only", $Only
}

if (-not $Resume) {
    $pyArgs += "--no-resume"
}

if ($DryRun) {
    $pyArgs += "--dry-run"
}

# ---- Run ----

Write-Host "=== GPT-5.6 Sol Dual-Pass Independent AI Review ==="
Write-Host "  qrels        : $QrelsPath"
Write-Host "  queries      : $QueriesPath"
Write-Host "  pass A out   : $PassAOut"
Write-Host "  pass B out   : $PassBOut"
Write-Host "  arbitrated   : $ArbitratedOut"
Write-Host "  summary      : $SummaryPath"
Write-Host "  ES           : $EsUrl / $Index"
Write-Host "  model        : $Model"
Write-Host "  revision     : $Revision"
Write-Host "  confidence   : $ConfidenceThreshold"
Write-Host "  concurrency  : $Concurrency (relay hard max: 10)"
Write-Host "  only         : $($Only -as [string])"
Write-Host "  resume       : $Resume"
Write-Host "  dry-run      : $DryRun"
Write-Host "  base-url     : $BaseUrl"
Write-Host ""

$env:OPENAI_MODEL = $Model

python @pyArgs

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "FAILED: sol_reviewer exit code $LASTEXITCODE"
    exit $LASTEXITCODE
}

Write-Host ""
Write-Host "=== Done ==="

# Print summary snippet if available
if (Test-Path $SummaryPath) {
    Write-Host ""
    $summary = Get-Content $SummaryPath -Raw | ConvertFrom-Json
    Write-Host "total queries  : $($summary.total_queries)"
    Write-Host "AI_REVIEWED    : $($summary.ai_reviewed)"
    Write-Host "DISPUTED       : $($summary.disputed)"
    if ($summary.dispute_reasons) {
        Write-Host "dispute reasons:"
        $summary.dispute_reasons.PSObject.Properties | ForEach-Object {
            Write-Host "  $($_.Name): $($_.Value)"
        }
    }
}

exit 0
