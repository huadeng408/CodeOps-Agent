# Preserve the existing test entry while Docker owns its resources and data.
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot '../../deployments/docker/full-stack-test.ps1')
exit $LASTEXITCODE
