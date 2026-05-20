package codeagent_test

import (
	"math"
	"strings"
	"testing"

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
