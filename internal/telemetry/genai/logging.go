package genai

import (
	"log"

	"github.com/go-logr/logr"
)

// SDK diagnostics may contain headers, URLs and response bodies; retain only the class.
type telemetryLogSink struct{}

func (telemetryLogSink) Init(logr.RuntimeInfo)            {}
func (telemetryLogSink) Enabled(level int) bool           { return level <= 0 }
func (telemetryLogSink) Info(int, string, ...any)         { log.Print("[telemetry] SDK diagnostic") }
func (telemetryLogSink) Error(error, string, ...any)      { log.Print("[telemetry] SDK failure") }
func (s telemetryLogSink) WithValues(...any) logr.LogSink { return s }
func (s telemetryLogSink) WithName(string) logr.LogSink   { return s }
