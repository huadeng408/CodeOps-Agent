package permission

import (
	"code-agent/internal/identity"
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
	sessionApproved map[string]map[string]bool
	approvalHistory map[string][]ApprovalRecord
	currentScope    string
}

const defaultScope = "legacy"

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
	"Read":            AutoAllow,
	"ReadSpill":       AutoAllow,
	"Glob":            AutoAllow,
	"Grep":            AutoAllow,
	"SearchKnowledge": AutoAllow,
	"RecallMemory":    AskSession,
	"Edit":            AskSession,
	"Write":           AskSession,
	"Bash":            AlwaysAsk,
	"Git":             AskSession,
	"WebFetch":        AskSession,
	"WebSearch":       AskSession,
	"JobStart":        AskSession,
	"JobOutput":       AutoAllow,
	"JobList":         AutoAllow,
	"JobKill":         AskSession,
	"JobWrite":        AskSession,
	"JobWait":         AutoAllow,
	"job_start":       AskSession,
	"job_output":      AutoAllow,
	"job_list":        AutoAllow,
	"job_kill":        AskSession,
	"job_write":       AskSession,
	"job_wait":        AutoAllow,
	"SessionFork":     AskSession,
	"SessionRewind":   AskSession,
	"Extension":       AskSession,
	"Attachment":      AskSession,
	"CodeRuntime":     AskSession,
	"LSP":             AskSession,
}

func NewController(levels map[string]Level, allowlist []AllowRule) *Controller {
	if levels == nil {
		levels = DefaultPermissions
	}
	return &Controller{
		levels:          levels,
		allowlist:       allowlist,
		sessionApproved: make(map[string]map[string]bool),
		approvalHistory: make(map[string][]ApprovalRecord),
		currentScope:    defaultScope,
	}
}

func NewControllerWithRules(levels map[string]Level, allowlist []AllowRule, denylist []AllowRule) *Controller {
	controller := NewController(levels, allowlist)
	controller.denylist = denylist
	return controller
}

func (c *Controller) Check(tool string, params map[string]any) Decision {
	return c.check(defaultScopeOrCurrent(c), tool, params)
}

// CheckFor evaluates a tool under an authenticated actor/session scope. An
// invalid actor is fail-closed as AskUser, never as an approval.
func (c *Controller) CheckFor(actor identity.Actor, tool string, params map[string]any) Decision {
	scope, ok := actorScope(actor)
	if !ok {
		return AskUser
	}
	return c.check(scope, tool, params)
}

func (c *Controller) check(scope, tool string, params map[string]any) Decision {
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
		if c.sessionApproved[scope] != nil && c.sessionApproved[scope][tool] {
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
	c.approveSession(defaultScopeOrCurrent(c), tool)
}

// ApproveSessionFor records approval only for the exact actor and session.
func (c *Controller) ApproveSessionFor(actor identity.Actor, tool string) {
	if scope, ok := actorScope(actor); ok {
		c.approveSession(scope, tool)
	}
}

func (c *Controller) approveSession(scope, tool string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}
	if c.sessionApproved[scope] == nil {
		c.sessionApproved[scope] = make(map[string]bool)
	}
	c.sessionApproved[scope][tool] = true
}

func (c *Controller) ApprovedTools() []string {
	return c.approvedTools(defaultScopeOrCurrent(c))
}

// ApprovedToolsFor returns approvals for one actor/session only.
func (c *Controller) ApprovedToolsFor(actor identity.Actor) []string {
	scope, ok := actorScope(actor)
	if !ok {
		return nil
	}
	return c.approvedTools(scope)
}

func (c *Controller) approvedTools(scope string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	approvedTools := c.sessionApproved[scope]
	out := make([]string, 0, len(approvedTools))
	for tool, approved := range approvedTools {
		if approved {
			out = append(out, tool)
		}
	}
	sort.Strings(out)
	return out
}

func (c *Controller) RestoreApprovedTools(tools []string) {
	c.restoreApprovedTools(defaultScopeOrCurrent(c), tools)
}

// RestoreApprovedToolsFor restores only one actor/session's approvals.
func (c *Controller) RestoreApprovedToolsFor(actor identity.Actor, tools []string) {
	if scope, ok := actorScope(actor); ok {
		c.restoreApprovedTools(scope, tools)
	}
}

func (c *Controller) restoreApprovedTools(scope string, tools []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.sessionApproved[scope] = make(map[string]bool)
	for _, tool := range tools {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			c.sessionApproved[scope][tool] = true
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
	c.recordApproval(defaultScopeOrCurrent(c), tool, params)
}

// RecordApprovalFor records an approval and its suggestion history under one
// authenticated actor/session scope.
func (c *Controller) RecordApprovalFor(actor identity.Actor, tool string, params map[string]any) {
	if scope, ok := actorScope(actor); ok {
		c.recordApproval(scope, tool, params)
	}
}

func (c *Controller) recordApproval(scope, tool string, params map[string]any) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}
	signature, sample := GeneralizeSignature(tool, params)

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sessionApproved[scope] == nil {
		c.sessionApproved[scope] = make(map[string]bool)
	}
	c.sessionApproved[scope][tool] = true
	c.approvalHistory[scope] = append(c.approvalHistory[scope], ApprovalRecord{
		Tool:      tool,
		Signature: signature,
		Sample:    sample,
		Timestamp: time.Now(),
	})
	if len(c.approvalHistory[scope]) > maxApprovalHistory {
		// 只保留最近 maxApprovalHistory 条，丢弃最旧的记录。
		drop := len(c.approvalHistory[scope]) - maxApprovalHistory
		c.approvalHistory[scope] = append([]ApprovalRecord(nil), c.approvalHistory[scope][drop:]...)
	}
}

// ApprovalHistory 返回有界批准历史的副本（按时间先后顺序）。
func (c *Controller) ApprovalHistory() []ApprovalRecord {
	return c.approvalHistoryFor(defaultScopeOrCurrent(c))
}

// ApprovalHistoryFor returns a defensive copy for one actor/session scope.
func (c *Controller) ApprovalHistoryFor(actor identity.Actor) []ApprovalRecord {
	scope, ok := actorScope(actor)
	if !ok {
		return nil
	}
	return c.approvalHistoryFor(scope)
}

func (c *Controller) approvalHistoryFor(scope string) []ApprovalRecord {
	c.mu.Lock()
	defer c.mu.Unlock()

	history := c.approvalHistory[scope]
	if len(history) == 0 {
		return nil
	}
	out := make([]ApprovalRecord, len(history))
	copy(out, history)
	return out
}

// Approvals 是 ApprovalHistory 的别名访问器。
func (c *Controller) Approvals() []ApprovalRecord {
	return c.ApprovalHistory()
}

// RestoreApprovalHistory 用持久化的记录重建批准历史（例如会话恢复时）。
// 它不会触碰 sessionApproved，后者由 RestoreApprovedTools 单独恢复。
func (c *Controller) RestoreApprovalHistory(records []ApprovalRecord) {
	c.restoreApprovalHistory(defaultScopeOrCurrent(c), records)
}

// RestoreApprovalHistoryFor restores suggestion history for one scope.
func (c *Controller) RestoreApprovalHistoryFor(actor identity.Actor, records []ApprovalRecord) {
	if scope, ok := actorScope(actor); ok {
		c.restoreApprovalHistory(scope, records)
	}
}

func (c *Controller) restoreApprovalHistory(scope string, records []ApprovalRecord) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(records) == 0 {
		delete(c.approvalHistory, scope)
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
	c.approvalHistory[scope] = out
}

// SuggestRules 扫描近期批准历史，返回候选允许规则。它只负责“建议”，
// 不会自动把规则加入 allowlist；已经被现有 allowlist 覆盖的相同规则会被过滤掉。
func (c *Controller) SuggestRules() []Suggestion {
	return c.suggestRules(defaultScopeOrCurrent(c))
}

// SuggestRulesFor derives suggestions without crossing actor/session scopes.
func (c *Controller) SuggestRulesFor(actor identity.Actor) []Suggestion {
	scope, ok := actorScope(actor)
	if !ok {
		return nil
	}
	return c.suggestRules(scope)
}

func (c *Controller) suggestRules(scope string) []Suggestion {
	c.mu.Lock()
	history := append([]ApprovalRecord(nil), c.approvalHistory[scope]...)
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

// SetScope selects the compatibility scope used by the legacy methods. New
// code should prefer the explicit *For methods so scope is visible at the call
// site.
func (c *Controller) SetScope(actor identity.Actor) error {
	scope, ok := actorScope(actor)
	if !ok {
		return identity.ErrInvalidActor
	}
	c.mu.Lock()
	c.currentScope = scope
	c.mu.Unlock()
	return nil
}

func actorScope(actor identity.Actor) (string, bool) {
	if err := actor.Validate(); err != nil {
		return "", false
	}
	return actor.ScopeKey(), true
}

func defaultScopeOrCurrent(c *Controller) string {
	if c == nil {
		return defaultScope
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if strings.TrimSpace(c.currentScope) == "" {
		return defaultScope
	}
	return c.currentScope
}

func suggestionAlreadyCovered(allowlist []AllowRule, sug Suggestion) bool {
	for _, rule := range allowlist {
		if rule.Tool == sug.Tool && rule.Pattern == sug.Pattern {
			return true
		}
	}
	return false
}
