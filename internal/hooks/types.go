package hooks

import "context"

type Phase string

const (
	PhaseSessionStart Phase = "session_start"
	PhasePreTool      Phase = "pre_tool"
	PhasePostTool     Phase = "post_tool"
	PhaseSessionEnd   Phase = "session_end"
)

type Context struct {
	SessionID string
	ToolName  string
	Payload   map[string]any
	Metadata  map[string]string
}

type Result struct {
	Values  map[string]any
	Cancel  bool
	Message string
}

type CommandHook struct {
	Phase   Phase
	Matcher string
	Command string
	WorkDir string
	Timeout int
}

type Handler func(context.Context, Context) (Result, error)
