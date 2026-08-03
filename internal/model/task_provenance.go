package model

import (
	"strings"
)

// CorpusProvenance carries the pinned source identity a controlled corpus
// import attaches to a pipeline task. It is the in-flight companion of the
// durable KnowledgeDocument row: handlers/services validate it at the trust
// boundary (see knowledge-ingest internal entry) and workers read it back from
// the Kafka payload so that chunks, embeddings and ES documents stay traceable
// to an immutable source commit.
type CorpusProvenance struct {
	SourceID         string `json:"source_id"`
	SourcePath       string `json:"source_path"`
	SourceURL        string `json:"source_url,omitempty"`
	SourceCommit     string `json:"source_commit"`
	SourceSHA256     string `json:"source_sha256"`
	TargetIndex      string `json:"target_index"`
	CorpusGeneration string `json:"corpus_generation"`
}

// Validate enforces the corpus contract: every routing/audit field must be
// present, the commit must be a 40-char lowercase hex git SHA, and the content
// hash must be a 64-char lowercase hex SHA-256. SourceURL is optional.
func (c CorpusProvenance) Validate() error {
	if strings.TrimSpace(c.SourceID) == "" {
		return errRequired("source_id")
	}
	if strings.TrimSpace(c.SourcePath) == "" {
		return errRequired("source_path")
	}
	if strings.TrimSpace(c.CorpusGeneration) == "" {
		return errRequired("corpus_generation")
	}
	if strings.TrimSpace(c.TargetIndex) == "" {
		return errRequired("target_index")
	}
	commit := strings.TrimSpace(c.SourceCommit)
	if commit == "" {
		return errRequired("source_commit")
	}
	if !isLowerHex(commit, 40) {
		return errFormat("source_commit", "40 lowercase hex chars")
	}
	sha := strings.TrimSpace(c.SourceSHA256)
	if sha == "" {
		return errRequired("source_sha256")
	}
	if !isLowerHex(sha, 64) {
		return errFormat("source_sha256", "64 lowercase hex chars")
	}
	return nil
}

// isLowerHex reports whether s is exactly n lowercase hexadecimal characters.
func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < n; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// errFormat mirrors errRequired for value-shape failures so callers can map
// every corpus rejection to a 400 without distinguishing error types.
func errFormat(field, want string) error {
	return &formatError{field: field, want: want}
}

type formatError struct {
	field string
	want  string
}

func (e *formatError) Error() string { return e.field + " must be " + e.want }
