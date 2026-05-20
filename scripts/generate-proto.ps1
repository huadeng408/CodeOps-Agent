$ErrorActionPreference = "Stop"

$root = Resolve-Path (Join-Path $PSScriptRoot "..")
$proto = "proto\codeagent\orchestrator.proto"

Push-Location $root
try {
    protoc `
        -I proto `
        --go_out=. `
        --go_opt=module=code-agent `
        --go-grpc_out=. `
        --go-grpc_opt=module=code-agent `
        $proto

    python -m grpc_tools.protoc `
        -I proto `
        --python_out=. `
        --grpc_python_out=. `
        $proto
}
finally {
    Pop-Location
}
