package metrics

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type Pricing struct {
	InputPer1K  float64
	OutputPer1K float64
}

type SessionMetrics struct {
	StartTime         time.Time
	TotalTokensIn     int
	TotalTokensOut    int
	TotalCachedTokens int
	TotalCost         float64
	ToolCalls         int
	Turns             int
	Errors            int
}

type TurnMetrics struct {
	StartTime time.Time
	TokensIn  int
	TokensOut int
	ToolCalls int
	Duration  time.Duration
}

type Collector struct {
	mu      sync.Mutex
	session SessionMetrics
	current TurnMetrics
	models  map[string]struct{}
}

var modelPricing = map[string]Pricing{
	"gpt-4o":            {InputPer1K: 0.0025, OutputPer1K: 0.01},
	"gpt-4o-mini":       {InputPer1K: 0.00015, OutputPer1K: 0.0006},
	"claude-sonnet-4-6": {InputPer1K: 0.003, OutputPer1K: 0.015},
	"claude-opus-4-7":   {InputPer1K: 0.015, OutputPer1K: 0.075},
}

func NewCollector() *Collector {
	now := time.Now()
	return &Collector{
		session: SessionMetrics{StartTime: now},
		current: TurnMetrics{StartTime: now},
		models:  make(map[string]struct{}),
	}
}

func (c *Collector) Hydrate(snapshot SessionMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if snapshot.StartTime.IsZero() {
		snapshot.StartTime = time.Now()
	}
	c.session = snapshot
	c.current = TurnMetrics{StartTime: time.Now()}
	c.models = make(map[string]struct{})
}

func (c *Collector) BeginTurn() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.session.Turns++
	c.current = TurnMetrics{StartTime: time.Now()}
}

func (c *Collector) EndTurn() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.current.StartTime.IsZero() {
		c.current.Duration = time.Since(c.current.StartTime)
	}
}

func (c *Collector) RecordLLMCall(model string, tokensIn, tokensOut int) {
	cost := EstimateCost(model, tokensIn, tokensOut)
	c.RecordLLMUsage(model, tokensIn, tokensOut, cost)
}

func (c *Collector) RecordLLMUsage(model string, tokensIn, tokensOut int, cost float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.session.TotalTokensIn += tokensIn
	c.session.TotalTokensOut += tokensOut
	c.session.TotalCost += cost
	c.current.TokensIn += tokensIn
	c.current.TokensOut += tokensOut
	if model != "" {
		c.models[model] = struct{}{}
	}
}

func (c *Collector) RecordToolCall() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.session.ToolCalls++
	c.current.ToolCalls++
}

// RecordCachedTokens accumulates prompt-cache hit tokens reported by the
// orchestrator (Anthropic cache_read_input_tokens / OpenAI cached_tokens).
func (c *Collector) RecordCachedTokens(tokens int) {
	if tokens <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session.TotalCachedTokens += tokens
}

func (c *Collector) RecordError() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.session.Errors++
}

func (c *Collector) RecordInput(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tokens := countWords(text)
	c.session.TotalTokensIn += tokens
	c.current.TokensIn += tokens
}

func (c *Collector) RecordOutput(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tokens := countWords(text)
	c.session.TotalTokensOut += tokens
	c.current.TokensOut += tokens
}

func (c *Collector) Snapshot() SessionMetrics {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.session
}

func EstimateCost(model string, tokensIn, tokensOut int) float64 {
	pricing := modelPricing[model]
	return float64(tokensIn)/1000*pricing.InputPer1K + float64(tokensOut)/1000*pricing.OutputPer1K
}

func (c *Collector) StatusLine() string {
	s := c.Snapshot()
	return fmt.Sprintf("Tokens: %d in / %d out | Cost: $%.4f | Tools: %d | Turns: %d",
		s.TotalTokensIn, s.TotalTokensOut, s.TotalCost, s.ToolCalls, s.Turns)
}

// CacheHitRatio returns the fraction of input tokens served from the prompt
// cache (0 when no input tokens have been recorded).
func (c *Collector) CacheHitRatio() float64 {
	s := c.Snapshot()
	if s.TotalTokensIn <= 0 {
		return 0
	}
	return float64(s.TotalCachedTokens) / float64(s.TotalTokensIn)
}

func countWords(text string) int {
	if strings.TrimSpace(text) == "" {
		return 0
	}
	return len(strings.Fields(text))
}
