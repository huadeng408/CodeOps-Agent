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

function Resolve-DockerCli {
    [CmdletBinding()]
    param()

    $command = Get-Command docker.exe -ErrorAction SilentlyContinue
    if ($null -eq $command) {
        $command = Get-Command docker -ErrorAction SilentlyContinue
    }
    if ($null -ne $command -and $command.CommandType -eq 'Application') {
        return $command.Source
    }

    $candidates = @()
    if (-not [string]::IsNullOrWhiteSpace($env:ProgramFiles)) {
        $candidates += Join-Path $env:ProgramFiles 'Docker\Docker\resources\bin\docker.exe'
    }
    if (-not [string]::IsNullOrWhiteSpace(${env:ProgramFiles(x86)})) {
        $candidates += Join-Path ${env:ProgramFiles(x86)} 'Docker\Docker\resources\bin\docker.exe'
    }
    if (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        $candidates += Join-Path $env:LOCALAPPDATA 'Docker\wsl\docker.exe'
    }
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return [System.IO.Path]::GetFullPath($candidate)
        }
    }
    throw 'Docker CLI was not found in PATH or a standard Docker Desktop installation path'
}

function Start-DockerDesktopIfNeeded {
    [CmdletBinding()]
    param()

    if ($env:OS -ne 'Windows_NT') {
        return
    }
    $running = Get-Process -Name 'Docker Desktop' -ErrorAction SilentlyContinue
    if ($null -ne $running) {
        return
    }
    $desktop = Join-Path ${env:ProgramFiles} 'Docker\Docker\Docker Desktop.exe'
    if (-not (Test-Path -LiteralPath $desktop -PathType Leaf)) {
        $desktop = Join-Path $env:LOCALAPPDATA 'Docker\Docker Desktop.exe'
    }
    if (Test-Path -LiteralPath $desktop -PathType Leaf) {
        Start-Process -FilePath $desktop -WindowStyle Hidden | Out-Null
    }
}

function Wait-DockerDaemonReady {
    [CmdletBinding()]
    param(
        [int]$TimeoutSeconds = 60
    )

    if ($TimeoutSeconds -le 0) {
        throw "Docker readiness timeout must be greater than zero"
    }

    $dockerCli = Resolve-DockerCli
    $dockerDirectory = Split-Path -Parent $dockerCli
    $pathSeparator = [IO.Path]::PathSeparator
    $pathEntries = @($env:PATH -split [regex]::Escape([string]$pathSeparator))
    if ($pathEntries -notcontains $dockerDirectory) {
        $env:PATH = "$dockerDirectory$pathSeparator$env:PATH"
    }

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $lastFailure = "unknown error"
    Start-DockerDesktopIfNeeded
    do {
        try {
            & $dockerCli info --format '{{.ServerVersion}}' 1>$null 2>$null
            if ($LASTEXITCODE -eq 0) {
                Write-Host "ready: Docker daemon"
                return
            }
            $lastFailure = "docker info exited with code $LASTEXITCODE"
        }
        catch {
            $lastFailure = $_.Exception.Message
        }
        Start-Sleep -Seconds 2
    } while ((Get-Date) -lt $deadline)

    throw "timed out waiting for Docker daemon after $TimeoutSeconds seconds ($lastFailure)"
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
