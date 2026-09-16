package mcp

import (
	"strings"
	"testing"
)

func TestMCPProcessEnvironmentScrubsAmbientSecretsAndAppliesExplicitOverrides(t *testing.T) {
	env := mcpProcessEnvironment(
		[]string{"PATH=ambient", "MCP_AMBIENT_SECRET_TOKEN=blocked", "HOME=workspace"},
		map[string]string{"PATH": "explicit", "MCP_EXPLICIT_TOKEN": "allowed"},
	)
	seenPath, seenExplicit, seenAmbient := 0, false, false
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		switch {
		case strings.EqualFold(key, "PATH"):
			seenPath++
			if value != "explicit" {
				t.Fatal("explicit environment override was not applied")
			}
		case key == "MCP_EXPLICIT_TOKEN":
			seenExplicit = value == "allowed"
		case key == "MCP_AMBIENT_SECRET_TOKEN":
			seenAmbient = true
		}
	}
	if seenPath != 1 || !seenExplicit || seenAmbient {
		t.Fatal("MCP child environment boundary was not enforced")
	}
}
