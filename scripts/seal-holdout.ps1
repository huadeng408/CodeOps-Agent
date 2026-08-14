param(
    [Parameter(Mandatory = $true)][string]$Questions,
    [Parameter(Mandatory = $true)][string]$Qrels,
    [Parameter(Mandatory = $true)][string]$Attestation,
    [Parameter(Mandatory = $true)][string]$Output
)

$ErrorActionPreference = 'Stop'
$repoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $repoRoot

# Keep labels outside the agent-readable sealed output. This script intentionally
# receives absolute external paths from the independent dataset steward.
$python = if (Test-Path 'C:\Python312\python.exe') { 'C:\Python312\python.exe' } else { 'python' }
& $python -c @'
import sys
from orchestrator.eval.holdout import seal_holdout
print(seal_holdout(*sys.argv[1:]))
'@ $Questions $Qrels $Attestation $Output
