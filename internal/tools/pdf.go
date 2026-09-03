package tools

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	minerUTimeout     = 10 * time.Minute
	maxPDFImageBlocks = 20
	maxPDFImageBytes  = 5 << 20
	maxPDFImagesTotal = maxMultimodalBytes
)

var (
	findPDFParser = exec.LookPath
	runPDFParser  = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
)

// readPDF uses MinerU's OCR pipeline. MinerU's extracted Markdown is returned
// as tool text, while extracted figures are carried as binary content blocks.
// Apache Tika and pdftotext are intentionally not part of this path.
func (e *Executor) readPDF(
	ctx context.Context,
	displayPath string,
	absPath string,
	data []byte,
	args map[string]any,
) (ToolResult, error) {
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		err := fmt.Errorf("not a valid PDF file: %s", displayPath)
		return ToolResult{Name: "Read", Error: err.Error(), ExitCode: 1}, err
	}

	pages, _ := stringArg(args, "pages", "page")
	pages = strings.TrimSpace(pages)
	if pages == "" {
		pages = "all"
	}
	mode := "ocr"
	// The harness contract requires explicit OCR for every PDF. An
	// ocr=false request must not downgrade MinerU to auto mode; the flag is
	// accepted but ignored so the parser boundary stays enforceable.
	if _, configured := args["ocr"]; configured {
		delete(args, "ocr")
	}
	backend := strings.TrimSpace(os.Getenv("CODE_AGENT_MINERU_BACKEND"))
	if backend == "" {
		backend = "pipeline"
	}

	command := strings.TrimSpace(os.Getenv("CODE_AGENT_MINERU_COMMAND"))
	if command == "" {
		command = "mineru"
	}
	executable, err := findPDFParser(command)
	if err != nil {
		err = fmt.Errorf(
			"MinerU is required to read PDF files (command %q not found); install MinerU or set CODE_AGENT_MINERU_COMMAND",
			command,
		)
		return ToolResult{Name: "Read", Error: err.Error(), ExitCode: 1}, err
	}

	outputDir, err := os.MkdirTemp("", "code-agent-mineru-")
	if err != nil {
		return ToolResult{Name: "Read", Error: err.Error(), ExitCode: 1}, err
	}
	defer os.RemoveAll(outputDir)

	commandArgs := []string{"-p", absPath, "-o", outputDir, "-m", mode, "-b", backend}
	if first, last, ok := parsePDFPageRange(pages); ok {
		commandArgs = append(commandArgs, "-s", strconv.Itoa(first-1), "-e", strconv.Itoa(last-1))
	}
	runCtx, cancel := context.WithTimeout(ctx, minerUTimeout)
	defer cancel()
	commandOutput, runErr := runPDFParser(runCtx, executable, commandArgs...)
	if runErr != nil {
		detail, _ := e.TruncateOutput(strings.TrimSpace(string(commandOutput)))
		detail = RedactSensitive(detail)
		if detail != "" {
			runErr = fmt.Errorf("%w: %s", runErr, detail)
		}
		err = fmt.Errorf("MinerU PDF parsing failed in %s mode: %w", mode, runErr)
		return ToolResult{Name: "Read", Error: err.Error(), ExitCode: 1}, err
	}

	markdown, imageBlocks, err := collectMinerUOutput(outputDir)
	if err != nil {
		err = fmt.Errorf("read MinerU output: %w", err)
		return ToolResult{Name: "Read", Error: err.Error(), ExitCode: 1}, err
	}
	if strings.TrimSpace(markdown) == "" && len(imageBlocks) == 0 {
		err = fmt.Errorf("MinerU PDF parsing produced no Markdown or image content")
		return ToolResult{Name: "Read", Error: err.Error(), ExitCode: 1}, err
	}

	header := fmt.Sprintf(
		"[PDF %s: %d bytes, pages: %s, parser: MinerU, mode: %s, backend: %s, images: %d]",
		displayPath, len(data), pages, mode, backend, len(imageBlocks),
	)
	output := header
	if strings.TrimSpace(markdown) != "" {
		output += "\n" + markdown
	}
	return ToolResult{
		Name:          "Read",
		Output:        output,
		ContentBlocks: imageBlocks,
	}, nil
}

func collectMinerUOutput(root string) (string, []ContentBlock, error) {
	var markdownPaths []string
	var imagePaths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		switch strings.ToLower(filepath.Ext(path)) {
		case ".md":
			markdownPaths = append(markdownPaths, path)
		case ".png", ".jpg", ".jpeg", ".gif", ".webp":
			imagePaths = append(imagePaths, path)
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	sort.Strings(markdownPaths)
	sort.Strings(imagePaths)

	markdownParts := make([]string, 0, len(markdownPaths))
	for _, path := range markdownPaths {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", nil, readErr
		}
		if text := strings.TrimSpace(string(content)); text != "" {
			markdownParts = append(markdownParts, text)
		}
	}

	blocks := make([]ContentBlock, 0, min(len(imagePaths), maxPDFImageBlocks))
	totalBytes := 0
	for _, path := range imagePaths {
		if len(blocks) >= maxPDFImageBlocks {
			break
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			return "", nil, statErr
		}
		if info.Size() <= 0 || info.Size() > maxPDFImageBytes || totalBytes+int(info.Size()) > maxPDFImagesTotal {
			continue
		}
		blob, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", nil, readErr
		}
		blocks = append(blocks, ContentBlock{ImageBlob: blob, MIME: imageMime(path)})
		totalBytes += len(blob)
	}
	return strings.Join(markdownParts, "\n\n"), blocks, nil
}
