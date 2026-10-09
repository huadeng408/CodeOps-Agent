$ErrorActionPreference = 'Stop'
if ($env:CODE_AGENT_RUN_LOCAL_CORE_E2E -ne '1') {
    Write-Output 'SKIP: set CODE_AGENT_RUN_LOCAL_CORE_E2E=1'
    exit 0
}
$repoRoot = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..\..'))
. (Join-Path $repoRoot 'scripts\rag-agent-e2e-runtime.ps1')
Wait-DockerDaemonReady
$runId = 'full-stack-services-' + [guid]::NewGuid().ToString('N')
$artifactDir = Join-Path $repoRoot ('output\playwright\' + $runId)
New-Item -ItemType Directory -Path $artifactDir | Out-Null
$servicePassword = [Convert]::ToHexString([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
$owned = @()
$saved = @{}
$exitCode = 1
try {
    foreach ($name in @('MYSQL_ROOT_PASSWORD', 'MYSQL_DATABASE', 'MINIO_ROOT_USER', 'MINIO_ROOT_PASSWORD', 'CODE_AGENT_E2E_PROFILE', 'CODE_AGENT_E2E_MYSQL_DSN', 'CODE_AGENT_E2E_REDIS_ADDR', 'CODE_AGENT_E2E_MINIO_ENDPOINT', 'CODE_AGENT_E2E_MINIO_ACCESS_KEY', 'CODE_AGENT_E2E_MINIO_SECRET_KEY')) {
        $saved[$name] = [Environment]::GetEnvironmentVariable($name, 'Process')
    }
    $env:MYSQL_ROOT_PASSWORD = $servicePassword
    $env:MYSQL_DATABASE = 'codeops_e2e'
    $env:MINIO_ROOT_USER = 'codeops-e2e'
    $env:MINIO_ROOT_PASSWORD = $servicePassword
    $services = @(
        @{ Name = 'mysql'; Image = 'mysql:8.4'; Port = 3306; Args = @('--env', 'MYSQL_ROOT_PASSWORD', '--env', 'MYSQL_DATABASE') },
        @{ Name = 'redis'; Image = 'redis:7.4'; Port = 6379; Args = @() },
        @{ Name = 'minio'; Image = 'minio/minio:RELEASE.2025-04-22T22-12-26Z'; Port = 9000; Args = @('--env', 'MINIO_ROOT_USER', '--env', 'MINIO_ROOT_PASSWORD') }
    )
    foreach ($service in $services) {
        $image = docker image inspect --format '{{.Id}}' $service.Image
        if ($LASTEXITCODE -ne 0) { throw 'Pinned service image is not cached; no implicit download' }
        $name = $runId + '-' + $service.Name
        $arguments = @('run', '-d', '--name', $name, '-p', ('127.0.0.1::' + $service.Port)) + $service.Args + @($image)
        if ($service.Name -eq 'minio') { $arguments += @('server', '/data') }
        $id = docker @arguments
        if ($LASTEXITCODE -ne 0) { throw ('Failed to start isolated ' + $service.Name) }
        $owned += @{ Name = $name; Id = $id; ImageId = $image; Service = $service.Name }
        $bindings = docker port $name ($service.Port.ToString() + '/tcp')
        if ($LASTEXITCODE -ne 0) { throw 'Could not resolve isolated service port' }
        $service.HostPort = $bindings.Trim().Split(':')[-1]
    }
    $mysql = $owned | Where-Object Service -eq 'mysql'
    $ready = $false
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        docker exec $mysql.Name mysqladmin ping --silent 2>$null | Out-Null
        if ($LASTEXITCODE -eq 0) { $ready = $true; break }
        Start-Sleep -Milliseconds 500
    }
    if (-not $ready) { throw 'Isolated MySQL readiness timed out' }
    $env:CODE_AGENT_E2E_PROFILE = 'full-stack'
    $env:CODE_AGENT_E2E_MYSQL_DSN = 'root:' + $servicePassword + '@tcp(127.0.0.1:' + $services[0].HostPort + ')/codeops_e2e?charset=utf8mb4&parseTime=True&loc=Local'
    $env:CODE_AGENT_E2E_REDIS_ADDR = '127.0.0.1:' + $services[1].HostPort
    $env:CODE_AGENT_E2E_MINIO_ENDPOINT = '127.0.0.1:' + $services[2].HostPort
    $env:CODE_AGENT_E2E_MINIO_ACCESS_KEY = $env:MINIO_ROOT_USER
    $env:CODE_AGENT_E2E_MINIO_SECRET_KEY = $servicePassword
    & node (Join-Path $PSScriptRoot 'local_core_browser.mjs')
    $exitCode = $LASTEXITCODE
} finally {
    foreach ($service in $owned) {
        try {
            docker stop $service.Id | Out-Null
            $service.StopExitCode = $LASTEXITCODE
            $service.StopState = if ($LASTEXITCODE -eq 0) { 'stopped' } else { 'unknown' }
        } catch {
            $service.StopExitCode = 1
            $service.StopState = 'unknown'
        }
        if ($service.StopExitCode -ne 0) { $exitCode = 1 }
    }
    foreach ($name in $saved.Keys) { [Environment]::SetEnvironmentVariable($name, $saved[$name], 'Process') }
    @{ RunId = $runId; ExitCode = $exitCode; Services = $owned; DataDisposition = 'Preserved; stop outcome recorded per owned container' } |
        ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $artifactDir 'services.json') -Encoding utf8
}
exit $exitCode
