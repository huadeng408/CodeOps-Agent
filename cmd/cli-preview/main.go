package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"code-agent/internal/cli"
)

func main() {
	scenario := flag.String("scenario", "all", "preview scenario: all, success, failure, permission, narrow, ascii")
	flag.Parse()

	capabilities := cli.TerminalCapabilities{Color: true, Unicode: true, Width: 96}
	if *scenario == "narrow" {
		capabilities.Width = 38
	}
	if *scenario == "ascii" {
		capabilities.Color = false
		capabilities.Unicode = false
	}
	renderer := cli.NewStreamRendererWithCapabilities(os.Stdout, capabilities)

	switch *scenario {
	case "all":
		renderAll(renderer)
	case "success":
		renderSuccess(renderer)
	case "failure":
		renderFailure(renderer)
	case "permission":
		renderPermission(renderer)
	case "narrow", "ascii":
		renderAll(renderer)
	default:
		fmt.Fprintf(os.Stderr, "unknown scenario %q\n", *scenario)
		os.Exit(2)
	}
}

func renderAll(renderer *cli.StreamRenderer) {
	renderer.PrintBootstrap(cli.BootstrapView{Version: "v0.1", Branch: "main", Workspace: `D:\vscode\localcode`, Mode: "chat", Model: "deepseek-v4-pro"})
	renderer.PrintLine("/help | /ingest | /diff | /plan")
	renderSuccess(renderer)
	renderFailure(renderer)
	renderPermission(renderer)
	renderer.PrintQuestion(cli.QuestionView{Question: "Choose an index", Options: []cli.QuestionOption{{Label: "hybrid", Description: "dense + lexical"}, {Label: "visual", Description: "image-aware"}}})
	renderer.PrintDiff(cli.DiffSummary{Files: 4, Added: 82, Removed: 17, Lines: []string{"M internal/cli/renderer.go", "A internal/cli/theme.go"}, Truncated: true})
	renderer.PrintStatus("chat | errors 0 | tokens 1200 in / 300 out | cost $0.0120")
}

func renderSuccess(renderer *cli.StreamRenderer) {
	renderer.ToolStarted("Test", "internal/rag")
	renderer.ToolCompleted(cli.ToolEvent{Name: "Test", Target: "internal/rag", Summary: "18 passed", Duration: 1400 * time.Millisecond})
}

func renderFailure(renderer *cli.StreamRenderer) {
	renderer.ToolCompleted(cli.ToolEvent{Name: "Test", Target: "internal/rag", ExitCode: 1, Duration: 2100 * time.Millisecond, Detail: "FAIL TestSpan\nexpected closed, got active", Truncated: true})
}

func renderPermission(renderer *cli.StreamRenderer) {
	renderer.PrintPermission(cli.PermissionView{Tool: "Shell", Reason: "changes repository state", Parameters: `git commit -m "fix: close rag spans"`, AllowSession: true})
}
