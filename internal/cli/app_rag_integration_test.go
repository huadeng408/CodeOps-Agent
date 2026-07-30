package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"code-agent/internal/config"
	"code-agent/internal/rag"
	"code-agent/internal/tools"
)

func TestNewAppWithoutOrchestratorDoesNotPanic(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default(root)
	cfg.ProjectRoot = root
	cfg.WorkingDir = root
	cfg.OrchestratorAddr = "127.0.0.1:1"
	cfg.OrchestratorAutoStart = false

	app := NewApp(cfg, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if app == nil {
		t.Fatal("NewApp returned nil")
	}
	t.Cleanup(func() { cleanupIntegrationApp(t, app) })
	if app.orchestrator != nil {
		t.Fatal("NewApp should tolerate an unavailable optional orchestrator")
	}
	if app.ragClient == nil || app.ragIngester != app.ragClient {
		t.Fatal("NewApp must share one RAG client between ingestion and app state")
	}
	if !app.executor.UsesRAGSearcher(app.ragClient) {
		t.Fatal("NewApp executor must use the same RAG client instance")
	}
}

func TestRealNewAppRAGIngestThenSearchKnowledge(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_RAG_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_RAG_E2E=1 to run the real RAG integration")
	}

	root := t.TempDir()
	marker := newAppRAGMarker(t)
	fileName := marker + ".txt"
	content := fmt.Sprintf("Real NewApp RAG lifecycle marker %s. The slash command and SearchKnowledge must share the configured client.\n", marker)
	if err := os.WriteFile(filepath.Join(root, fileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("marker=%s", marker)

	cfg := realAppRAGConfig(t, root)
	var output bytes.Buffer
	app := NewApp(cfg, strings.NewReader(""), &output, &output)
	if app == nil {
		t.Fatal("NewApp returned nil")
	}
	t.Cleanup(func() { cleanupIntegrationApp(t, app) })
	if app.orchestrator != nil {
		t.Fatal("real RAG integration must not depend on a Python orchestrator")
	}

	ingestCtx, cancelIngest := context.WithTimeout(context.Background(), 2*time.Minute)
	handled := app.handleSlashCommand(ingestCtx, "/ingest "+fileName)
	cancelIngest()
	if !handled {
		t.Fatal("/ingest was not handled")
	}
	rendered := output.String()
	if strings.Contains(rendered, "ingest failed:") {
		t.Fatalf("real NewApp /ingest failed: %s", rendered)
	}
	match := regexp.MustCompile(`(?m)md5:\s*([0-9a-f]{32})`).FindStringSubmatch(rendered)
	if len(match) != 2 {
		t.Fatalf("real NewApp /ingest did not render a file MD5: %q", rendered)
	}
	t.Logf("fileMd5=%s", match[1])

	topHit := waitForAppRAGMarker(t, marker, fileName, match[1], func(ctx context.Context) (tools.ToolResult, error) {
		return app.executor.Execute(ctx, tools.ToolRequest{
			Name: "SearchKnowledge",
			Arguments: map[string]any{
				"query":          marker,
				"top_k":          10,
				"mode":           "bm25",
				"disable_rerank": true,
			},
		})
	})
	t.Logf("top hit:\n%s", topHit)

	wrongUserID := uint(1)
	if cfg.RAGUserID == wrongUserID {
		wrongUserID = 2
	}
	wrongClient := rag.NewClient(rag.Config{
		Enabled:       true,
		BaseURL:       cfg.RAGServerURL,
		InternalToken: cfg.RAGInternalSecret,
		UserID:        wrongUserID,
		OrgTag:        cfg.RAGOrgTag,
		IngestPublic:  false,
	})
	wrongExecutor := tools.NewExecutor(t.TempDir())
	wrongExecutor.SetRAGSearcher(wrongClient)
	assertAppRAGMarkerInvisible(t, marker, fileName, match[1], func(ctx context.Context) (tools.ToolResult, error) {
		return wrongExecutor.Execute(ctx, tools.ToolRequest{
			Name: "SearchKnowledge",
			Arguments: map[string]any{
				"query":          marker,
				"top_k":          10,
				"mode":           "bm25",
				"disable_rerank": true,
			},
		})
	})
}

func realAppRAGConfig(t *testing.T, root string) config.Config {
	t.Helper()
	serverURL := strings.TrimSpace(os.Getenv("CODE_AGENT_RAG_SERVER_URL"))
	secret := strings.TrimSpace(os.Getenv("CODE_AGENT_RAG_INTERNAL_SECRET"))
	userIDText := strings.TrimSpace(os.Getenv("CODE_AGENT_RAG_USER_ID"))
	if serverURL == "" || secret == "" || userIDText == "" {
		t.Fatal("CODE_AGENT_RAG_SERVER_URL, CODE_AGENT_RAG_INTERNAL_SECRET, and CODE_AGENT_RAG_USER_ID are required")
	}
	parsedUserID, err := strconv.ParseUint(userIDText, 10, 64)
	if err != nil || parsedUserID == 0 {
		t.Fatalf("CODE_AGENT_RAG_USER_ID must be a positive integer, got %q", userIDText)
	}

	cfg := config.Default(root)
	cfg.ProjectRoot = root
	cfg.WorkingDir = root
	cfg.OrchestratorAddr = "127.0.0.1:1"
	cfg.OrchestratorAutoStart = false
	cfg.RAGEnabled = true
	cfg.RAGServerURL = serverURL
	cfg.RAGInternalSecret = secret
	cfg.RAGUserID = uint(parsedUserID)
	cfg.RAGOrgTag = strings.TrimSpace(os.Getenv("CODE_AGENT_RAG_ORG_TAG"))
	cfg.RAGIngestPublic = false
	return cfg
}

func cleanupIntegrationApp(t *testing.T, app *App) {
	t.Helper()
	if app == nil {
		return
	}
	if app.mcp != nil {
		for _, server := range app.mcp.ListServers() {
			_ = app.mcp.Stop(server.Name)
		}
	}
	if app.orchestratorPM != nil {
		app.orchestratorPM.Stop()
	}
	if app.telemetry != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.telemetry.Shutdown(ctx); err != nil {
			t.Logf("telemetry shutdown: %v", err)
		}
	}
}

func newAppRAGMarker(t *testing.T) string {
	t.Helper()
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("ragapp%d%s", time.Now().UnixNano(), hex.EncodeToString(random))
}

func waitForAppRAGMarker(t *testing.T, marker, fileName, fileMD5 string, search func(context.Context) (tools.ToolResult, error)) string {
	t.Helper()
	pollCtx, cancelPoll := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancelPoll()
	var last tools.ToolResult
	var lastErr error
	for pollCtx.Err() == nil {
		requestCtx, cancel := context.WithTimeout(pollCtx, 30*time.Second)
		last, lastErr = search(requestCtx)
		cancel()
		if lastErr == nil && last.ExitCode == 0 {
			if hit, ok := matchingAppRAGHit(last.Output, marker, fileName, fileMD5); ok {
				return hit
			}
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-timer.C:
		case <-pollCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
		}
	}
	t.Fatalf("marker %q was not searchable before timeout; last tool error=%v exit=%d result error=%q output=%q", marker, lastErr, last.ExitCode, last.Error, last.Output)
	return ""
}

func assertAppRAGMarkerInvisible(t *testing.T, marker, fileName, fileMD5 string, search func(context.Context) (tools.ToolResult, error)) {
	t.Helper()
	pollCtx, cancelPoll := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelPoll()
	for pollCtx.Err() == nil {
		requestCtx, cancel := context.WithTimeout(pollCtx, 5*time.Second)
		result, err := search(requestCtx)
		cancel()
		if err != nil {
			t.Fatalf("wrong-user RAG search failed: %v", err)
		}
		if result.ExitCode != 0 {
			t.Fatalf("wrong-user RAG search returned tool failure: %s", result.Error)
		}
		if matchingAppRAGHitExists(result.Output, marker, fileName, fileMD5) {
			t.Fatalf("wrong user can see private RAG marker: %q", result.Output)
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-timer.C:
		case <-pollCtx.Done():
			if !timer.Stop() {
				<-timer.C
			}
		}
	}
}

func matchingAppRAGHit(output, marker, fileName, fileMD5 string) (string, bool) {
	if !matchingAppRAGHitExists(output, marker, fileName, fileMD5) {
		return "", false
	}
	fileIndex := strings.Index(output, "fileName: "+fileName)
	start := strings.LastIndex(output[:fileIndex], "\n[")
	if start < 0 {
		start = 0
	}
	end := strings.Index(output[fileIndex:], "\n[")
	if end < 0 {
		end = len(output) - fileIndex
	}
	return output[start : fileIndex+end], true
}

func matchingAppRAGHitExists(output, marker, fileName, fileMD5 string) bool {
	return strings.Contains(output, marker) && strings.Contains(output, "fileName: "+fileName) && strings.Contains(output, "fileMd5: "+fileMD5)
}
