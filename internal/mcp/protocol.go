package mcp

import "encoding/json"

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ErrorObject    `json:"error,omitempty"`
}

type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type ErrorObject struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	Server      string          `json:"server"`
}

type ServerConfig struct {
	Name       string            `json:"name"`
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string  `json:"env,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
}
