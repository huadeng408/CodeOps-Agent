package admission_test

import (
	"testing"

	"code-agent/internal/admission"
)

func TestProviderProfileRequiresExplicitBounds(t *testing.T) {
	t.Setenv("LLM_PROVIDER", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "fixture-provider-authority")
	t.Setenv("ANTHROPIC_BASE_URL", "https://example.invalid")
	t.Setenv("ANTHROPIC_MODEL", "fixture-model")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "invalid")
	config := admission.ProviderFromEnv()
	if config.Protocol != "anthropic" || config.APIKey != "fixture-provider-authority" || config.InputLimit != 0 || config.OutputLimit != 0 {
		t.Fatal("legacy provider defaults invented an admitted bound")
	}
	t.Setenv("LLM_PROVIDER", " OPENAI ")
	t.Setenv("OPENAI_API_KEY", "fixture-openai-authority")
	t.Setenv("OPENAI_BASE_URL", "https://example.invalid/v1")
	t.Setenv("OPENAI_MODEL", "fixture-model")
	t.Setenv("CODE_AGENT_MODEL_INPUT_LIMIT", "100")
	t.Setenv("CODE_AGENT_MODEL_OUTPUT_LIMIT", "3")
	config = admission.ProviderFromEnv()
	if config.Protocol != "openai" || config.APIKey != "fixture-openai-authority" || config.InputLimit != 100 || config.OutputLimit != 3 {
		t.Fatal("explicit profile did not reach the Go transport")
	}
}
