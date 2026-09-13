$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$start = Join-Path $PSScriptRoot 'start-interview.ps1'

try {
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $start
    if ($LASTEXITCODE -ne 0) { throw "项目启动脚本退出码 $LASTEXITCODE" }
    Start-Process 'http://127.0.0.1:3000/'
    Add-Type -AssemblyName PresentationFramework
    [System.Windows.MessageBox]::Show('CodeOps-Agent 前后端已启动。浏览器已打开 http://127.0.0.1:3000/', 'CodeOps-Agent', 'OK', 'Information') | Out-Null
}
catch {
    Add-Type -AssemblyName PresentationFramework
    [System.Windows.MessageBox]::Show("启动失败：$($_.Exception.Message)`n`n请检查项目目录 .tmp/interview-*.log", 'CodeOps-Agent 启动失败', 'OK', 'Error') | Out-Null
    exit 1
}
