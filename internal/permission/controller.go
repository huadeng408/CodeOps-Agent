package permission

import (
	"sort"
	"strings"
	"sync"
	"time"
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
	approvalHistory []ApprovalRecord
}

// ApprovalRecord 记录一次用户对工具调用的批准，用于学习允许规则。
// Signature 是从参数中归一化出的可泛化签名（例如 Bash 命令前缀 "go test"
// 或文件工具的目标目录 "/repo/docs"）；Sample 是用于展示的原始命令/路径。
type ApprovalRecord struct {
	Tool      string    `json:"tool"`
	Signature string    `json:"signature,omitempty"`
	Sample    string    `json:"sample,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// maxApprovalHistory 限制内存中保留的批准记录条数（保留最近的若干条）。
const maxApprovalHistory = 200

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

// RecordApproval 记录一次工具批准。它是 ApproveSession 的超集：除了把该工具
// 标记为“本会话已批准”外，还会从 params 中抽取归一化签名，把 (工具, 签名)
// 追加到有界历史中，供 SuggestRules 推导候选允许规则。
//
// 对 AlwaysAsk 级别的工具（例如 Bash），sessionApproved 标记会被 Check 忽略，
// 因此不会意外地整会话放行；只有历史被积累下来用于建议。
func (c *Controller) RecordApproval(tool string, params map[string]any) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}
	signature, sample := GeneralizeSignature(tool, params)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.sessionApproved[tool] = true
	c.approvalHistory = append(c.approvalHistory, ApprovalRecord{
		Tool:      tool,
		Signature: signature,
		Sample:    sample,
		Timestamp: time.Now(),
	})
	if len(c.approvalHistory) > maxApprovalHistory {
		// 只保留最近 maxApprovalHistory 条，丢弃最旧的记录。
		drop := len(c.approvalHistory) - maxApprovalHistory
		c.approvalHistory = append([]ApprovalRecord(nil), c.approvalHistory[drop:]...)
	}
}

// ApprovalHistory 返回有界批准历史的副本（按时间先后顺序）。
func (c *Controller) ApprovalHistory() []ApprovalRecord {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.approvalHistory) == 0 {
		return nil
	}
	out := make([]ApprovalRecord, len(c.approvalHistory))
	copy(out, c.approvalHistory)
	return out
}

// Approvals 是 ApprovalHistory 的别名访问器。
func (c *Controller) Approvals() []ApprovalRecord {
	return c.ApprovalHistory()
}

// RestoreApprovalHistory 用持久化的记录重建批准历史（例如会话恢复时）。
// 它不会触碰 sessionApproved，后者由 RestoreApprovedTools 单独恢复。
func (c *Controller) RestoreApprovalHistory(records []ApprovalRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(records) == 0 {
		c.approvalHistory = nil
		return
	}
	out := make([]ApprovalRecord, 0, len(records))
	for _, rec := range records {
		rec.Tool = strings.TrimSpace(rec.Tool)
		if rec.Tool == "" {
			continue
		}
		out = append(out, rec)
	}
	if len(out) > maxApprovalHistory {
		out = out[len(out)-maxApprovalHistory:]
	}
	c.approvalHistory = out
}

// SuggestRules 扫描近期批准历史，返回候选允许规则。它只负责“建议”，
// 不会自动把规则加入 allowlist；已经被现有 allowlist 覆盖的相同规则会被过滤掉。
func (c *Controller) SuggestRules() []Suggestion {
	c.mu.Lock()
	history := append([]ApprovalRecord(nil), c.approvalHistory...)
	allowlist := c.allowlist
	c.mu.Unlock()

	candidates := DefaultSuggester.Suggest(history)
	out := make([]Suggestion, 0, len(candidates))
	for _, sug := range candidates {
		if suggestionAlreadyCovered(allowlist, sug) {
			continue
		}
		out = append(out, sug)
	}
	return out
}

func suggestionAlreadyCovered(allowlist []AllowRule, sug Suggestion) bool {
	for _, rule := range allowlist {
		if rule.Tool == sug.Tool && rule.Pattern == sug.Pattern {
			return true
		}
	}
	return false
}
