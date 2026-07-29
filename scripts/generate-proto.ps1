$ErrorActionPreference = "Stop"

$root = Resolve-Path (Join-Path $PSScriptRoot "..")
$proto = "proto\codeagent\orchestrator.proto"

Push-Location $root
try {
    protoc `
        --go_out=. `
        --go_opt=paths=source_relative `
        --go-grpc_out=. `
        --go-grpc_opt=paths=source_relative `
        $proto
    if ($LASTEXITCODE -ne 0) {
        throw "Go protobuf generation failed with exit code $LASTEXITCODE"
    }

    Move-Item -LiteralPath "proto\codeagent\orchestrator.pb.go" -Destination "gen\codeagentpb\orchestrator.pb.go" -Force
    Move-Item -LiteralPath "proto\codeagent\orchestrator_grpc.pb.go" -Destination "gen\codeagentpb\orchestrator_grpc.pb.go" -Force

    python -m grpc_tools.protoc `
        -I proto `
        --python_out=. `
        --grpc_python_out=. `
        $proto
    if ($LASTEXITCODE -ne 0) {
        throw "Python protobuf generation failed with exit code $LASTEXITCODE"
    }
}
finally {
    Pop-Location
}
