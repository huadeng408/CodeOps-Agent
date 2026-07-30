package tools

import (
	"context"
	"fmt"
	"math"
	"strings"

	"code-agent/internal/rag"
)

const (
	defaultRAGTopK = 5
	maxRAGTopK     = 50
)

func (e *Executor) executeSearchKnowledge(ctx context.Context, args map[string]any) (ToolResult, error) {
	query, ok := stringArg(args, "query")
	query = strings.TrimSpace(query)
	if !ok || query == "" {
		return ragToolFailure("query is required"), nil
	}

	topK, err := ragTopKArg(args)
	if err != nil {
		return ragToolFailure(err.Error()), nil
	}
	mode := "hybrid"
	if value, exists := args["mode"]; exists {
		var modeOK bool
		mode, modeOK = value.(string)
		if !modeOK || !supportedRAGMode(mode) {
			return ragToolFailure("mode must be one of hybrid, bm25, or vector"), nil
		}
	}
	disableRerank := false
	if value, exists := args["disable_rerank"]; exists {
		var boolOK bool
		disableRerank, boolOK = value.(bool)
		if !boolOK {
			return ragToolFailure("disable_rerank must be a boolean"), nil
		}
	}

	e.mu.Lock()
	searcher := e.rag
	e.mu.Unlock()
	if searcher == nil {
		return ragToolFailure(rag.ErrUnavailable.Error()), nil
	}
	results, err := searcher.Search(ctx, rag.SearchOptions{
		Query:         query,
		TopK:          topK,
		Mode:          mode,
		DisableRerank: disableRerank,
	})
	if err != nil {
		return ragToolFailure(err.Error()), nil
	}

	output := formatRAGResults(results)
	output, truncated := e.TruncateOutput(output)
	return ToolResult{Name: "SearchKnowledge", Output: output, Truncated: truncated}, nil
}

func ragTopKArg(args map[string]any) (int, error) {
	value, exists := args["top_k"]
	if !exists {
		return defaultRAGTopK, nil
	}
	var topK int
	switch number := value.(type) {
	case int:
		topK = number
	case float64:
		if math.Trunc(number) != number || number > float64(maxRAGTopK) || number < 1 {
			return 0, fmt.Errorf("top_k must be an integer between 1 and %d", maxRAGTopK)
		}
		topK = int(number)
	default:
		return 0, fmt.Errorf("top_k must be an integer between 1 and %d", maxRAGTopK)
	}
	if topK < 1 || topK > maxRAGTopK {
		return 0, fmt.Errorf("top_k must be an integer between 1 and %d", maxRAGTopK)
	}
	return topK, nil
}

func supportedRAGMode(mode string) bool {
	switch mode {
	case "hybrid", "bm25", "vector":
		return true
	default:
		return false
	}
}

func formatRAGResults(results []rag.SearchResult) string {
	if len(results) == 0 {
		return "No knowledge results found."
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Knowledge search results: %d\n", len(results))
	for index, result := range results {
		fmt.Fprintf(&output, "\n[%d]\nfileName: %s\nchunkId: %d\nscore: %.6f\n", index+1, result.FileName, result.ChunkID, result.Score)
		if result.FileMD5 != "" {
			fmt.Fprintf(&output, "fileMd5: %s\n", result.FileMD5)
		}
		if result.OrgTag != "" {
			fmt.Fprintf(&output, "orgTag: %s\n", result.OrgTag)
		}
		fmt.Fprintf(&output, "textContent:\n%s\n", result.TextContent)
	}
	return strings.TrimSpace(output.String())
}

func ragToolFailure(message string) ToolResult {
	return ToolResult{Name: "SearchKnowledge", Error: message, ExitCode: 1}
}
