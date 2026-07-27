package tools

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func (e *Executor) executeGrep(_ context.Context, args map[string]any) (ToolResult, error) {
	pattern, ok := stringArg(args, "pattern", "query")
	if !ok || pattern == "" {
		return ToolResult{Name: "Grep", Error: "pattern is required"}, fmt.Errorf("pattern is required")
	}
	searchRoot, _ := stringArg(args, "path", "root")
	if searchRoot == "" {
		searchRoot = "."
	}
	absRoot, err := workspacePath(e.Root, searchRoot)
	if err != nil {
		return ToolResult{Name: "Grep", Error: err.Error()}, err
	}

	options, err := grepOptionsFromArgs(args)
	if err != nil {
		return ToolResult{Name: "Grep", Error: err.Error(), ExitCode: 1}, err
	}
	if options.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	expr, err := regexp.Compile(pattern)
	if err != nil {
		return ToolResult{Name: "Grep", Error: err.Error(), ExitCode: 1}, err
	}

	matches := []grepMatch{}
	fileCounts := map[string]int{}
	files := map[string]struct{}{}
	err = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(e.Root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if options.Glob != "" {
			ok, err := matchGlob(options.Glob, rel)
			if err != nil {
				return err
			}
			if !ok {
				return nil
			}
		}

		fileMatches, err := grepFile(p, rel, expr, options)
		if err != nil {
			return nil
		}
		if len(fileMatches) > 0 {
			files[rel] = struct{}{}
			fileCounts[rel] = len(fileMatches)
			matches = append(matches, fileMatches...)
		}
		return nil
	})
	if err != nil {
		return ToolResult{Name: "Grep", Error: err.Error()}, err
	}

	output, headTruncated := formatGrepOutput(matches, files, fileCounts, options)
	output, byteTruncated := e.TruncateOutput(output)
	return ToolResult{Name: "Grep", Output: output, Truncated: headTruncated || byteTruncated}, nil
}

type grepOptions struct {
	OutputMode string
	IgnoreCase bool
	OnlyMatch  bool
	Before     int
	After      int
	HeadLimit  int
	Glob       string
}

type grepMatch struct {
	File       string
	Line       int
	Text       string
	MatchTexts []string
	Before     []grepContextLine
	After      []grepContextLine
}

type grepContextLine struct {
	Line int
	Text string
}

func grepOptionsFromArgs(args map[string]any) (grepOptions, error) {
	mode, _ := stringArg(args, "output_mode", "mode")
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "files_with_matches"
	}
	if mode != "content" && mode != "files_with_matches" && mode != "count" {
		return grepOptions{}, fmt.Errorf("unsupported output_mode %q", mode)
	}
	glob, _ := stringArg(args, "glob")
	before, _, err := intArg(args, "B")
	if err != nil {
		return grepOptions{}, err
	}
	after, _, err := intArg(args, "A")
	if err != nil {
		return grepOptions{}, err
	}
	contextLines, hasContext, err := intArg(args, "context")
	if err != nil {
		return grepOptions{}, err
	}
	if !hasContext {
		contextLines, hasContext, err = intArg(args, "C")
		if err != nil {
			return grepOptions{}, err
		}
	}
	if hasContext {
		before = contextLines
		after = contextLines
	}
	headLimit, hasHeadLimit, err := intArg(args, "head_limit")
	if err != nil {
		return grepOptions{}, err
	}
	if !hasHeadLimit {
		headLimit = 250
	}
	if before < 0 || after < 0 || headLimit < 0 {
		return grepOptions{}, fmt.Errorf("grep numeric options must be non-negative")
	}
	return grepOptions{
		OutputMode: mode,
		IgnoreCase: boolArg(args, "i", "-i", "ignore_case"),
		OnlyMatch:  boolArg(args, "o", "-o", "only_matching"),
		Before:     before,
		After:      after,
		HeadLimit:  headLimit,
		Glob:       glob,
	}, nil
}

func grepFile(path, rel string, expr *regexp.Regexp, options grepOptions) ([]grepMatch, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	lines := []string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	matches := []grepMatch{}
	for idx, line := range lines {
		if !expr.MatchString(line) {
			continue
		}
		match := grepMatch{File: rel, Line: idx + 1, Text: line}
		if options.OnlyMatch {
			match.MatchTexts = expr.FindAllString(line, -1)
		}
		if options.Before > 0 {
			start := idx - options.Before
			if start < 0 {
				start = 0
			}
			for i := start; i < idx; i++ {
				match.Before = append(match.Before, grepContextLine{Line: i + 1, Text: lines[i]})
			}
		}
		if options.After > 0 {
			end := idx + options.After
			if end >= len(lines) {
				end = len(lines) - 1
			}
			for i := idx + 1; i <= end; i++ {
				match.After = append(match.After, grepContextLine{Line: i + 1, Text: lines[i]})
			}
		}
		matches = append(matches, match)
	}
	return matches, nil
}

func formatGrepOutput(matches []grepMatch, files map[string]struct{}, counts map[string]int, options grepOptions) (string, bool) {
	switch options.OutputMode {
	case "files_with_matches":
		out := make([]string, 0, len(files))
		for file := range files {
			out = append(out, file)
		}
		sort.Strings(out)
		return limitLines(out, options.HeadLimit)
	case "count":
		out := make([]string, 0, len(counts))
		for file, count := range counts {
			out = append(out, fmt.Sprintf("%s:%d", file, count))
		}
		sort.Strings(out)
		return limitLines(out, options.HeadLimit)
	default:
		out := []string{}
		for _, match := range matches {
			for _, line := range match.Before {
				out = append(out, fmt.Sprintf("%s-%d-%s", match.File, line.Line, line.Text))
			}
			if options.OnlyMatch && len(match.MatchTexts) > 0 {
				for _, text := range match.MatchTexts {
					out = append(out, fmt.Sprintf("%s:%d:%s", match.File, match.Line, text))
				}
			} else {
				out = append(out, fmt.Sprintf("%s:%d:%s", match.File, match.Line, match.Text))
			}
			for _, line := range match.After {
				out = append(out, fmt.Sprintf("%s-%d-%s", match.File, line.Line, line.Text))
			}
		}
		return limitLines(out, options.HeadLimit)
	}
}

func limitLines(lines []string, limit int) (string, bool) {
	if limit > 0 && len(lines) > limit {
		lines = append(lines[:limit], fmt.Sprintf("[grep output truncated: %d more lines]", len(lines)-limit))
		return strings.Join(lines, "\n"), true
	}
	return strings.Join(lines, "\n"), false
}
