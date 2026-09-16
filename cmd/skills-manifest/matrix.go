package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"code-agent/internal/skills"
)

type matrixSourcePin struct {
	GitSHA         string `json:"git_sha"`
	DirtyHash      string `json:"dirty_hash"`
	UntrackedFiles int    `json:"untracked_files"`
}

type matrixFailure struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

type matrixSkill struct {
	Name           string   `json:"name"`
	Tools          []string `json:"tools,omitempty"`
	ModelInvocable bool     `json:"model_invocable"`
	UserInvocable  bool     `json:"user_invocable"`
	BodySHA256     string   `json:"body_sha256,omitempty"`
}

type matrixSummary struct {
	Denominator           int             `json:"denominator"`
	Passed                int             `json:"passed"`
	Failures              []matrixFailure `json:"failures"`
	ProductionLoader      bool            `json:"production_loader"`
	MetadataOnlyDiscovery bool            `json:"metadata_only_discovery"`
	LazyBodyLoads         int             `json:"lazy_body_loads"`
}

type executionMatrix struct {
	SchemaVersion  int             `json:"schema_version"`
	Status         string          `json:"status"`
	RunID          string          `json:"run_id"`
	SourcePin      matrixSourcePin `json:"source_pin"`
	ManifestSHA256 string          `json:"manifest_sha256"`
	CatalogSHA256  string          `json:"catalog_sha256"`
	Execution      matrixSummary   `json:"execution_matrix"`
	Skills         []matrixSkill   `json:"skills"`
}

func writeExecutionMatrix(
	manager *skills.Manager,
	manifestPath string,
	matrixPath string,
	repoRoot string,
	runID string,
) (executionMatrix, error) {
	if strings.TrimSpace(runID) == "" || filepath.Base(runID) != runID || runID == "." || runID == ".." {
		return executionMatrix{}, errors.New("run-id must be one safe directory name")
	}
	pin, err := skillMatrixSourcePin(repoRoot)
	if err != nil {
		return executionMatrix{}, fmt.Errorf("pin matrix source: %w", err)
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return executionMatrix{}, fmt.Errorf("read Skill manifest: %w", err)
	}
	metadataOnly, manifestNames, err := inspectMetadataManifest(manifest)
	if err != nil {
		return executionMatrix{}, err
	}

	items := manager.List()
	results := make([]matrixSkill, 0, len(items))
	failures := make([]matrixFailure, 0)
	loadedCount := 0
	for _, item := range items {
		result := matrixSkill{
			Name:           item.Name,
			Tools:          append([]string(nil), item.Tools...),
			ModelInvocable: item.Invocation.ModelInvocable,
			UserInvocable:  item.Invocation.UserInvocable,
		}
		loaded, category := loadSkillForMatrix(manager, item)
		if category != "" {
			failures = append(failures, matrixFailure{Name: item.Name, Category: category})
			results = append(results, result)
			continue
		}
		digest := sha256.Sum256([]byte(loaded.Prompt))
		result.BodySHA256 = hex.EncodeToString(digest[:])
		loadedCount++
		results = append(results, result)
	}

	if len(manifestNames) != len(items) {
		failures = append(failures, matrixFailure{Name: "catalog", Category: "manifest_count_mismatch"})
	} else {
		for _, item := range items {
			if _, ok := manifestNames[item.Name]; !ok {
				failures = append(failures, matrixFailure{Name: item.Name, Category: "manifest_entry_missing"})
			}
		}
	}
	passed := len(items) - len(failures)
	if passed < 0 {
		passed = 0
	}
	manifestDigest := sha256.Sum256(manifest)
	status := "VERIFIED"
	if !metadataOnly || len(failures) > 0 || loadedCount != len(items) {
		status = "BLOCKED"
	}
	matrix := executionMatrix{
		SchemaVersion:  1,
		Status:         status,
		RunID:          runID,
		SourcePin:      pin,
		ManifestSHA256: hex.EncodeToString(manifestDigest[:]),
		CatalogSHA256:  matrixCatalogSHA256(results),
		Execution: matrixSummary{
			Denominator:           len(items),
			Passed:                passed,
			Failures:              failures,
			ProductionLoader:      true,
			MetadataOnlyDiscovery: metadataOnly,
			LazyBodyLoads:         loadedCount,
		},
		Skills: results,
	}
	encoded, err := json.MarshalIndent(matrix, "", "  ")
	if err != nil {
		return executionMatrix{}, fmt.Errorf("encode Skill execution matrix: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(matrixPath), 0o755); err != nil {
		return executionMatrix{}, fmt.Errorf("create matrix directory: %w", err)
	}
	if err := os.WriteFile(matrixPath, append(encoded, '\n'), 0o600); err != nil {
		return executionMatrix{}, fmt.Errorf("write Skill execution matrix: %w", err)
	}
	return matrix, nil
}

func matrixCatalogSHA256(entries []matrixSkill) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("skill-execution-matrix-catalog-v1\x00"))
	writeMatrixUint64(digest, uint64(len(entries)))
	for _, entry := range entries {
		writeMatrixString(digest, entry.Name)
		writeMatrixUint64(digest, uint64(len(entry.Tools)))
		for _, tool := range entry.Tools {
			writeMatrixString(digest, tool)
		}
		if entry.ModelInvocable {
			_, _ = digest.Write([]byte{1})
		} else {
			_, _ = digest.Write([]byte{0})
		}
		if entry.UserInvocable {
			_, _ = digest.Write([]byte{1})
		} else {
			_, _ = digest.Write([]byte{0})
		}
		writeMatrixString(digest, entry.BodySHA256)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func writeMatrixString(digest hash.Hash, value string) {
	data := []byte(value)
	writeMatrixUint64(digest, uint64(len(data)))
	_, _ = digest.Write(data)
}

func writeMatrixUint64(digest hash.Hash, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = digest.Write(encoded[:])
}

func inspectMetadataManifest(data []byte) (bool, map[string]struct{}, error) {
	var payload struct {
		Skills []map[string]any `json:"skills"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return false, nil, fmt.Errorf("decode Skill manifest: %w", err)
	}
	names := make(map[string]struct{}, len(payload.Skills))
	metadataOnly := true
	for _, item := range payload.Skills {
		name, _ := item["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return false, nil, errors.New("Skill manifest contains an empty name")
		}
		if _, exists := item["prompt"]; exists {
			metadataOnly = false
		}
		names[name] = struct{}{}
	}
	return metadataOnly, names, nil
}

func loadSkillForMatrix(manager *skills.Manager, metadata skills.Skill) (skills.Skill, string) {
	var loaded skills.Skill
	var ok bool
	var err error
	if metadata.Invocation.ModelInvocable {
		loaded, ok, err = manager.LoadForModel(metadata.Name)
		if err != nil || !ok {
			return skills.Skill{}, "model_load_failed"
		}
	} else {
		if _, _, blockedErr := manager.LoadForModel(metadata.Name); blockedErr == nil {
			return skills.Skill{}, "model_policy_not_enforced"
		}
	}
	if metadata.Invocation.UserInvocable {
		userLoaded, userOK, userErr := manager.LoadForUser(metadata.Name)
		if userErr != nil || !userOK {
			return skills.Skill{}, "user_load_failed"
		}
		if loaded.Name == "" {
			loaded = userLoaded
		} else if loaded.Prompt != userLoaded.Prompt {
			return skills.Skill{}, "audience_body_mismatch"
		}
	} else {
		if _, _, blockedErr := manager.LoadForUser(metadata.Name); blockedErr == nil {
			return skills.Skill{}, "user_policy_not_enforced"
		}
	}
	if loaded.Name == "" {
		return skills.Skill{}, "not_invocable"
	}
	if strings.TrimSpace(loaded.Prompt) == "" {
		return skills.Skill{}, "empty_body"
	}
	reloaded, reloadOK, reloadErr := manager.Load(metadata.Name)
	if reloadErr != nil || !reloadOK || reloaded.Prompt != loaded.Prompt {
		return skills.Skill{}, "cached_reload_failed"
	}
	return loaded, ""
}

func skillMatrixSourcePin(repoRoot string) (matrixSourcePin, error) {
	rootBytes, err := gitOutput(repoRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return matrixSourcePin{}, err
	}
	root := strings.TrimSpace(string(rootBytes))
	shaBytes, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return matrixSourcePin{}, err
	}
	diff, err := gitOutput(root, "diff", "HEAD", "--no-ext-diff", "--binary")
	if err != nil {
		return matrixSourcePin{}, err
	}
	untrackedRaw, err := gitOutput(root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return matrixSourcePin{}, err
	}
	untracked := make([]string, 0)
	for _, name := range strings.Split(string(untrackedRaw), "\x00") {
		if name != "" {
			untracked = append(untracked, name)
		}
	}
	sort.Strings(untracked)
	digest := sha256.New()
	_, _ = digest.Write(diff)
	for _, relative := range untracked {
		if !filepath.IsLocal(relative) {
			return matrixSourcePin{}, fmt.Errorf("unsafe untracked path %q", relative)
		}
		data, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return matrixSourcePin{}, readErr
		}
		_, _ = digest.Write([]byte(filepath.ToSlash(relative)))
		_, _ = digest.Write([]byte{0})
		_, _ = digest.Write(data)
	}
	return matrixSourcePin{
		GitSHA:         strings.TrimSpace(string(shaBytes)),
		DirtyHash:      hex.EncodeToString(digest.Sum(nil)),
		UntrackedFiles: len(untracked),
	}, nil
}

func gitOutput(root string, args ...string) ([]byte, error) {
	commandArgs := append([]string{"-C", root}, args...)
	command := exec.Command("git", commandArgs...)
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s failed", strings.Join(args, " "))
	}
	return output, nil
}
