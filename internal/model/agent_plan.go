// Package model contains persistent models and DTOs.
package model

import "strings"

type RetrievalMode string

const (
	RetrievalModeHybrid RetrievalMode = "hybrid"
	RetrievalModeBM25   RetrievalMode = "bm25"
	RetrievalModeVector RetrievalMode = "vector"
)

// AgentPlan represents an agent plan.
type AgentPlan struct {
	Intent         string        `json:"intent"`
	RewrittenQuery string        `json:"rewritten_query"`
	SubQueries     []string      `json:"sub_queries"`
	RetrievalMode  RetrievalMode `json:"retrieval_mode"`
	EnableRerank   *bool         `json:"enable_rerank,omitempty"`
	NeedHistory    bool          `json:"need_history"`
	SkipRetrieval  bool          `json:"skip_retrieval"`
	Reason         string        `json:"reason,omitempty"`
}

// DefaultAgentPlan returns the default agent plan.
func DefaultAgentPlan(query string) AgentPlan {
	return AgentPlan{
		Intent:         "single_hop",
		RewrittenQuery: strings.TrimSpace(query),
		SubQueries:     []string{},
		RetrievalMode:  RetrievalModeHybrid,
		NeedHistory:    false,
		SkipRetrieval:  false,
	}
}
