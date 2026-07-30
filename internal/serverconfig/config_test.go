package serverconfig

import "testing"

func TestCorpusDefaults(t *testing.T) {
	corpus := DefaultCorpusConfig()
	if corpus.Generation != "techdocs-2026-07-30-v1" {
		t.Fatalf("generation = %q", corpus.Generation)
	}
	if corpus.TextIndex != "knowledge_base_v2_bge_m3" {
		t.Fatalf("text index = %q", corpus.TextIndex)
	}
	if corpus.ReadAlias != "knowledge_base_current" {
		t.Fatalf("read alias = %q", corpus.ReadAlias)
	}
	if corpus.VisualAlias != "knowledge_page_visual_current" {
		t.Fatalf("visual alias = %q", corpus.VisualAlias)
	}
	if corpus.AllowAliasSwitch {
		t.Fatal("alias switching must default to false")
	}
}
