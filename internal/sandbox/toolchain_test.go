package sandbox

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code-agent/internal/telemetry/genai"
	"go.opentelemetry.io/otel"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("CODEOPS_TEST_DOCKER_CREATE"); mode != "" {
		if mode == "invalid-cid" {
			_, _ = fmt.Fprintln(os.Stdout, "warning mixed with ID")
			os.Exit(0)
		}
		if mode == "flood" {
			_, _ = io.WriteString(os.Stderr, strings.Repeat("diagnostic", 200000))
		} else {
			_, _ = fmt.Fprintln(os.Stderr, "daemon accepted create; response connection lost")
		}
		os.Exit(1)
	}
	var telemetry genai.Tracer
	if os.Getenv("CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS") == "1" && os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		telemetry = genai.NewTelemetry(context.Background())
	}
	code := m.Run()
	if telemetry != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := telemetry.Shutdown(ctx); err != nil {
			code = 1
		}
		cancel()
	}
	os.Exit(code)
}

func TestContainerCreationPreservesUnknownOutcomeAndOutputLimit(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"lost", "flood", "invalid-cid"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("CODEOPS_TEST_DOCKER_CREATE", mode)
			result, err := runContainer(context.Background(), binary, []string{"-test.run=^$", "--", "run", "--rm", "fixture-image", "fixture-program"}, 2, false)
			if !errors.Is(err, ErrSandboxOutcomeUnknown) || result.ExitCode != 126 || !strings.Contains(err.Error(), "codeops-sandbox-") {
				t.Fatalf("lost response treated as known refusal: exit=%d err=%v", result.ExitCode, err)
			}
			if mode == "flood" && (!result.Truncated || len(result.Output) > maxCapturedOutput || !strings.Contains(result.Output, "[output truncated]")) {
				t.Fatal("setup diagnostic lost its output limit")
			}
		})
	}
}

func TestTaskToolchainIntegrationBaselines(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_TASK_TOOLCHAIN_TESTS") != "1" {
		t.Skip("set CODE_AGENT_RUN_TASK_TOOLCHAIN_TESTS=1 with approved pinned Go/Python images")
	}
	for _, language := range []string{"go", "python"} {
		t.Run(language, func(t *testing.T) {
			workspace := t.TempDir()
			files := map[string]string{}
			program, args, versionArgs := "go", []string{"test", "./...", "-count=1", "-v"}, []string{"version"}
			if language == "go" {
				files = map[string]string{"go.mod": "module sandbox-check\n\ngo 1.20\n", "sum.go": "package check\nfunc Sum(a,b int) int { return a+b }\n", "sum_test.go": "package check\nimport \"testing\"\nfunc TestSum(t *testing.T) { if Sum(2,3) != 5 { t.Fatal(\"sum\") } }\n"}
			} else {
				files = map[string]string{"test_sum.py": "import unittest\nclass SumTest(unittest.TestCase):\n    def test_sum(self):\n        self.assertEqual(2+3,5)\n"}
				program, args, versionArgs = "python", []string{"-m", "unittest", "discover", "-v"}, []string{"-I", "--version"}
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(workspace, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			image := os.Getenv("CODE_AGENT_TEST_" + strings.ToUpper(language) + "_IMAGE")
			if image == "" {
				t.Fatal("approved digest-pinned task image required")
			}
			runner := NewDockerRunner(Config{TrustRoot: workspace, Image: image, Toolchain: language})
			ctx, span := otel.Tracer("code-agent/sandbox/e2e").Start(context.Background(), "sandbox.toolchain.baseline")
			defer span.End()
			t.Logf("TOOLCHAIN_TRACE %s %s", language, span.SpanContext().TraceID())
			ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
			defer cancel()
			version, err := runner.Run(ctx, Request{Workspace: workspace, Program: program, Args: versionArgs})
			if err != nil || version.ExitCode != 0 {
				t.Fatalf("toolchain identity: %+v err=%v", version, err)
			}
			t.Logf("TOOLCHAIN_IDENTITY %s %s", language, strings.TrimSpace(version.Output))
			result, err := runner.Run(ctx, Request{Workspace: workspace, Program: program, Args: args})
			if err != nil || result.ExitCode != 0 || result.Truncated || (language == "go" && !strings.Contains(result.Output, "--- PASS: TestSum")) || (language == "python" && !strings.Contains(result.Output, "Ran 1 test")) {
				t.Fatalf("toolchain baseline: %+v err=%v", result, err)
			}
			t.Logf("TOOLCHAIN_BASELINE %s %s", language, result.Output)
			entries, err := os.ReadDir(workspace)
			if err != nil || len(entries) != len(files) {
				t.Fatalf("baseline acquired generated files: entries=%d err=%v", len(entries), err)
			}
			for name, data := range files {
				actual, err := os.ReadFile(filepath.Join(workspace, name))
				if err != nil || sha256.Sum256(actual) != sha256.Sum256([]byte(data)) {
					t.Fatalf("baseline source changed: %s", name)
				}
			}
		})
	}
}
