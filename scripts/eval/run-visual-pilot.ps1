# Runs the visual retrieval pilot bake-off: text-only vs page-visual vs
# late-interaction on the same qrels. Produces a JSON report; never creates
# or switches a production visual alias (that requires the quality gate).
#
# Usage (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts/eval/run-visual-pilot.ps1 `
#       -QrelsPath eval/data/vidore/qrels.jsonl `
#       -TextRanked eval/data/vidore/text-ranked.jsonl `
#       -VisualRanked eval/data/vidore/visual-ranked.jsonl `
#       -LateRanked eval/data/vidore/late-ranked.jsonl `
#       -OutDir results/visual-pilot
#
# Ranked files are JSON lines of {query_id, document_id, page_id, element_id?,
# bbox?, score?}. Missing -VisualRanked/-LateRanked disables that path (score
# reported as 0, marked disabled) — the pilot still runs on the paths present.

param(
    [string]$QrelsPath,
    [string]$TextRanked,
    [string]$VisualRanked = "",
    [string]$LateRanked = "",
    [string]$OutDir = "results/visual-pilot"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $QrelsPath)) {
    Write-Error "qrels file not found: $QrelsPath"
    exit 2
}
if (-not (Test-Path $TextRanked)) {
    Write-Error "text ranked file not found: $TextRanked"
    exit 2
}

New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$report = Join-Path $OutDir "visual-pilot-$stamp.json"

$env:VIDORE_QRELS = $QrelsPath
$env:VIDORE_TEXT_RANKED = $TextRanked
$env:VIDORE_VISUAL_RANKED = $VisualRanked
$env:VIDORE_LATE_RANKED = $LateRanked

python -m eval.scripts.visual_pilot_bakeoff --qrels $QrelsPath --text $TextRanked --visual $VisualRanked --late $LateRanked --out $report

if ($LASTEXITCODE -ne 0) {
    Write-Error "bake-off failed with exit code $LASTEXITCODE"
    exit $LASTEXITCODE
}

Write-Host ""
Write-Host "=== VISUAL PILOT REPORT ==="
Get-Content $report -Raw
Write-Host ""
Write-Host "Report: $report"
Write-Host "NOTE: no production visual alias was created or switched (gate required)."
