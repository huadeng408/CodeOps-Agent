package permission

import (
	"sort"
	"strings"
	"sync"
)

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
	denylist        []AllowRule
	sessionApproved map[string]bool
}

var DefaultPermissions = map[string]Level{
	"Read":      AutoAllow,
	"Glob":      AutoAllow,
	"Grep":      AutoAllow,
	"Edit":      AskSession,
	"Write":     AskSession,
	"Bash":      AlwaysAsk,
	"Git":       AskSession,
	"WebFetch":  AskSession,
	"WebSearch": AskSession,
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

func NewControllerWithRules(levels map[string]Level, allowlist []AllowRule, denylist []AllowRule) *Controller {
	controller := NewController(levels, allowlist)
	controller.denylist = denylist
	return controller
}

func (c *Controller) Check(tool string, params map[string]any) Decision {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, rule := range c.denylist {
		if rule.Matches(tool, params) {
			return Deny
		}
	}

	for _, rule := range c.allowlist {
		if rule.Matches(tool, params) {
			return Approve
		}
	}

	level := c.levelLocked(tool)
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

func (c *Controller) Level(tool string) Level {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.levelLocked(tool)
}

func (c *Controller) levelLocked(tool string) Level {
	if c.levels == nil {
		return AskSession
	}
	level, ok := c.levels[tool]
	if !ok {
		return AskSession
	}
	return level
}

func (c *Controller) ApproveSession(tool string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}
	c.sessionApproved[tool] = true
}

func (c *Controller) ApprovedTools() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]string, 0, len(c.sessionApproved))
	for tool, approved := range c.sessionApproved {
		if approved {
			out = append(out, tool)
		}
	}
	sort.Strings(out)
	return out
}

func (c *Controller) RestoreApprovedTools(tools []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sessionApproved = make(map[string]bool)
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			c.sessionApproved[tool] = true
		}
	}
}
