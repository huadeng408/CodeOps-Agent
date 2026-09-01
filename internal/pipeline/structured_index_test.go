package pipeline

import (
	"strings"
	"testing"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
)

func newProcessorForIndexTest(embeddingCfg serverconfig.EmbeddingConfig, corpusCfg serverconfig.CorpusConfig) *Processor {
	return &Processor{
		esCfg:        serverconfig.ElasticsearchConfig{IndexName: "knowledge_base"},
		embeddingCfg: embeddingCfg,
		corpusCfg:    corpusCfg,
	}
}

func TestIndexNameForUsesCorpusTextIndexForStructured(t *testing.T) {
	p := newProcessorForIndexTest(serverconfig.EmbeddingConfig{}, serverconfig.CorpusConfig{TextIndex: "knowledge_base_v2_bge_m3"})
	item := model.DocumentVector{CorpusGeneration: "techdocs-2026-07-30-v1"}
	if got := p.indexStage().indexNameFor(item); got != "knowledge_base_v2_bge_m3" {
		t.Fatalf("indexNameFor() = %q, want corpus text index", got)
	}
}

func TestIndexNameForKeepsLegacyIndexForUnstructured(t *testing.T) {
	p := newProcessorForIndexTest(serverconfig.EmbeddingConfig{}, serverconfig.CorpusConfig{TextIndex: "knowledge_base_v2_bge_m3"})
	if got := p.indexStage().indexNameFor(model.DocumentVector{}); got != "knowledge_base" {
		t.Fatalf("indexNameFor() = %q, want legacy index", got)
	}
}

func TestIndexNameForStructuredReturnsEmptyWithoutCorpusConfig(t *testing.T) {
	p := newProcessorForIndexTest(serverconfig.EmbeddingConfig{}, serverconfig.CorpusConfig{})
	if got := p.indexStage().indexNameFor(model.DocumentVector{CorpusGeneration: "g"}); got != "" {
		t.Fatalf("indexNameFor() = %q, want empty for missing corpus text index", got)
	}
}

func TestValidateStructuredVectorRejectsBadDimensionsOnExternalPath(t *testing.T) {
	p := newProcessorForIndexTest(serverconfig.EmbeddingConfig{
		ModelRevision:      "BAAI/bge-m3@8f1b7f9d4c2a6e5b0d9c8f7a6b5c4d3e2f1a0b9c",
		Dimensions:         1024,
		ExpectedDimensions: 1024,
	}, serverconfig.CorpusConfig{})
	item := model.DocumentVector{CorpusGeneration: "techdocs-2026-07-30-v1"}
	err := p.indexStage().validateStructuredVector(item, []float32{1, 2})
	if err == nil {
		t.Fatal("expected dimension validation error on external path")
	}
}

func TestValidateStructuredVectorAllowsLegacyWithoutNativeContract(t *testing.T) {
	p := newProcessorForIndexTest(serverconfig.EmbeddingConfig{ModelRevision: "", Dimensions: 512}, serverconfig.CorpusConfig{})
	if err := p.indexStage().validateStructuredVector(model.DocumentVector{}, []float32{1, 2}); err != nil {
		t.Fatalf("legacy path must not require native contract: %v", err)
	}
}

func TestValidateStructuredVectorRejectsFloatingRevision(t *testing.T) {
	p := newProcessorForIndexTest(serverconfig.EmbeddingConfig{
		ModelRevision:      "BAAI/bge-m3@main",
		Dimensions:         1024,
		ExpectedDimensions: 1024,
	}, serverconfig.CorpusConfig{})
	item := model.DocumentVector{CorpusGeneration: "techdocs-2026-07-30-v1"}
	err := p.indexStage().validateStructuredVector(item, make([]float32, 1024))
	if err == nil || !strings.Contains(err.Error(), "immutable commit") {
		t.Fatalf("expected immutable revision error, got %v", err)
	}
}
