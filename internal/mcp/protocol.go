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
	Name               string          `json:"name"`
	Description        string          `json:"description"`
	InputSchema        json.RawMessage `json:"input_schema,omitempty"`
	InputSchemaSHA256  string          `json:"input_schema_sha256"`
	ServerConfigSHA256 string          `json:"server_config_sha256"`
	Server             string          `json:"server"`
}

// ToolBinding is the immutable identity delegated to a child Agent. The
// description is deliberately excluded because it does not affect calls.
type ToolBinding struct {
	Name               string `json:"name"`
	Server             string `json:"server"`
	InputSchemaSHA256  string `json:"input_schema_sha256"`
	ServerConfigSHA256 string `json:"server_config_sha256"`
}

func (t *ToolDefinition) UnmarshalJSON(data []byte) error {
	type alias ToolDefinition
	var raw struct {
		alias
		InputSchemaCamel json.RawMessage `json:"inputSchema,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*t = ToolDefinition(raw.alias)
	if len(t.InputSchema) == 0 && len(raw.InputSchemaCamel) > 0 {
		t.InputSchema = raw.InputSchemaCamel
	}
	return nil
}

func (t ToolDefinition) MarshalJSON() ([]byte, error) {
	type alias ToolDefinition
	raw := struct {
		alias
		InputSchemaCamel json.RawMessage `json:"inputSchema,omitempty"`
	}{
		alias:            alias(t),
		InputSchemaCamel: t.InputSchema,
	}
	raw.alias.InputSchema = nil
	return json.Marshal(raw)
}

type ToolCallResult struct {
	Content []ToolContent `json:"content,omitempty"`
	IsError bool          `json:"isError,omitempty"`
}

type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type ServerConfig struct {
	Name       string            `json:"name"`
	Command    string            `json:"command"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
}
