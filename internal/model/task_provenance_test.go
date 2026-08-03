package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func validCorpusProvenance() CorpusProvenance {
	return CorpusProvenance{
		SourceID:         "go",
		SourcePath:       "doc/asm.html",
		SourceURL:        "https://example.com/go/blob/abc/doc/asm.html",
		SourceCommit:     "0123456789abcdef0123456789abcdef01234567",
		SourceSHA256:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		TargetIndex:      "knowledge_base_v2_bge_m3",
		CorpusGeneration: "techdocs-2026-07-30-v1",
	}
}

func TestCorpusProvenanceValidateAcceptsValid(t *testing.T) {
	if err := validCorpusProvenance().Validate(); err != nil {
		t.Fatalf("valid provenance must pass Validate, got %v", err)
	}
}

func TestCorpusProvenanceValidateAcceptsBlankSourceURL(t *testing.T) {
	p := validCorpusProvenance()
	p.SourceURL = "" // SourceURL is optional
	if err := p.Validate(); err != nil {
		t.Fatalf("blank SourceURL must be allowed, got %v", err)
	}
}

func TestCorpusProvenanceValidateRejectsMissingFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*CorpusProvenance)
		field  string
	}{
		{"source_id_blank", func(p *CorpusProvenance) { p.SourceID = "" }, "source_id"},
		{"source_id_whitespace", func(p *CorpusProvenance) { p.SourceID = "  " }, "source_id"},
		{"source_path_blank", func(p *CorpusProvenance) { p.SourcePath = "" }, "source_path"},
		{"source_path_whitespace", func(p *CorpusProvenance) { p.SourcePath = "\t" }, "source_path"},
		{"corpus_generation_blank", func(p *CorpusProvenance) { p.CorpusGeneration = "" }, "corpus_generation"},
		{"target_index_blank", func(p *CorpusProvenance) { p.TargetIndex = "" }, "target_index"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validCorpusProvenance()
			tc.mutate(&p)
			err := p.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("expected %s validation error, got %v", tc.field, err)
			}
		})
	}
}

func TestCorpusProvenanceValidateRejectsBadCommitFormat(t *testing.T) {
	cases := []struct {
		name   string
		commit string
	}{
		{"empty", ""},
		{"too_short", "abc123"},
		{"non_hex", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
		{"uppercase", "0123456789ABCDEF0123456789ABCDEF01234567"},
		{"too_long", "0123456789abcdef0123456789abcdef0123456789"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validCorpusProvenance()
			p.SourceCommit = tc.commit
			err := p.Validate()
			if err == nil || !strings.Contains(err.Error(), "source_commit") {
				t.Fatalf("expected source_commit format error for %q, got %v", tc.commit, err)
			}
		})
	}
}

func TestCorpusProvenanceValidateRejectsBadSHA256Format(t *testing.T) {
	cases := []struct {
		name string
		hash string
	}{
		{"empty", ""},
		{"too_short", "abc123"},
		{"non_hex", strings.Repeat("z", 64)},
		{"uppercase", strings.Repeat("A", 64)},
		{"too_long", strings.Repeat("0", 65)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validCorpusProvenance()
			p.SourceSHA256 = tc.hash
			err := p.Validate()
			if err == nil || !strings.Contains(err.Error(), "source_sha256") {
				t.Fatalf("expected source_sha256 format error for %q, got %v", tc.hash, err)
			}
		})
	}
}

func TestCorpusProvenanceJSONRoundTrip(t *testing.T) {
	p := validCorpusProvenance()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded CorpusProvenance
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded != p {
		t.Fatalf("round trip lost fields: got %+v want %+v", decoded, p)
	}
}
