package tools

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestReadPDFUsesMinerUOCRAndReturnsImages(t *testing.T) {
	t.Setenv("CODE_AGENT_MINERU_COMMAND", "mineru")
	t.Setenv("CODE_AGENT_MINERU_BACKEND", "pipeline")
	originalFind := findPDFParser
	originalRun := runPDFParser
	t.Cleanup(func() {
		findPDFParser = originalFind
		runPDFParser = originalRun
	})
	findPDFParser = func(name string) (string, error) {
		if name != "mineru" {
			t.Fatalf("unexpected parser command: %s", name)
		}
		return "mineru-test", nil
	}
	runPDFParser = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "mineru-test" {
			t.Fatalf("unexpected executable: %s", name)
		}
		if !containsArgs(args, "-m", "ocr") || !containsArgs(args, "-b", "pipeline") || !containsArgs(args, "-s", "1") || !containsArgs(args, "-e", "2") {
			t.Fatalf("unexpected MinerU arguments: %v", args)
		}
		outputIndex := slices.Index(args, "-o")
		if outputIndex < 0 || outputIndex+1 >= len(args) {
			t.Fatalf("missing MinerU output directory: %v", args)
		}
		outputDir := args[outputIndex+1]
		assetDir := filepath.Join(outputDir, "document", "ocr", "images")
		if err := os.MkdirAll(assetDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outputDir, "document", "ocr", "document.md"), []byte("# OCR result\n\nDetected text."), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(assetDir, "figure.png"), []byte("png-bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
		return []byte("ok"), nil
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "document.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := NewExecutor(root).Execute(context.Background(), ToolRequest{
		Name: "Read",
		Arguments: map[string]any{
			"path":  "document.pdf",
			"pages": "2-3",
		},
	})
	if err != nil {
		t.Fatalf("read PDF: %v", err)
	}
	if !strings.Contains(result.Output, "parser: MinerU, mode: ocr, backend: pipeline") || !strings.Contains(result.Output, "Detected text") {
		t.Fatalf("unexpected PDF output: %q", result.Output)
	}
	if len(result.ContentBlocks) != 1 || result.ContentBlocks[0].MIME != "image/png" || string(result.ContentBlocks[0].ImageBlob) != "png-bytes" {
		t.Fatalf("unexpected PDF content blocks: %+v", result.ContentBlocks)
	}
}

func TestReadPDFRequiresMinerU(t *testing.T) {
	t.Setenv("CODE_AGENT_MINERU_COMMAND", "mineru")
	originalFind := findPDFParser
	t.Cleanup(func() { findPDFParser = originalFind })
	findPDFParser = func(string) (string, error) {
		return "", os.ErrNotExist
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "document.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := NewExecutor(root).Execute(context.Background(), ToolRequest{
		Name:      "Read",
		Arguments: map[string]any{"path": "document.pdf"},
	})
	if err == nil || result.ExitCode != 1 || !strings.Contains(result.Error, "MinerU is required") {
		t.Fatalf("expected MinerU dependency error, got result=%+v err=%v", result, err)
	}
}

func TestReadPDFRejectsAutoModeAndEnforcesOCR(t *testing.T) {
	t.Setenv("CODE_AGENT_MINERU_COMMAND", "mineru")
	t.Setenv("CODE_AGENT_MINERU_BACKEND", "pipeline")
	originalFind := findPDFParser
	originalRun := runPDFParser
	t.Cleanup(func() {
		findPDFParser = originalFind
		runPDFParser = originalRun
	})
	findPDFParser = func(name string) (string, error) {
		if name != "mineru" {
			t.Fatalf("unexpected parser command: %s", name)
		}
		return "mineru-test", nil
	}
	runPDFParser = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "mineru-test" {
			t.Fatalf("unexpected executable: %s", name)
		}
		for _, arg := range args {
			if arg == "auto" {
				t.Fatalf("MinerU must never run in auto mode: %v", args)
			}
		}
		if !containsArgs(args, "-m", "ocr") {
			t.Fatalf("expected explicit -m ocr, got %v", args)
		}
		outputIndex := slices.Index(args, "-o")
		if outputIndex < 0 || outputIndex+1 >= len(args) {
			t.Fatalf("missing MinerU output directory: %v", args)
		}
		outputDir := args[outputIndex+1]
		if err := os.MkdirAll(filepath.Join(outputDir, "document", "ocr"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(outputDir, "document", "ocr", "document.md"), []byte("# OCR result\n\nDetected text."), 0o644); err != nil {
			t.Fatal(err)
		}
		return []byte("ok"), nil
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "document.pdf"), []byte("%PDF-1.7\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Explicitly request ocr=false: the guard must still force OCR mode.
	result, err := NewExecutor(root).Execute(context.Background(), ToolRequest{
		Name:      "Read",
		Arguments: map[string]any{"path": "document.pdf", "ocr": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected successful read, got %+v", result)
	}
}

func containsArgs(args []string, key, value string) bool {
	index := slices.Index(args, key)
	return index >= 0 && index+1 < len(args) && args[index+1] == value
}
