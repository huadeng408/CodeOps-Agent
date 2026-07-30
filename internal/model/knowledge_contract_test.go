package model

import "testing"

func TestKnowledgeIDsAreStable(t *testing.T) {
	sourceID := SourceVersionID("python", "0123456789abcdef0123456789abcdef01234567", "techdocs-v1")
	if sourceID != "python@0123456789abcdef0123456789abcdef01234567#techdocs-v1" {
		t.Fatalf("source id = %q", sourceID)
	}
	documentID := DocumentID("python", "0123456789abcdef0123456789abcdef01234567", "Doc/library.rst")
	if documentID != "python@0123456789abcdef0123456789abcdef01234567:Doc/library.rst" {
		t.Fatalf("document id = %q", documentID)
	}
}

func TestKnowledgeIDsRejectBlankComponents(t *testing.T) {
	if got := SourceVersionID("", "commit", "generation"); got != "" {
		t.Fatalf("source id with blank source = %q", got)
	}
	if got := SourceVersionID("source", " ", "generation"); got != "" {
		t.Fatalf("source id with blank commit = %q", got)
	}
	if got := SourceVersionID("source", "commit", "\t"); got != "" {
		t.Fatalf("source id with blank generation = %q", got)
	}
	if got := DocumentID("", "commit", "path"); got != "" {
		t.Fatalf("document id with blank source = %q", got)
	}
	if got := DocumentID("source", " ", "path"); got != "" {
		t.Fatalf("document id with blank commit = %q", got)
	}
	if got := DocumentID("source", "commit", "\n"); got != "" {
		t.Fatalf("document id with blank path = %q", got)
	}
}
