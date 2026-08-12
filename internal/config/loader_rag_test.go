package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultRAGConfigurationIsDisabledAndLocal(t *testing.T) {
	t.Parallel()

	cfg := Default(t.TempDir())
	if cfg.RAGEnabled {
		t.Fatal("RAGEnabled = true, want false")
	}
	if cfg.RAGServerURL != "http://127.0.0.1:8081" {
		t.Fatalf("RAGServerURL = %q", cfg.RAGServerURL)
	}
	if cfg.RAGInternalSecret != "" || cfg.RAGUserID != 0 || cfg.RAGOrgTag != "" || cfg.RAGIngestPublic {
		t.Fatalf("unexpected RAG defaults: %+v", cfg)
	}
}

func TestApplyJSONPatchMergesRAGConfiguration(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	data := []byte(`{
		"rag_enabled": true,
		"rag_server_url": "http://rag.internal:9090/",
		"rag_internal_secret": "internal-secret",
		"rag_user_id": 77,
		"rag_org_tag": "platform",
		"rag_ingest_public": true,
		"rag_source_id": "pilot-corpus",
		"rag_source_path_prefix": "workspace",
		"rag_source_url": "https://example.invalid/corpus",
		"rag_source_commit": "0123456789abcdef0123456789abcdef01234567",
		"rag_target_index": "knowledge_pilot_v1",
		"rag_corpus_generation": "pilot-20260813",
		"rag_ingest_run_id": "pilot-run-001"
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := Default(dir)
	if err := applyJSONPatch(path, &cfg); err != nil {
		t.Fatalf("applyJSONPatch: %v", err)
	}
	if !cfg.RAGEnabled || cfg.RAGServerURL != "http://rag.internal:9090/" || cfg.RAGInternalSecret != "internal-secret" || cfg.RAGUserID != 77 || cfg.RAGOrgTag != "platform" || !cfg.RAGIngestPublic {
		t.Fatalf("unexpected merged RAG config: %+v", cfg)
	}
	if cfg.RAGSourceID != "pilot-corpus" || cfg.RAGSourcePathPrefix != "workspace" || cfg.RAGSourceURL != "https://example.invalid/corpus" || cfg.RAGSourceCommit != "0123456789abcdef0123456789abcdef01234567" || cfg.RAGTargetIndex != "knowledge_pilot_v1" || cfg.RAGCorpusGeneration != "pilot-20260813" || cfg.RAGIngestRunID != "pilot-run-001" {
		t.Fatalf("unexpected merged RAG provenance config: %+v", cfg)
	}
}

func TestApplyJSONPatchHonorsExplicitFalseForRAGBooleans(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"rag_enabled":false,"rag_ingest_public":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Default(dir)
	cfg.RAGEnabled = true
	cfg.RAGIngestPublic = true

	if err := applyJSONPatch(path, &cfg); err != nil {
		t.Fatalf("applyJSONPatch: %v", err)
	}
	if cfg.RAGEnabled || cfg.RAGIngestPublic {
		t.Fatalf("explicit false was not merged: %+v", cfg)
	}
}
