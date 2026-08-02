package model

import "strings"

// DocumentContract is the frozen, durable contract for a source document in
// the multimodal corpus (design spec §3.1). Tenant and ACL fields are written
// only by the Go control plane and are NOT part of the worker contract.
type DocumentContract struct {
	DocumentID       string `json:"document_id"`
	SourceID         string `json:"source_id"`
	SourceURI        string `json:"source_uri"`
	SourceSHA256     string `json:"source_sha256"`
	Mime             string `json:"mime"`
	ParserName       string `json:"parser_name"`
	ParserVersion    string `json:"parser_version"`
	ParserBackend    string `json:"parser_backend"`
	LicenseID        string `json:"license_id"`
	CorpusGeneration string `json:"corpus_generation"`
}

// Validate rejects documents that cannot be traced to a pinned source.
func (d DocumentContract) Validate() error {
	if strings.TrimSpace(d.DocumentID) == "" {
		return errRequired("document_id")
	}
	if strings.TrimSpace(d.SourceID) == "" {
		return errRequired("source_id")
	}
	if strings.TrimSpace(d.SourceURI) == "" {
		return errRequired("source_uri")
	}
	if strings.TrimSpace(d.SourceSHA256) == "" {
		return errRequired("source_sha256")
	}
	if strings.TrimSpace(d.ParserName) == "" {
		return errRequired("parser_name")
	}
	if strings.TrimSpace(d.ParserVersion) == "" {
		return errRequired("parser_version")
	}
	if strings.TrimSpace(d.CorpusGeneration) == "" {
		return errRequired("corpus_generation")
	}
	return nil
}

func errRequired(field string) error {
	return &contractError{field: field}
}

type contractError struct{ field string }

func (e *contractError) Error() string { return e.field + " is required" }
