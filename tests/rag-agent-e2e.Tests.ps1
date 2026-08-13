$repoRoot = Split-Path -Parent $PSScriptRoot
$scriptPath = Join-Path $repoRoot "scripts/rag-agent-e2e.ps1"
$runtimePath = Join-Path $repoRoot "scripts/rag-agent-e2e-runtime.ps1"

Describe "rag-agent-e2e process contract" {
    It "returns nonzero from powershell -File on an early terminating failure" {
        $missingMinerU = Join-Path $TestDrive "missing-mineru.exe"
        $oldSecret = [Environment]::GetEnvironmentVariable("CODE_AGENT_RAG_INTERNAL_SECRET")
        try {
            $env:CODE_AGENT_RAG_INTERNAL_SECRET = "test-only-secret"
            & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $scriptPath `
                -MinerUCommand $missingMinerU -ArtifactRoot ".tmp/rag-agent-e2e-contract-test" *> $null
            $LASTEXITCODE | Should Not Be 0
        }
        finally {
            [Environment]::SetEnvironmentVariable("CODE_AGENT_RAG_INTERNAL_SECRET", $oldSecret)
        }
    }

    It "propagates the RAG secret only during server startup and restores the parent value" {
        . $runtimePath
        $oldRAGSecret = [Environment]::GetEnvironmentVariable("CODE_AGENT_RAG_INTERNAL_SECRET")
        $oldOrchestratorSecret = [Environment]::GetEnvironmentVariable("ORCHESTRATOR_SHARED_SECRET")
        try {
            $env:CODE_AGENT_RAG_INTERNAL_SECRET = "test-only-secret"
            $env:ORCHESTRATOR_SHARED_SECRET = "preexisting-value"
            $childSawMatch = Invoke-WithOrchestratorSharedSecret `
                -Secret $env:CODE_AGENT_RAG_INTERNAL_SECRET `
                -Action {
                    $output = & powershell.exe -NoProfile -Command `
                        "[Environment]::GetEnvironmentVariable('ORCHESTRATOR_SHARED_SECRET') -eq [Environment]::GetEnvironmentVariable('CODE_AGENT_RAG_INTERNAL_SECRET')"
                    if ($LASTEXITCODE -ne 0) {
                        throw "child environment probe failed"
                    }
                    return "$output".Trim()
                }

            $childSawMatch | Should Be "True"
            $env:ORCHESTRATOR_SHARED_SECRET | Should Be "preexisting-value"
        }
        finally {
            [Environment]::SetEnvironmentVariable("CODE_AGENT_RAG_INTERNAL_SECRET", $oldRAGSecret)
            [Environment]::SetEnvironmentVariable("ORCHESTRATOR_SHARED_SECRET", $oldOrchestratorSecret)
        }
    }

    It "restores the parent value when server startup terminates" {
        . $runtimePath
        $oldOrchestratorSecret = [Environment]::GetEnvironmentVariable("ORCHESTRATOR_SHARED_SECRET")
        try {
            $env:ORCHESTRATOR_SHARED_SECRET = "preexisting-value"
            { Invoke-WithOrchestratorSharedSecret -Secret "test-only-secret" -Action { throw "startup failed" } } | Should Throw
            $env:ORCHESTRATOR_SHARED_SECRET | Should Be "preexisting-value"
        }
        finally {
            [Environment]::SetEnvironmentVariable("ORCHESTRATOR_SHARED_SECRET", $oldOrchestratorSecret)
        }
    }

    It "scopes the Python worker token and restores the parent value" {
        . $runtimePath
        $oldWorkerToken = [Environment]::GetEnvironmentVariable("PAISMART_INTERNAL_TOKEN")
        try {
            $env:PAISMART_INTERNAL_TOKEN = "preexisting-value"
            $childSawMatch = Invoke-WithPaismartInternalToken -Secret "test-only-secret" -Action {
                $output = & powershell.exe -NoProfile -Command `
                    "[Environment]::GetEnvironmentVariable('PAISMART_INTERNAL_TOKEN') -eq 'test-only-secret'"
                return "$output".Trim()
            }
            $childSawMatch | Should Be "True"
            $env:PAISMART_INTERNAL_TOKEN | Should Be "preexisting-value"
            Test-Path Env:PAISMART_EMBEDDING_BASE_URL | Should Be $false
            Test-Path Env:PAISMART_EMBEDDING_MODEL | Should Be $false
            Test-Path Env:PAISMART_EMBEDDING_DIMENSIONS | Should Be $false
        }
        finally {
            [Environment]::SetEnvironmentVariable("PAISMART_INTERNAL_TOKEN", $oldWorkerToken)
        }
    }
}
