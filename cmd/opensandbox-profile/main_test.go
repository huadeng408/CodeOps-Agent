package main_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileCLIPreservesApprovedToolchainAndBindsItsPolicy(t *testing.T) {
	var policies []string
	for _, toolchain := range []string{"", "go", "python"} {
		t.Run("toolchain="+toolchain, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Mkdir(filepath.Join(directory, "opensandbox_server"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "opensandbox_server", "codeops_profile.py"), []byte("# fixed server fixture\n"), 0600); err != nil {
				t.Fatal(err)
			}
			input := map[string]any{
				"source_sha":       "f89eff24bbd15ae1c37a47769f7667849e679be0",
				"task_images":      []string{"sha256:" + strings.Repeat("1", 64)},
				"execd_image":      "sha256:" + strings.Repeat("2", 64),
				"egress_image":     "sha256:" + strings.Repeat("3", 64),
				"execd_sha256":     strings.Repeat("4", 64),
				"bootstrap_sha256": "499114628856ef088dacd848daefb90ba141fdd39d23608906357f61e6fd4bef",
				"workspace_root":   "/run/desktop/mnt/host/d/approved",
				"network":          "approved-network", "toolchain": toolchain,
			}
			data, _ := json.Marshal(input)
			inputPath := filepath.Join(t.TempDir(), "input.json")
			if err := os.WriteFile(inputPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			if output, err := exec.Command("go", "run", ".", directory, inputPath).CombinedOutput(); err != nil {
				t.Fatalf("profile CLI: %s %v", output, err)
			}
			output, err := os.ReadFile(filepath.Join(directory, "codeops-profile.json"))
			if err != nil {
				t.Fatal(err)
			}
			var profile map[string]any
			if json.Unmarshal(output, &profile) != nil {
				t.Fatal("CLI produced invalid profile")
			}
			if toolchain == "" {
				if _, exists := profile["toolchain"]; exists {
					t.Fatal("legacy Go profile changed")
				}
			} else if profile["toolchain"] != toolchain {
				t.Fatalf("approved toolchain lost: %+v", profile)
			}
			policy, ok := profile["policy_sha256"].(string)
			if !ok || len(policy) != 64 {
				t.Fatal("policy identity missing")
			}
			for _, previous := range policies {
				if policy == previous {
					t.Fatal("toolchain change did not change policy identity")
				}
			}
			policies = append(policies, policy)
			if err := exec.Command("go", "run", ".", directory, inputPath).Run(); err == nil {
				t.Fatal("CLI overwrote retained profile")
			}
		})
	}
}
