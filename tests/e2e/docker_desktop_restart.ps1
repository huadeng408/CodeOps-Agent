param([int]$Rounds = 3)

$ErrorActionPreference = 'Stop'
if ($env:CODE_AGENT_RUN_DOCKER_RESTART_E2E -ne '1') {
    Write-Output 'SKIP: set CODE_AGENT_RUN_DOCKER_RESTART_E2E=1; this test stops Docker Desktop'
    exit 0
}
if ($env:OS -ne 'Windows_NT' -or $Rounds -lt 3) {
    throw 'This test requires Windows and at least three restart rounds'
}
$repoRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
. (Join-Path $repoRoot 'scripts/rag-agent-e2e-runtime.ps1')
$runId = 'docker-restart-' + [guid]::NewGuid().ToString('N')
$artifactDirectory = Join-Path $repoRoot ('output/playwright/' + $runId)
[IO.Directory]::CreateDirectory($artifactDirectory) | Out-Null
$sourcePaths = @('scripts/rag-agent-e2e-runtime.ps1', 'tests/test_docker_desktop_startup.py',
    'tests/e2e/docker_desktop_restart.ps1', '.github/workflows/ci.yml')
$sourceHashes = @{}
foreach ($path in $sourcePaths) {
    $sourceHashes[$path] = (Get-FileHash -LiteralPath (Join-Path $repoRoot $path) -Algorithm SHA256).Hash.ToLowerInvariant()
}
$sourceHead = (& git -C $repoRoot rev-parse HEAD).Trim()
$results = @()
$exitCode = 1
$probeId = $null
$seedId = $null
$volumeName = $null
$probeState = 'unknown'
$sourceUnchanged = $false
$failure = $null
$existingContainerCount = $null
$existingVolumeCount = $null

function Get-ContainerConfigurationChecksum([string[]]$Ids) {
    if ($Ids.Count -eq 0) { return 'empty' }
    # Credentials in existing container configuration stay in memory, only hash it.
    $raw = @(& $dockerCli inspect @Ids)
    if ($LASTEXITCODE -ne 0) { throw 'Could not inspect existing container configuration' }
    try { $configs = @(($raw -join "`n") | ConvertFrom-Json | Sort-Object Id | Select-Object Id,Name,Image,Config,HostConfig,Mounts) }
    catch { throw 'Could not parse existing container configuration' }
    $bytes = [Text.Encoding]::UTF8.GetBytes(($configs | ConvertTo-Json -Depth 30 -Compress))
    $hasher = [Security.Cryptography.SHA256]::Create()
    try { return [BitConverter]::ToString($hasher.ComputeHash($bytes)).Replace('-', '').ToLowerInvariant() }
    finally { $hasher.Dispose() }
}

try {
    Wait-DockerDaemonReady -TimeoutSeconds 60
    $dockerCli = Resolve-DockerCli
    if (@(& $dockerCli ps -q).Count -ne 0 -or $LASTEXITCODE -ne 0) {
        throw 'Refusing to interrupt running user containers'
    }
    $imageId = & $dockerCli image inspect alpine:3.20 --format '{{.Id}}'
    if ($LASTEXITCODE -ne 0) { throw 'Pinned local Alpine image is unavailable; no implicit download' }
    $beforeContainers = @(& $dockerCli ps -aq --no-trunc | Sort-Object)
    if ($LASTEXITCODE -ne 0) { throw 'Could not record existing containers' }
    $existingContainerCount = $beforeContainers.Count
    $beforeVolumes = @(& $dockerCli volume ls -q | Sort-Object)
    if ($LASTEXITCODE -ne 0) { throw 'Could not record existing volumes' }
    $existingVolumeCount = $beforeVolumes.Count
    $beforeConfiguration = Get-ContainerConfigurationChecksum $beforeContainers
    $settingsPath = Join-Path $env:APPDATA 'Docker/settings-store.json'
    $settingsHash = (Get-FileHash -LiteralPath $settingsPath -Algorithm SHA256).Hash
    $volumeName = & $dockerCli volume create ($runId + '-data')
    if ($LASTEXITCODE -ne 0) { throw 'Could not create owned persistence probe volume' }
    $seedId = & $dockerCli create --pull never --name ($runId + '-seed') --network none --read-only `
        --cap-drop ALL --security-opt no-new-privileges --pids-limit 32 --memory 32m `
        --mount ('type=volume,src=' + $volumeName + ',dst=/proof') $imageId sh -c 'echo codeops-docker-ok > /proof/token'
    if ($LASTEXITCODE -ne 0) { throw 'Could not create owned persistence seed container' }
    & $dockerCli start -a $seedId | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not seed owned persistence probe' }
    $probeId = & $dockerCli create --pull never --name $runId --network none --read-only `
        --cap-drop ALL --security-opt no-new-privileges --pids-limit 32 --memory 32m `
        --cpus 0.25 --user 65534:65534 --mount ('type=volume,src=' + $volumeName + ',dst=/proof,readonly') `
        $imageId cat /proof/token
    if ($LASTEXITCODE -ne 0) { throw 'Could not create isolated container probe' }
    for ($round = 1; $round -le $Rounds; $round++) {
        $result = [ordered]@{ round = $round; status = 'BLOCKED'; inventory_preserved = 'unknown'; configuration_preserved = 'unknown'; settings_preserved = 'unknown'; probe_state = 'unknown' }
        $results += $result
        $probeState = 'unknown'
        if (@(& $dockerCli ps -q).Count -ne 0 -or $LASTEXITCODE -ne 0) {
            throw 'Refusing to interrupt a container started during the test'
        }
        & $dockerCli desktop stop --timeout 45
        $result.stop_exit_code = $LASTEXITCODE
        if ($LASTEXITCODE -ne 0) { throw 'Orderly Docker Desktop shutdown failed' }
        if ($null -ne (Get-Process -Name 'Docker Desktop', 'com.docker.backend' -ErrorAction SilentlyContinue)) {
            throw 'Docker processes remain; refusing to force-kill them'
        }
        $timer = [Diagnostics.Stopwatch]::StartNew()
        Wait-DockerDaemonReady -TimeoutSeconds 60
        $timer.Stop()
        $result.startup_ms = $timer.ElapsedMilliseconds
        $result.server_version = & $dockerCli info --format '{{.ServerVersion}}'
        if ($LASTEXITCODE -ne 0) { throw 'Restarted engine did not answer Docker info' }
        $probeOutput = & $dockerCli start -a $probeId
        $result.probe_start_exit_code = $LASTEXITCODE
        $probe = (& $dockerCli inspect $probeId --format '{{json .State}}') | ConvertFrom-Json
        $result.probe_container_exit_code = $probe.ExitCode
        $result.probe_state = $probe.Status
        $probeState = $probe.Status
        if ($LASTEXITCODE -ne 0 -or $result.probe_start_exit_code -ne 0 -or
            $probe.ExitCode -ne 0 -or $probe.Running -or $probe.Status -ne 'exited' -or $probeOutput -ne 'codeops-docker-ok') {
            throw 'Restarted engine could not execute the isolated container'
        }
        $result.owned_volume_content_recovered = $true
        $containers = @(& $dockerCli ps -aq --no-trunc | Where-Object { $_ -notin @($probeId, $seedId) } | Sort-Object)
        if ($LASTEXITCODE -ne 0 -or ($containers -join ',') -cne ($beforeContainers -join ',')) {
            throw 'Existing container inventory changed'
        }
        $volumes = @(& $dockerCli volume ls -q | Where-Object { $_ -ne $volumeName } | Sort-Object)
        if ($LASTEXITCODE -ne 0 -or ($volumes -join ',') -cne ($beforeVolumes -join ',')) {
            throw 'Existing volume inventory changed'
        }
        $result.inventory_preserved = $true
        if ((Get-ContainerConfigurationChecksum $beforeContainers) -cne $beforeConfiguration) {
            throw 'Existing container configuration changed'
        }
        $result.configuration_preserved = $true
        if ((Get-FileHash -LiteralPath $settingsPath -Algorithm SHA256).Hash -cne $settingsHash) {
            throw 'Docker settings changed'
        }
        $result.settings_preserved = $true
        $result.status = 'VERIFIED'
        Write-Output ("PASS: cold start {0}/{1}, {2}ms, real offline container exit 0" -f $round, $Rounds, $result.startup_ms)
    }
    foreach ($path in $sourcePaths) {
        if ((Get-FileHash -LiteralPath (Join-Path $repoRoot $path) -Algorithm SHA256).Hash.ToLowerInvariant() -cne $sourceHashes[$path]) {
            throw 'Source changed during the runtime test'
        }
    }
    $sourceUnchanged = $true
    $exitCode = 0
}
catch {
    $failure = $_.Exception.Message
    Write-Output ('FAILED: ' + $failure)
}
finally {
    [ordered]@{
        run_id = $runId
        source_head = $sourceHead
        source_sha256 = $sourceHashes
        source_unchanged = $sourceUnchanged
        command = 'CODE_AGENT_RUN_DOCKER_RESTART_E2E=1 powershell.exe -NoProfile -ExecutionPolicy Bypass -File tests/e2e/docker_desktop_restart.ps1 -Rounds ' + $Rounds
        planned_rounds = $Rounds
        completed_rounds = @($results | Where-Object { $_.status -eq 'VERIFIED' }).Count
        exit_code = $exitCode
        rounds = $results
        image_id = $imageId
        probe_container_id = $probeId
        probe_state = $probeState
        seed_container_id = $seedId
        owned_volume = $volumeName
        failure = $failure
        existing_container_count = $existingContainerCount
        existing_volume_count = $existingVolumeCount
        existing_configuration_sha256 = $beforeConfiguration
        disposition = 'No deletion commands; owned probes and volume retained; preservation checks recorded per round'
        content_scope = 'Owned marker checks recorded per round; existing volume contents not read or hashed'
        trace_scope = 'Docker lifecycle, not Agent model/Go/Python trace'
        model_calls = 0
    } | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $artifactDirectory 'receipt.json') -Encoding utf8
    Write-Output ('receipt=' + (Join-Path $artifactDirectory 'receipt.json'))
}
exit $exitCode
