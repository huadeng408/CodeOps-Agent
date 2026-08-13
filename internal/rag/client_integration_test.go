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
	fileName := newRAGFileName(t)
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
	pipelineCtx, cancelPipeline := context.WithTimeout(context.Background(), 2*time.Minute)
	pipeline, err := client.WaitForPipelineCompletion(pipelineCtx, requiredRAGEnv(t, "CODE_AGENT_RAG_RUN_ID"), ingested.FileMD5, time.Second)
	cancelPipeline()
	if err != nil {
		t.Fatalf("current-run pipeline did not complete: %v", err)
	}
	t.Logf("pipeline stages=%+v", pipeline.Stages)

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
	assertRAGMarkerInvisible(t, marker, func(ctx context.Context) (tools.ToolResult, error) {
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
		IngestProvenance: rag.IngestProvenanceConfig{
			SourceID:         requiredRAGEnv(t, "CODE_AGENT_RAG_SOURCE_ID"),
			SourcePathPrefix: requiredRAGEnv(t, "CODE_AGENT_RAG_SOURCE_PATH_PREFIX"),
			SourceURL:        strings.TrimSpace(os.Getenv("CODE_AGENT_RAG_SOURCE_URL")),
			SourceCommit:     requiredRAGEnv(t, "CODE_AGENT_RAG_SOURCE_COMMIT"),
			TargetIndex:      requiredRAGEnv(t, "CODE_AGENT_RAG_TARGET_INDEX"),
			CorpusGeneration: requiredRAGEnv(t, "CODE_AGENT_RAG_CORPUS_GENERATION"),
			RunID:            requiredRAGEnv(t, "CODE_AGENT_RAG_RUN_ID"),
		},
	}
}

func requiredRAGEnv(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func newRAGMarker(t *testing.T) string {
	t.Helper()
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("rage2e%d%s", time.Now().UnixNano(), hex.EncodeToString(random))
}

func newRAGFileName(t *testing.T) string {
	t.Helper()
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	return "rag-e2e-document-" + hex.EncodeToString(random) + ".txt"
}

func TestMatchingRAGHitRequiresAllValuesInOneBlock(t *testing.T) {
	emptyFileName := "Knowledge search results: 1\n\n[1]\nfileName: \nfileMd5: expected-md5\ntextContent:\nunique-marker"
	if _, ok := matchingRAGHit(emptyFileName, "unique-marker", "expected.txt", "expected-md5"); !ok {
		t.Fatal("run-scoped fileMd5 plus marker in one hit must not depend on lossy fileName metadata")
	}
	markerInFileName := "Knowledge search results: 1\n\n[1]\nfileName: unique-marker.txt\nfileMd5: expected-md5\ntextContent:\nother text"
	if _, ok := matchingRAGHit(markerInFileName, "unique-marker", "unique-marker.txt", "expected-md5"); ok {
		t.Fatal("marker in fileName must not count as a textContent match")
	}
	markerInMetadata := "Knowledge search results: 1\n\n[1]\nfileName: expected.txt\nfileMd5: expected-md5\norgTag: unique-marker\ntextContent:\nother text"
	if _, ok := matchingRAGHit(markerInMetadata, "unique-marker", "expected.txt", "expected-md5"); ok {
		t.Fatal("marker in metadata must not count as a textContent match")
	}
	output := "Knowledge search results: 2\n\n[1]\nfileName: expected.txt\nfileMd5: other-md5\ntextContent:\nunique-marker\n\n[2]\nfileName: other.txt\nfileMd5: expected-md5\ntextContent:\nother text"
	if _, ok := matchingRAGHit(output, "unique-marker", "expected.txt", "expected-md5"); ok {
		t.Fatal("values split across hits must not match")
	}
	bodyHeader := "Knowledge search results: 1\n\n[1]\nfileName: expected.txt\nfileMd5: expected-md5\ntextContent:\nfirst line\n[2]\nunique-marker"
	if _, ok := matchingRAGHit(bodyHeader, "unique-marker", "expected.txt", "expected-md5"); !ok {
		t.Fatal("header-like textContent line must not split the hit")
	}
	output += "\n\n[3]\nfileName: expected.txt\nfileMd5: expected-md5\ntextContent:\nunique-marker"
	if hit, ok := matchingRAGHit(output, "unique-marker", "expected.txt", "expected-md5"); !ok || !strings.HasPrefix(hit, "[3]") {
		t.Fatalf("same-hit values did not match block 3: %q", hit)
	}
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

func assertRAGMarkerInvisible(t *testing.T, marker string, search func(context.Context) (tools.ToolResult, error)) {
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
		if strings.Contains(result.Output, marker) {
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
	for _, hit := range parseRAGHits(output) {
		if strings.Contains(hit.textContent, marker) && hit.fileMD5 == fileMD5 {
			return hit.block, true
		}
	}
	return "", false
}

type parsedRAGHit struct {
	block       string
	fileName    string
	fileMD5     string
	textContent string
}

func parseRAGHits(output string) []parsedRAGHit {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	var starts []int
	for index := range lines {
		if isRAGHitStart(lines, index) {
			starts = append(starts, index)
		}
	}
	var hits []parsedRAGHit
	for index, start := range starts {
		end := len(lines)
		if index+1 < len(starts) {
			end = starts[index+1]
		}
		hit := parsedRAGHit{block: strings.Join(lines[start:end], "\n")}
		for lineIndex := start + 1; lineIndex < end; lineIndex++ {
			line := strings.TrimSpace(lines[lineIndex])
			switch {
			case strings.HasPrefix(line, "fileName: "):
				hit.fileName = strings.TrimPrefix(line, "fileName: ")
			case strings.HasPrefix(line, "fileMd5: "):
				hit.fileMD5 = strings.TrimPrefix(line, "fileMd5: ")
			case line == "textContent:":
				hit.textContent = strings.Join(lines[lineIndex+1:end], "\n")
				lineIndex = end
			}
		}
		hits = append(hits, hit)
	}
	return hits
}

func isRAGHitStart(lines []string, index int) bool {
	line := strings.TrimSpace(lines[index])
	if len(line) < 3 || line[0] != '[' || line[len(line)-1] != ']' {
		return false
	}
	if _, err := strconv.Atoi(line[1 : len(line)-1]); err != nil {
		return false
	}
	return (index == 0 || strings.TrimSpace(lines[index-1]) == "") && index+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[index+1]), "fileName:")
}
