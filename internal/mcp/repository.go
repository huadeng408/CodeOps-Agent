package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"code-agent/internal/safety"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	RepositoryServerName    = "codeops-repository"
	RepositoryServerVersion = "1.0.0"
	RepositoryToolName      = "RepositoryManifest"
	RepositoryServerLicense = "Apache-2.0"
	repositoryInputSchema   = `{"type":"object","properties":{"file":{"type":"string","enum":["go.mod","pyproject.toml","package.json"]}},"required":["file"],"additionalProperties":false}`
)

// RepositoryServerConfig fixes the only bundled MCP capability. Args are
// supplied by the executable entry point, not by a repository or model.
func RepositoryServerConfig(executable string, args []string, workspace string) (ServerConfig, error) {
	checksum, err := executableSHA256(executable)
	if err != nil {
		return ServerConfig{}, errors.New("repository MCP executable cannot be pinned")
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(repositoryInputSchema), &schema); err != nil {
		return ServerConfig{}, err
	}
	canonical, err := json.Marshal(schema)
	if err != nil {
		return ServerConfig{}, err
	}
	return ServerConfig{Name: RepositoryServerName, Command: executable, Args: args, WorkingDir: workspace,
		Trust: &ServerTrust{Name: RepositoryServerName, Version: RepositoryServerVersion, License: RepositoryServerLicense, ExecutableSHA256: checksum,
			Tools: map[string]string{RepositoryToolName: fmt.Sprintf("%x", sha256.Sum256(canonical))}}}, nil
}

func executableSHA256(name string) (string, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}

type repositoryManifestArgs struct {
	File string `json:"file"`
}

type repositoryManifest struct {
	File    string `json:"file"`
	Content string `json:"content"`
	SHA256  string `json:"sha256"`
}

// RunRepositoryServer exposes only three read-only manifests over stdio. It has
// no command, network, workspace selection, or credential capability.
func RunRepositoryServer(ctx context.Context, workspace string) error {
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return errors.New("repository MCP workspace is unavailable")
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return errors.New("repository MCP workspace is unavailable")
	}
	defer root.Close()
	var schema map[string]any
	if err := json.Unmarshal([]byte(repositoryInputSchema), &schema); err != nil {
		return err
	}
	server := sdk.NewServer(&sdk.Implementation{Name: RepositoryServerName, Version: RepositoryServerVersion}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: RepositoryToolName, Description: "Read an approved repository dependency manifest and its SHA-256; read-only, offline, current task workspace only.", InputSchema: schema},
		func(ctx context.Context, _ *sdk.CallToolRequest, args repositoryManifestArgs) (*sdk.CallToolResult, repositoryManifest, error) {
			if err := ctx.Err(); err != nil {
				return nil, repositoryManifest{}, err
			}
			switch args.File {
			case "go.mod", "pyproject.toml", "package.json":
			default:
				return nil, repositoryManifest{}, errors.New("repository MCP manifest is not approved")
			}
			unpin, err := safety.PinReadOnlyFiles(filepath.Join(root.Name(), args.File))
			if err != nil {
				return nil, repositoryManifest{}, errors.New("repository MCP manifest cannot be pinned")
			}
			defer unpin()
			before, err := root.Lstat(args.File)
			if err != nil || !before.Mode().IsRegular() {
				return nil, repositoryManifest{}, errors.New("repository MCP manifest is unavailable or not a regular file")
			}
			file, err := root.Open(args.File)
			if err != nil {
				return nil, repositoryManifest{}, errors.New("repository MCP manifest cannot be opened")
			}
			defer file.Close()
			opened, err := file.Stat()
			if err != nil || !os.SameFile(before, opened) {
				return nil, repositoryManifest{}, errors.New("repository MCP manifest identity changed")
			}
			data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
			if err != nil || len(data) > 1<<20 || !utf8.Valid(data) {
				return nil, repositoryManifest{}, errors.New("repository MCP manifest must be UTF-8 and at most 1 MiB")
			}
			after, err := root.Lstat(args.File)
			if err != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
				return nil, repositoryManifest{}, errors.New("repository MCP manifest changed while reading")
			}
			return nil, repositoryManifest{File: args.File, Content: string(data), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}, nil
		})
	return server.Run(ctx, &sdk.StdioTransport{})
}
