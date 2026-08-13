package service

import (
	"io"
	"os"
	"strings"
	"testing"

	"code-agent/internal/telemetry/genai"
	"code-agent/pkg/log"
)

func TestRetrievalMetricsLogsOnlyQueryHash(t *testing.T) {
	readLog, writeLog, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout := os.Stdout
	os.Stdout = writeLog
	log.Init("info", "json", "")
	result := make(chan []byte, 1)
	go func() {
		body, _ := io.ReadAll(readLog)
		result <- body
	}()
	t.Cleanup(func() {
		os.Stdout = originalStdout
		log.Init("error", "console", "")
		_ = writeLog.Close()
		_ = readLog.Close()
	})

	query := "private retrieval query"
	normalized := "private normalized query"
	phrase := "private phrase"
	service := &searchService{observer: newRetrievalObserver(4)}
	service.logRetrievalMetrics(query, retrievalObservation{LatencyMs: 12.5})
	log.Sync()
	if err := writeLog.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = originalStdout
	logged := string(<-result)
	log.Init("error", "console", "")

	for _, content := range []string{query, normalized, phrase, `"query"`, `"normalizedQuery"`, `"rawPhrase"`} {
		if strings.Contains(logged, content) {
			t.Fatalf("retrieval metrics log contains query content %q: %s", content, logged)
		}
	}
	if !strings.Contains(logged, `"queryHash":"`+genai.HashQuery(query)+`"`) {
		t.Fatalf("retrieval metrics log is missing query hash: %s", logged)
	}
	if !strings.Contains(logged, `"latencyMs":12.5`) {
		t.Fatalf("retrieval metrics log is missing metrics: %s", logged)
	}
	for _, field := range []string{`"windowSamples":1`, `"totalRequests":1`, `"health":"insufficient_samples"`} {
		if !strings.Contains(logged, field) {
			t.Fatalf("retrieval metrics log is missing health field %s: %s", field, logged)
		}
	}
}

func TestRetrievalObserverAlertsOnlyAfterMinimumSampleWindow(t *testing.T) {
	observer := newRetrievalObserver(8)
	for range 24 {
		observer.Record(retrievalObservation{LatencyMs: 10_000, RerankTimeout: true})
	}
	if snapshot := observer.Record(retrievalObservation{LatencyMs: 10_000, RerankTimeout: true}); snapshot.Health != "alert" {
		t.Fatalf("health = %q, want alert after minimum sample window", snapshot.Health)
	}
	if snapshot := newRetrievalObserver(8).Record(retrievalObservation{LatencyMs: 10_000, RerankTimeout: true}); snapshot.Health != "insufficient_samples" {
		t.Fatalf("health = %q, want insufficient_samples before minimum window", snapshot.Health)
	}
}
