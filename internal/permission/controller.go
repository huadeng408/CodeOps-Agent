package permission

import "sync"

type Level int

const (
	AutoAllow Level = iota
	AskSession
	AlwaysAsk
)

type Decision int

const (
	Approve Decision = iota
	AskUser
	Deny
)

type Controller struct {
	mu              sync.Mutex
	levels          map[string]Level
	allowlist       []AllowRule
	sessionApproved map[string]bool
}

var DefaultPermissions = map[string]Level{
	"Read":     AutoAllow,
	"Glob":     AutoAllow,
	"Grep":     AutoAllow,
	"Edit":     AskSession,
	"Write":    AskSession,
	"Bash":     AlwaysAsk,
	"Git":      AskSession,
	"WebFetch": AskSession,
}

func NewController(levels map[string]Level, allowlist []AllowRule) *Controller {
	if levels == nil {
		levels = DefaultPermissions
	}
	return &Controller{
		levels:          levels,
		allowlist:       allowlist,
		sessionApproved: make(map[string]bool),
	}
}

func (c *Controller) Check(tool string, params map[string]any) Decision {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, rule := range c.allowlist {
		if rule.Matches(tool, params) {
			return Approve
		}
	}

	level := c.levels[tool]
	switch level {
	case AutoAllow:
		return Approve
	case AskSession:
		if c.sessionApproved[tool] {
			return Approve
		}
		return AskUser
	case AlwaysAsk:
		return AskUser
	default:
		return AskUser
	}
}

func (c *Controller) ApproveSession(tool string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sessionApproved[tool] = true
}
