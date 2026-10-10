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

function Move-StaleDockerRuntimeSockets {
    $localRoot = [IO.Path]::GetFullPath($env:LOCALAPPDATA)
    $runtimes = @(
        @{ Parent = 'Docker'; Name = 'run'; Entries = @('dockerInference', 'userAnalyticsOtlpHttp.sock') },
        @{ Parent = ''; Name = 'docker-secrets-engine'; Entries = @('engine.sock') }
    )
    foreach ($runtime in $runtimes) {
        $parentDirectory = $localRoot
        if ($runtime.Parent) { $parentDirectory = Join-Path $localRoot $runtime.Parent }
        $runtimeDirectory = Join-Path $parentDirectory $runtime.Name
        if (-not [IO.Directory]::Exists($runtimeDirectory)) { continue }
        # Move only transient IPC, never Docker settings, WSL disks or workload data.
        foreach ($directory in @($localRoot, $parentDirectory, $runtimeDirectory)) {
            $attributes = [IO.File]::GetAttributes($directory)
            if (($attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) {
                throw 'Refusing to archive a redirected Docker runtime directory'
            }
        }
        $entries = @([IO.Directory]::EnumerateFileSystemEntries($runtimeDirectory))
        if ($entries.Count -eq 0) { continue }
        foreach ($entry in $entries) {
            if ([IO.Path]::GetFileName($entry) -cnotin $runtime.Entries) {
                throw 'Docker runtime contains an unexpected entry; preserve it for manual diagnosis'
            }
            try {
                $attributes = [IO.File]::GetAttributes($entry)
                if (($attributes -band [IO.FileAttributes]::Directory) -ne 0 -or
                    [IO.FileInfo]::new($entry).Length -ne 0) {
                    throw 'Refusing to archive non-socket Docker runtime content'
                }
            }
            catch {
                # Windows may reject metadata on an abandoned AF_UNIX endpoint (1920).
                if ($null -eq $_.Exception.InnerException -or
                    ($_.Exception.InnerException.HResult -band 65535) -ne 1920) {
                    throw
                }
            }
        }
        if ($null -ne (Get-Process -Name 'Docker Desktop', 'com.docker.backend' -ErrorAction SilentlyContinue)) {
            throw 'Docker started during IPC inspection; refusing to move its runtime directory'
        }
        $archiveName = $runtime.Name + '.archive-' + [guid]::NewGuid().ToString('N')
        $archiveDirectory = Join-Path $parentDirectory $archiveName
        Move-Item -LiteralPath $runtimeDirectory -Destination $archiveDirectory -ErrorAction Stop
        Write-Host "preserved: stale Docker IPC in $archiveName"
    }
}

function Start-DockerDesktopIfNeeded {
    [CmdletBinding()]
    param()

    if ($env:OS -ne 'Windows_NT') {
        return
    }
    $startupLock = [Threading.Mutex]::new($false, 'Local\CodeOpsAgent.DockerDesktopStartup')
    $ownsLock = $false
    try {
        try { $ownsLock = $startupLock.WaitOne(0) }
        catch [Threading.AbandonedMutexException] { $ownsLock = $true }
        if (-not $ownsLock) { return }
        $running = Get-Process -Name 'Docker Desktop', 'com.docker.backend' -ErrorAction SilentlyContinue
        if ($null -ne $running) {
            return
        }
        $desktop = Join-Path ${env:ProgramFiles} 'Docker\Docker\Docker Desktop.exe'
        if (-not (Test-Path -LiteralPath $desktop -PathType Leaf)) {
            $desktop = Join-Path $env:LOCALAPPDATA 'Docker\Docker Desktop.exe'
        }
        if (Test-Path -LiteralPath $desktop -PathType Leaf) {
            Move-StaleDockerRuntimeSockets
            Start-Process -FilePath $desktop -WindowStyle Hidden | Out-Null
        }
    }
    finally {
        if ($ownsLock) { $startupLock.ReleaseMutex() }
        $startupLock.Dispose()
    }
}

function Use-DockerDesktopLinuxContext {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$DockerCli
    )

    # Docker Desktop exposes the Linux engine as a named context. Inherited
    # DOCKER_HOST/TLS variables take precedence over that context and can point
    # at a stale named pipe or missing certificate directory, which makes a
    # healthy Desktop daemon look unavailable. Clear them on every platform:
    # PowerShell 7 on Linux is a supported runner and has the same precedence
    # rules as Windows PowerShell. Keep them cleared for the probe when no
    # Desktop context exists so the default Unix socket can be used.
    foreach ($name in @('DOCKER_HOST', 'DOCKER_TLS_VERIFY', 'DOCKER_CERT_PATH')) {
        Remove-Item -LiteralPath ("Env:{0}" -f $name) -ErrorAction SilentlyContinue
    }
    $contextNames = @(& $DockerCli context ls --format '{{.Name}}' 2>$null)
    if ($LASTEXITCODE -ne 0 -or $contextNames -notcontains 'desktop-linux') {
        return $null
    }
    $env:DOCKER_CONTEXT = 'desktop-linux'
    return 'desktop-linux'
}

function ConvertTo-NativeArgument {
    param([AllowEmptyString()][string]$Argument)

    if ($Argument.Length -gt 0 -and $Argument -notmatch '[\s"]') {
        return $Argument
    }

    $builder = [System.Text.StringBuilder]::new()
    $null = $builder.Append('"')
    $backslashes = 0
    foreach ($character in $Argument.ToCharArray()) {
        if ($character -eq '\') {
            $backslashes++
            continue
        }
        if ($character -eq '"') {
            $escaped = ('\' * ($backslashes * 2 + 1)) -join ''
            $null = $builder.Append($escaped)
            $null = $builder.Append('"')
            $backslashes = 0
            continue
        }
        if ($backslashes -gt 0) {
            $literal = ('\' * $backslashes) -join ''
            $null = $builder.Append($literal)
            $backslashes = 0
        }
        $null = $builder.Append($character)
    }
    if ($backslashes -gt 0) {
        $trailing = ('\' * ($backslashes * 2)) -join ''
        $null = $builder.Append($trailing)
    }
    $null = $builder.Append('"')
    return $builder.ToString()
}

function Stop-ProcessTree {
    param(
        [System.Diagnostics.Process]$Process,
        [int]$WaitMilliseconds = 10000
    )

    if ($null -eq $Process -or $Process.HasExited) {
        return
    }
    if ($env:OS -eq 'Windows_NT' -and $null -ne (Get-Command taskkill.exe -ErrorAction SilentlyContinue)) {
        & taskkill.exe /PID $Process.Id /T /F 2>$null | Out-Null
    }
    else {
        try { $Process.Kill() } catch { }
    }
    try { $Process.WaitForExit([Math]::Max(1, $WaitMilliseconds)) | Out-Null } catch { }
}

function Invoke-DockerProbe {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$DockerCli,
        [string[]]$ProbeArguments = @(),
        [int]$ProbeTimeoutMilliseconds = 3000
    )

    if ($ProbeTimeoutMilliseconds -le 0) {
        throw 'Docker probe timeout must be greater than zero'
    }

    $startInfo = [System.Diagnostics.ProcessStartInfo]::new()
    $startInfo.FileName = $DockerCli
    $startInfo.UseShellExecute = $false
    $startInfo.CreateNoWindow = $true
    $startInfo.RedirectStandardOutput = $true
    $startInfo.RedirectStandardError = $true
    $startInfo.Arguments = (
        $ProbeArguments | ForEach-Object { ConvertTo-NativeArgument $_ }
    ) -join ' '

    $process = [System.Diagnostics.Process]::new()
    $process.StartInfo = $startInfo
    try {
        if (-not $process.Start()) {
            throw "failed to start Docker CLI: $DockerCli"
        }
        $stdoutTask = $process.StandardOutput.ReadToEndAsync()
        $stderrTask = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit($ProbeTimeoutMilliseconds)) {
            Stop-ProcessTree $process -WaitMilliseconds $ProbeTimeoutMilliseconds
            return [pscustomobject]@{
                TimedOut = $true
                ExitCode = $null
            }
        }
        if (-not $stdoutTask.Wait($ProbeTimeoutMilliseconds) -or
            -not $stderrTask.Wait($ProbeTimeoutMilliseconds)) {
            Stop-ProcessTree $process -WaitMilliseconds $ProbeTimeoutMilliseconds
            return [pscustomobject]@{
                TimedOut = $true
                ExitCode = $null
            }
        }
        return [pscustomobject]@{
            TimedOut = $false
            ExitCode = $process.ExitCode
        }
    }
    finally {
        $process.Dispose()
    }
}

function Wait-DockerDaemonReady {
    [CmdletBinding()]
    param(
        # Docker Desktop may need several minutes for a cold WSL2 engine start.
        # Callers can still pass a shorter budget when they intentionally want
        # a bounded probe (for example, a negative-path contract test).
        [int]$TimeoutSeconds = 300
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
    $dockerContext = Use-DockerDesktopLinuxContext -DockerCli $dockerCli
    $startupAttempted = $false
    do {
        try {
            $probeArguments = @('info', '--format', '{{.ServerVersion}}')
            if (-not [string]::IsNullOrWhiteSpace($dockerContext)) {
                $probeArguments = @('--context', $dockerContext) + $probeArguments
            }
            $remainingMilliseconds = [int][Math]::Max(1, ((
                $deadline - (Get-Date)
            ).TotalMilliseconds))
            $probeTimeoutMilliseconds = [int][Math]::Min(3000, $remainingMilliseconds)
            $probe = Invoke-DockerProbe `
                -DockerCli $dockerCli `
                -ProbeArguments $probeArguments `
                -ProbeTimeoutMilliseconds $probeTimeoutMilliseconds
            if ($probe.TimedOut) {
                $lastFailure = "docker probe timed out after ${probeTimeoutMilliseconds}ms"
            }
            elseif ($probe.ExitCode -eq 0) {
                Write-Host "ready: Docker daemon"
                return
            }
            else {
                $lastFailure = "docker info exited with code $($probe.ExitCode)"
            }
        }
        catch {
            $lastFailure = $_.Exception.Message
        }
        if (-not $startupAttempted) {
            Start-DockerDesktopIfNeeded
            $startupAttempted = $true
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
