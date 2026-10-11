// opensandbox-profile renders nonsecret build inputs for the pinned local profile.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"regexp"
	"strings"
)

type profile struct {
	SourceSHA       string   `json:"source_sha"`
	TaskImages      []string `json:"task_images"`
	ExecdImage      string   `json:"execd_image"`
	EgressImage     string   `json:"egress_image"`
	ExecdSHA256     string   `json:"execd_sha256"`
	BootstrapSHA256 string   `json:"bootstrap_sha256"`
	WorkspaceRoot   string   `json:"workspace_root"`
	Network         string   `json:"network"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: opensandbox-profile <prepared-server-directory> <nonsecret-profile.json>")
		os.Exit(2)
	}
	if err := render(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "profile rendering refused:", err)
		os.Exit(1)
	}
}

func render(directory, input string) error {
	file, err := os.Open(input)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 {
		return errors.New("profile input exceeds its bound")
	}
	var p profile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid profile input")
	}
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	pin := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	if p.SourceSHA != "f89eff24bbd15ae1c37a47769f7667849e679be0" || len(p.TaskImages) != 1 || !pin.MatchString(p.TaskImages[0]) || !pin.MatchString(p.ExecdImage) || !pin.MatchString(p.EgressImage) || !hex64.MatchString(p.ExecdSHA256) || p.BootstrapSHA256 != "499114628856ef088dacd848daefb90ba141fdd39d23608906357f61e6fd4bef" || !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{1,63}$`).MatchString(p.Network) || !strings.HasPrefix(p.WorkspaceRoot, "/run/desktop/mnt/host/") || path.Clean(p.WorkspaceRoot) != p.WorkspaceRoot || strings.ContainsAny(p.WorkspaceRoot, "\r\n\x00\"\\") {
		return errors.New("profile identities or host mapping are incomplete")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	serverFiles := map[string]string{}
	err = fs.WalkDir(root.FS(), "opensandbox_server", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("server source contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("server source is not regular")
		}
		data, err := root.ReadFile(name)
		if err != nil {
			return err
		}
		serverFiles[name] = digest(data)
		return nil
	})
	if err != nil {
		return err
	}
	if serverFiles["opensandbox_server/codeops_profile.py"] == "" {
		return errors.New("restricted server profile is missing")
	}
	config := fmt.Sprintf(`[server]
host = "0.0.0.0"
port = 8080
max_sandbox_timeout_seconds = 120
[proxy]
resolve_internal = true
[runtime]
type = "docker"
execd_image = %q
execd_run_as_init = true
[storage]
allowed_host_paths = [%q]
[store]
type = "sqlite"
path = "/run/codeops-control/runtime.sqlite"
[docker]
network_mode = %q
publish_host = "127.0.0.1"
port_range_min = 40000
port_range_max = 40200
drop_capabilities = ["ALL"]
no_new_privileges = true
pids_limit = 128
[egress]
image = %q
mode = "dns+nft"
disable_ipv6 = false
`, p.ExecdImage, p.WorkspaceRoot, p.Network, p.EgressImage)
	profileMap := map[string]any{"source_sha": p.SourceSHA, "task_images": p.TaskImages, "execd_image": p.ExecdImage, "egress_image": p.EgressImage, "execd_sha256": p.ExecdSHA256, "bootstrap_sha256": p.BootstrapSHA256, "workspace_root": p.WorkspaceRoot, "network": p.Network}
	inputs := map[string]any{"profile_without_hash": profileMap, "config_sha256": digest([]byte(config)), "server_files": serverFiles}
	canonical, err := json.Marshal(inputs)
	if err != nil {
		return err
	}
	policyHash := digest(canonical)
	// Clone before adding the hash: the immutable preimage excludes this field.
	outputProfile := map[string]any{}
	for key, value := range profileMap {
		outputProfile[key] = value
	}
	outputProfile["policy_sha256"] = policyHash
	profileBytes, err := json.MarshalIndent(outputProfile, "", "  ")
	if err != nil {
		return err
	}
	inputsBytes, err := json.MarshalIndent(inputs, "", "  ")
	if err != nil {
		return err
	}
	outputs := []struct {
		name string
		data []byte
	}{{"codeops-runtime.toml", []byte(config)}, {"codeops-profile.json", append(profileBytes, '\n')}, {"codeops-policy-inputs.json", append(inputsBytes, '\n')}}
	for _, item := range outputs {
		if _, err := root.Stat(item.name); !errors.Is(err, fs.ErrNotExist) {
			return errors.New("rendered files already exist or are unavailable; use a fresh build directory")
		}
	}
	for _, item := range outputs {
		output, err := root.OpenFile(item.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := output.Write(item.data)
		closeErr := output.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			return err
		}
	}
	fmt.Println(policyHash)
	return nil
}

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
