function Invoke-WithOrchestratorSharedSecret {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Secret,
        [Parameter(Mandatory = $true)][scriptblock]$Action
    )

    $previous = Get-Item -LiteralPath Env:ORCHESTRATOR_SHARED_SECRET -ErrorAction SilentlyContinue
    try {
        $env:ORCHESTRATOR_SHARED_SECRET = $Secret
        & $Action
    }
    finally {
        if ($null -ne $previous) {
            $env:ORCHESTRATOR_SHARED_SECRET = $previous.Value
        }
        else {
            Remove-Item -LiteralPath Env:ORCHESTRATOR_SHARED_SECRET -ErrorAction SilentlyContinue
        }
    }
}

function Invoke-WithPaismartInternalToken {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Secret,
        [Parameter(Mandatory = $true)][scriptblock]$Action
    )

    $values = @{
        PAISMART_INTERNAL_TOKEN = $Secret
        PAISMART_EMBEDDING_BASE_URL = "http://127.0.0.1:8009"
        PAISMART_EMBEDDING_MODEL = "BAAI/bge-m3"
        PAISMART_EMBEDDING_DIMENSIONS = "1024"
    }
    $previous = @{}
    foreach ($name in $values.Keys) {
        $previous[$name] = Get-Item -LiteralPath ("Env:{0}" -f $name) -ErrorAction SilentlyContinue
    }
    try {
        foreach ($name in $values.Keys) {
            Set-Item -LiteralPath ("Env:{0}" -f $name) -Value $values[$name]
        }
        & $Action
    }
    finally {
        foreach ($name in $values.Keys) {
            if ($null -ne $previous[$name]) {
                Set-Item -LiteralPath ("Env:{0}" -f $name) -Value $previous[$name].Value
            }
            else {
                Remove-Item -LiteralPath ("Env:{0}" -f $name) -ErrorAction SilentlyContinue
            }
        }
    }
}
