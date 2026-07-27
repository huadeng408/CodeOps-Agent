package codeagent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code-agent/internal/tools"
)

// minimalNotebookBody 构造一个最小的合法 .ipynb（nbformat 4.5，含一个 code 与一个 markdown 单元格）。
func minimalNotebookBody(t *testing.T) string {
	t.Helper()
	doc := map[string]any{
		"cells": []map[string]any{
			{
				"cell_type": "code",
				"id":        "code-1",
				"source":    []string{"print('hi')\n"},
				"metadata":  map[string]any{},
				"outputs":   []any{},
			},
			{
				"cell_type": "markdown",
				"id":        "md-1",
				"source":    []string{"# Title\n"},
				"metadata":  map[string]any{},
			},
		},
		"metadata":       map[string]any{},
		"nbformat":       4,
		"nbformat_minor": 5,
	}
	data, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		t.Fatalf("marshal notebook: %v", err)
	}
	return string(data)
}

func writeNotebook(t *testing.T, root, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "nb.ipynb"), []byte(body), 0o644); err != nil {
		t.Fatalf("write notebook: %v", err)
	}
}

func TestNotebookEditInsertReplaceDelete(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)
	writeNotebook(t, root, minimalNotebookBody(t))

	// 1) insert a markdown cell after md-1.
	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "NotebookEdit",
		Arguments: map[string]any{
			"path":      "nb.ipynb",
			"edit_mode": "insert",
			"cell_id":   "md-1",
			"cell_type": "markdown",
			"source":    "appended note",
		},
	}); err != nil {
		t.Fatalf("insert failed: %v", err)
	}

	// 2) replace the inserted cell (index 2) with code.
	res, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "NotebookEdit",
		Arguments: map[string]any{
			"path":       "nb.ipynb",
			"edit_mode":  "replace",
			"cell_index": 2,
			"cell_type":  "code",
			"source":     "x = 1",
		},
	})
	if err != nil {
		t.Fatalf("replace failed: %v", err)
	}
	if len(res.Changes) != 1 || res.Changes[0].After == res.Changes[0].Before {
		t.Fatalf("replace should change notebook content: %#v", res.Changes)
	}

	// Validate the on-disk structure: 3 cells, the last one is code with source "x = 1".
	data, err := os.ReadFile(filepath.Join(root, "nb.ipynb"))
	if err != nil {
		t.Fatalf("re-read notebook: %v", err)
	}
	var doc struct {
		Cells []struct {
			CellType string   `json:"cell_type"`
			Source   []string `json:"source"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode notebook: %v", err)
	}
	if len(doc.Cells) != 3 {
		t.Fatalf("expected 3 cells after insert+replace, got %d", len(doc.Cells))
	}
	last := doc.Cells[2]
	if last.CellType != "code" {
		t.Fatalf("expected last cell type code, got %q", last.CellType)
	}
	if got := strings.Join(last.Source, ""); got != "x = 1" {
		t.Fatalf("expected replaced source %q, got %q", "x = 1", got)
	}

	// 3) delete the code-1 cell by id -> 2 cells remain.
	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "NotebookEdit",
		Arguments: map[string]any{
			"path":      "nb.ipynb",
			"edit_mode": "delete",
			"cell_id":   "code-1",
		},
	}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	data, err = os.ReadFile(filepath.Join(root, "nb.ipynb"))
	if err != nil {
		t.Fatalf("re-read notebook after delete: %v", err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode notebook after delete: %v", err)
	}
	if len(doc.Cells) != 2 {
		t.Fatalf("expected 2 cells after delete, got %d", len(doc.Cells))
	}
}

func TestNotebookEditRejectsBadEditModeAndMissingCell(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)
	writeNotebook(t, root, minimalNotebookBody(t))

	if _, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "NotebookEdit",
		Arguments: map[string]any{
			"path":      "nb.ipynb",
			"edit_mode": "bogus",
		},
	}); err == nil {
		t.Fatal("expected invalid edit_mode to fail")
	}

	res, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "NotebookEdit",
		Arguments: map[string]any{
			"path":      "nb.ipynb",
			"edit_mode": "delete",
			"cell_id":   "nope",
		},
	})
	if err == nil || !strings.Contains(res.Error, "not found") {
		t.Fatalf("expected missing cell_id error, got res=%+v err=%v", res, err)
	}
}

func TestReadNotebookRendersCellSummary(t *testing.T) {
	root := t.TempDir()
	executor := tools.NewExecutor(root)
	writeNotebook(t, root, minimalNotebookBody(t))

	result, err := executor.Execute(context.Background(), tools.ToolRequest{
		Name: "Read", Arguments: map[string]any{"path": "nb.ipynb"},
	})
	if err != nil {
		t.Fatalf("read notebook failed: %v", err)
	}
	// Should be a readable cell summary, not raw JSON.
	if !strings.Contains(result.Output, "[Notebook nb.ipynb") {
		t.Fatalf("missing notebook header: %q", result.Output)
	}
	if !strings.Contains(result.Output, "Cell 0 (code)") || !strings.Contains(result.Output, "print('hi')") {
		t.Fatalf("code cell summary missing: %q", result.Output)
	}
	if !strings.Contains(result.Output, "Cell 1 (markdown)") || !strings.Contains(result.Output, "# Title") {
		t.Fatalf("markdown cell summary missing: %q", result.Output)
	}
	if strings.HasPrefix(strings.TrimSpace(result.Output), "{") {
		t.Fatalf("notebook read returned raw JSON instead of a summary: %q", result.Output)
	}
}
