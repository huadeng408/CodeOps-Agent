package corpus

import (
	"context"
	"os"
	"testing"

	"code-agent/internal/serverconfig"
	"code-agent/pkg/embedding"
	"code-agent/pkg/log"
)

func init() {
	log.Init("error", "console", "")
}

// TestLiveBGE3Preflight verifies the real BGE-M3 embedding service against
// the native 1024-dim contract. Explicit test: run only when the service is
// up and CODE_AGENT_RUN_RAG_E2E=1 is set.
func TestLiveBGE3Preflight(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_RAG_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_RAG_E2E=1 to verify the live BGE-M3 service")
	}
	cfg := serverconfig.EmbeddingConfig{
		Model:                   "D:/tools/mineru-models/models/BAAI--bge-m3",
		ModelRevision:           "BAAI/bge-m3@8f1b7f9d4c2a6e5b0d9c8f7a6b5c4d3e2f1a0b9c",
		BaseURL:                 "http://127.0.0.1:8009",
		Dimensions:              1024,
		ExpectedDimensions:      1024,
		HealthPath:              "/health",
		RequireNativeDimensions: true,
	}
	if err := embedding.Preflight(context.Background(), cfg); err != nil {
		t.Fatalf("PREFLIGHT FAILED: %v", err)
	}
	t.Log("PREFLIGHT PASSED: native 1024-dim BGE-M3 contract verified")
}
