// Package service contains business logic.
package service

import "time"

// ContextSnippet represents a context snippet.
type ContextSnippet struct {
	ID         string
	SourceType string
	Label      string
	Text       string
	Score      float64
	Timestamp  time.Time
}
