param(
    [string]$ApiKeyFile = (Join-Path $env:USERPROFILE ("Desktop\api$([char]0x5bc6)$([char]0x94a5).txt"))
)

$ErrorActionPreference = "Stop"
if (-not (Test-Path -LiteralPath $ApiKeyFile)) {
    throw "API key file not found: $ApiKeyFile"
}

$allowed = @{
    "ANTHROPIC_API_KEY" = $true
    "ANTHROPIC_AUTH_TOKEN" = $true
    "ANTHROPIC_BASE_URL" = $true
    "ANTHROPIC_MODEL" = $true
    "ANTHROPIC_MAX_TOKENS" = $true
    "ANTHROPIC_TIMEOUT" = $true
}

foreach ($line in Get-Content -LiteralPath $ApiKeyFile) {
    if ($line -match '^\s*"?([^"=:\s]+)"?\s*[:=]\s*"?([^"\r\n]*)"?\s*,?\s*$') {
        $name = $Matches[1]
        if ($allowed.ContainsKey($name)) {
            [Environment]::SetEnvironmentVariable($name, $Matches[2], "Process")
        }
    }
}

if ([string]::IsNullOrWhiteSpace($env:ANTHROPIC_API_KEY) -and [string]::IsNullOrWhiteSpace($env:ANTHROPIC_AUTH_TOKEN)) {
    throw "The API key file did not provide ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN"
}

$env:CODE_AGENT_RUN_REAL_PROVIDER_E2E = "1"
$env:CODE_AGENT_E2E_PROVIDER = "anthropic"
go test ./tests/e2e -run TestProductionRealProviderRouteE2E -count=1 -v
