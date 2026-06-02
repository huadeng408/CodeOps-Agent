.PHONY: test test-go test-python fmt proto run

test: test-go test-python

test-go:
	go test ./...

test-python:
	pytest -q

fmt:
	gofmt -w cmd internal tests/go
	python -m compileall orchestrator codeagent tests >/dev/null

proto:
	protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/codeagent/orchestrator.proto
	mv proto/codeagent/orchestrator.pb.go gen/codeagentpb/orchestrator.pb.go
	mv proto/codeagent/orchestrator_grpc.pb.go gen/codeagentpb/orchestrator_grpc.pb.go
	python -m grpc_tools.protoc -Iproto --python_out=. --grpc_python_out=. proto/codeagent/orchestrator.proto

run:
	go run ./cmd/agent
