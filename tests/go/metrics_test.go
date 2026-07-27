package codeagent_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"code-agent/internal/metrics"
)

func TestCollectorRecordsLLMUsageFromSessionMeta(t *testing.T) {
	collector := metrics.NewCollector()
	collector.BeginTurn()
	collector.RecordLLMUsage("gpt-4o", 100, 50, 0.00075)
	collector.EndTurn()

	snapshot := collector.Snapshot()
	if snapshot.TotalTokensIn != 100 || snapshot.TotalTokensOut != 50 {
		t.Fatalf("unexpected token totals: %+v", snapshot)
	}
	if math.Abs(snapshot.TotalCost-0.00075) > 0.000001 {
		t.Fatalf("unexpected cost: %.8f", snapshot.TotalCost)
	}
	if !strings.Contains(collector.StatusLine(), "Cost: $0.0008") {
		t.Fatalf("unexpected status line: %s", collector.StatusLine())
	}
}

func TestEstimateCostUsesKnownModelPricing(t *testing.T) {
	cost := metrics.EstimateCost("gpt-4o", 1000, 1000)

	if math.Abs(cost-0.0125) > 0.000001 {
		t.Fatalf("unexpected estimate: %.8f", cost)
	}
}

func TestCollectorAccumulatesCachedTokens(t *testing.T) {
	collector := metrics.NewCollector()
	collector.RecordLLMUsage("claude-sonnet-4-6", 1000, 100, 0.0045)
	collector.RecordCachedTokens(400)
	collector.RecordCachedTokens(150)
	collector.RecordCachedTokens(0) // no-op

	snapshot := collector.Snapshot()
	if snapshot.TotalCachedTokens != 550 {
		t.Fatalf("expected 550 cached tokens, got %d", snapshot.TotalCachedTokens)
	}
	ratio := collector.CacheHitRatio()
	if math.Abs(ratio-0.55) > 0.0001 {
		t.Fatalf("expected 0.55 cache hit ratio, got %.4f", ratio)
	}
}

func TestCollectorHydratesPersistedSessionMetrics(t *testing.T) {
	collector := metrics.NewCollector()
	started := time.Now().Add(-time.Hour)
	collector.Hydrate(metrics.SessionMetrics{
		StartTime:      started,
		TotalTokensIn:  200,
		TotalTokensOut: 80,
		TotalCost:      0.0015,
		ToolCalls:      3,
	})

	snapshot := collector.Snapshot()
	if !snapshot.StartTime.Equal(started) {
		t.Fatalf("unexpected start time: %v", snapshot.StartTime)
	}
	if snapshot.TotalTokensIn != 200 || snapshot.TotalTokensOut != 80 || snapshot.ToolCalls != 3 {
		t.Fatalf("unexpected hydrated metrics: %+v", snapshot)
	}
	if math.Abs(snapshot.TotalCost-0.0015) > 0.000001 {
		t.Fatalf("unexpected hydrated cost: %.8f", snapshot.TotalCost)
	}

	collector.BeginTurn()
	collector.RecordLLMUsage("gpt-4o", 10, 5, 0.0001)
	collector.RecordToolCall()
	collector.EndTurn()

	snapshot = collector.Snapshot()
	if snapshot.TotalTokensIn != 210 || snapshot.TotalTokensOut != 85 || snapshot.ToolCalls != 4 {
		t.Fatalf("unexpected metrics after hydrate accumulation: %+v", snapshot)
	}
}
