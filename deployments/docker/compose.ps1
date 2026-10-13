[CmdletBinding()]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$ComposeArguments
)

$ErrorActionPreference = 'Stop'
$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '../..')).Path
if (-not $ComposeArguments) { $ComposeArguments = @('ps') }
if ($ComposeArguments[0].StartsWith('-')) {
    throw 'Place the Compose command first; project and configuration overrides are not supported.'
}
if ($ComposeArguments -match '^(-[pf]|--(project-name|project-directory|file|env-file)(=|$))') {
    throw 'Use the fixed localcode project and the configuration in deployments/docker.'
}
if ($ComposeArguments[0] -notin @('ps', 'config', 'version', 'help', 'ls', 'images', 'logs', 'top', 'events')) {
    . (Join-Path $repoRoot 'scripts/rag-agent-e2e-runtime.ps1')
    Wait-DockerDaemonReady
}
& docker compose --project-name localcode --project-directory $PSScriptRoot --file (Join-Path $PSScriptRoot 'compose.yaml') @ComposeArguments
exit $LASTEXITCODE
