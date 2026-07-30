package rag_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"code-agent/internal/rag"
	"code-agent/internal/tools"
)

func TestRealRAGIngestThenSearchKnowledge(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_RAG_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_RAG_E2E=1 to run the real RAG integration")
	}

	cfg := realRAGConfig(t)
	client := rag.NewClient(cfg)
	marker := newRAGMarker(t)
	fileName := marker + ".txt"
	filePath := filepath.Join(t.TempDir(), fileName)
	content := fmt.Sprintf("Real RAG end-to-end knowledge marker %s. This sentence must be indexed and returned by BM25 search.\n", marker)
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("marker=%s", marker)

	ingestCtx, cancelIngest := context.WithTimeout(context.Background(), 2*time.Minute)
	ingested, err := client.Ingest(ingestCtx, filePath)
	cancelIngest()
	if err != nil {
		t.Fatalf("real RAG ingest failed: %v", err)
	}
	if ingested == nil || strings.TrimSpace(ingested.FileMD5) == "" {
		t.Fatalf("real RAG ingest returned no file MD5: %#v", ingested)
	}
	t.Logf("fileMd5=%s", ingested.FileMD5)

	executor := tools.NewExecutor(t.TempDir())
	executor.SetRAGSearcher(client)
	topHit := waitForRAGMarker(t, marker, fileName, ingested.FileMD5, func(ctx context.Context) (tools.ToolResult, error) {
		return executor.Execute(ctx, tools.ToolRequest{
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
	if cfg.UserID == wrongUserID {
		wrongUserID = 2
	}
	wrongConfig := cfg
	wrongConfig.UserID = wrongUserID
	wrongClient := rag.NewClient(wrongConfig)
	wrongExecutor := tools.NewExecutor(t.TempDir())
	wrongExecutor.SetRAGSearcher(wrongClient)
	assertRAGMarkerInvisible(t, marker, fileName, ingested.FileMD5, func(ctx context.Context) (tools.ToolResult, error) {
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

func realRAGConfig(t *testing.T) rag.Config {
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
	return rag.Config{
		Enabled:       true,
		BaseURL:       serverURL,
		InternalToken: secret,
		UserID:        uint(parsedUserID),
		OrgTag:        strings.TrimSpace(os.Getenv("CODE_AGENT_RAG_ORG_TAG")),
		IngestPublic:  false,
	}
}

func newRAGMarker(t *testing.T) string {
	t.Helper()
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("rage2e%d%s", time.Now().UnixNano(), hex.EncodeToString(random))
}

func waitForRAGMarker(t *testing.T, marker, fileName, fileMD5 string, search func(context.Context) (tools.ToolResult, error)) string {
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
			if hit, ok := matchingRAGHit(last.Output, marker, fileName, fileMD5); ok {
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

func assertRAGMarkerInvisible(t *testing.T, marker, fileName, fileMD5 string, search func(context.Context) (tools.ToolResult, error)) {
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
		if matchingRAGHitExists(result.Output, marker, fileName, fileMD5) {
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

func matchingRAGHit(output, marker, fileName, fileMD5 string) (string, bool) {
	if !matchingRAGHitExists(output, marker, fileName, fileMD5) {
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

func matchingRAGHitExists(output, marker, fileName, fileMD5 string) bool {
	return strings.Contains(output, marker) && strings.Contains(output, "fileName: "+fileName) && strings.Contains(output, "fileMd5: "+fileMD5)
}
