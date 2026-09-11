package main

import (
	"testing"

	"code-agent/internal/serverconfig"
)

func TestKafkaConsumersAreOptIn(t *testing.T) {
	var cfg serverconfig.KafkaConfig
	if cfg.ConsumersEnabled {
		t.Fatal("Kafka consumers must be disabled for the default Agent Harness runtime")
	}
	cfg.ConsumersEnabled = true
	if !cfg.ConsumersEnabled {
		t.Fatal("explicit Kafka consumer opt-in was not preserved")
	}
}
