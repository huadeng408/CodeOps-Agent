package admission

import (
	"os"
	"strconv"
	"strings"
)

// Explicit input bounds belong to the approved provider profile. An absent
// bound stays unavailable; a session context default is not evidence of it.
func ProviderFromEnv() ProviderConfig {
	protocol := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	if protocol == "" {
		protocol = "openai"
		if os.Getenv("ANTHROPIC_API_KEY") != "" || os.Getenv("ANTHROPIC_AUTH_TOKEN") != "" {
			protocol = "anthropic"
		}
	}
	prefix := strings.ToUpper(protocol)
	key := os.Getenv(prefix + "_API_KEY")
	if key == "" && protocol == "anthropic" {
		key = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	}
	input, _ := strconv.ParseInt(os.Getenv("CODE_AGENT_MODEL_INPUT_LIMIT"), 10, 64)
	output, _ := strconv.ParseInt(os.Getenv("CODE_AGENT_MODEL_OUTPUT_LIMIT"), 10, 64)
	return ProviderConfig{Protocol: protocol, BaseURL: os.Getenv(prefix + "_BASE_URL"), Model: os.Getenv(prefix + "_MODEL"), APIKey: key, InputLimit: input, OutputLimit: output}
}
