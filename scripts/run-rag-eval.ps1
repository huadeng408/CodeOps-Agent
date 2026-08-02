# RAG retrieval evaluation runner (plan Task 3).
#
# Runs the offline retrieval evaluation over a qrels + predictions pair and
# writes a redacted JSON report. Requires explicit corpus generation, index
# alias, qrels path, predictions path and output path. The visual path is
# reported as "disabled" unless -VisualDisabled is set to $false.
#
# Usage (from repo root):
#   powershell -ExecutionPolicy Bypass -File scripts/run-rag-eval.ps1 `
#       -QrelsPath data/eval/techdocs/qrels.text.jsonl `
#       -PredictionsPath results/retrieval/bm25-predictions.jsonl `
#       -CorpusGeneration techdocs-2026-07-30-v1 `
#       -IndexAlias knowledge_base_current `
#       -OutputPath results/retrieval/bm25-report.json

param(
    [Parameter(Mandatory = $true)]
    [string]$QrelsPath,
    [Parameter(Mandatory = $true)]
    [string]$PredictionsPath,
    [Parameter(Mandatory = $true)]
    [string]$CorpusGeneration,
    [Parameter(Mandatory = $true)]
    [string]$IndexAlias,
    [Parameter(Mandatory = $true)]
    [string]$OutputPath,
    [bool]$VisualDisabled = $true
)

$ErrorActionPreference = "Stop"

Write-Host "=== RAG retrieval evaluation ==="
Write-Host "  corpus generation: $CorpusGeneration"
Write-Host "  index alias      : $IndexAlias"
Write-Host "  qrels            : $QrelsPath"
Write-Host "  predictions      : $PredictionsPath"
Write-Host "  output           : $OutputPath"
Write-Host "  visual disabled  : $VisualDisabled"

$VisualArgs = "--visual-disabled"
if (-not $VisualDisabled) {
    $VisualArgs = "--no-visual-disabled"
}

python -m orchestrator.eval.runner `
    --qrels-path $QrelsPath `
    --predictions-path $PredictionsPath `
    --corpus-generation $CorpusGeneration `
    --index-alias $IndexAlias `
    --output-path $OutputPath `
    $VisualArgs

if ($LASTEXITCODE -ne 0) {
    Write-Error "retrieval evaluation failed with exit code $LASTEXITCODE"
    exit $LASTEXITCODE
}

Write-Host ""
Write-Host "Evaluation complete. Report: $OutputPath"
exit 0
