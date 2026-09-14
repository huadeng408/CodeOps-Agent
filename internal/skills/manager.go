package skills

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

type Skill struct {
	Name         string           `json:"name"`
	Description  string           `json:"description"`
	Prompt       string           `json:"prompt"`
	Tools        []string         `json:"tools,omitempty"`
	WhenToUse    string           `json:"whenToUse,omitempty"`
	Source       string           `json:"source,omitempty"`
	Provider     string           `json:"provider,omitempty"`
	Path         string           `json:"path,omitempty"`
	ResourceBase string           `json:"resourceBase,omitempty"`
	Invocation   InvocationPolicy `json:"invocation,omitempty"`
}

type Snapshot struct {
	Count    int
	Complete bool
	Revision uint64
}

// InvocationPolicy mirrors the model/user invocation controls used by
// DeepSeek-style SKILL.md frontmatter. Both are enabled by default for
// built-in and legacy skills.
type InvocationPolicy struct {
	ModelInvocable bool `json:"modelInvocable"`
	UserInvocable  bool `json:"userInvocable"`
	Configured     bool `json:"-"`
}

// DiscoveryOptions describes the three Skill precedence levels. Later levels
// override earlier ones, so a project can replace a global default safely.
type DiscoveryOptions struct {
	GlobalDir        string
	Directories      []string
	ProjectDir       string
	ProjectDSHDir    string
	ProjectAgentsDir string
	UserDSHDir       string
	UserAgentsDir    string
	BundledDir       string
}

type skillEntry struct {
	skill Skill
	path  string
	rank  int
}

type discoveredSkill struct {
	skill Skill
	path  string
	rank  int
	order int
}

type discoveryRoot struct {
	path   string
	source string
	rank   int
}

type skillFrontmatter struct {
	Name                   string   `yaml:"name"`
	Description            string   `yaml:"description"`
	Tools                  []string `yaml:"tools"`
	WhenToUse              string   `yaml:"whenToUse"`
	DisableModelInvocation *bool    `yaml:"disable-model-invocation"`
	UserInvocable          *bool    `yaml:"user-invocable"`
}

type Manager struct {
	mu              sync.Mutex
	items           map[string]skillEntry
	builtins        map[string]skillEntry
	discoveredNames map[string]struct{}
	complete        bool
	revision        uint64
}

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func validSkillName(name string) bool {
	return skillNamePattern.MatchString(name)
}

func validSkillMetadata(skill Skill) bool {
	if !validSkillName(skill.Name) || strings.TrimSpace(skill.Description) == "" {
		return false
	}
	for _, tool := range skill.Tools {
		if strings.TrimSpace(tool) == "" || strings.ContainsAny(tool, "\r\n\t") {
			return false
		}
	}
	return true
}

func NewManager() *Manager {
	manager := &Manager{
		items:           make(map[string]skillEntry),
		builtins:        make(map[string]skillEntry),
		discoveredNames: make(map[string]struct{}),
		complete:        true,
		revision:        1,
	}
	for _, skill := range goalSkills() {
		manager.registerBuiltin(skill)
	}
	return manager
}

func (m *Manager) registerBuiltin(skill Skill) {
	if !validSkillMetadata(skill) {
		return
	}
	if !skill.Invocation.Configured && !skill.Invocation.ModelInvocable && !skill.Invocation.UserInvocable {
		skill.Invocation = InvocationPolicy{ModelInvocable: true, UserInvocable: true}
	}
	entry := skillEntry{skill: skill}
	m.items[skill.Name] = entry
	m.builtins[skill.Name] = entry
}

func (m *Manager) Register(skill Skill) {
	if !validSkillMetadata(skill) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if !skill.Invocation.Configured && !skill.Invocation.ModelInvocable && !skill.Invocation.UserInvocable {
		skill.Invocation = InvocationPolicy{ModelInvocable: true, UserInvocable: true}
	}
	m.items[skill.Name] = skillEntry{skill: skill}
}

// Discover registers SKILL.md metadata without reading instruction bodies.
// Missing directories are normal because global and project Skill directories
// are both optional.
func (m *Manager) Discover(options DiscoveryOptions) error {
	// DeepSeek Harness uses lower numeric ranks for higher-precedence roots.
	// Collect every candidate before mutating the catalog so one malformed root
	// cannot partially replace the last-good observation.
	roots := []discoveryRoot{
		{path: options.ProjectDSHDir, source: "project-dsh", rank: 100},
		{path: options.ProjectAgentsDir, source: "project-agents", rank: 200},
		{path: options.ProjectDir, source: "project", rank: 250},
	}
	for _, path := range options.Directories {
		roots = append(roots, discoveryRoot{path: path, source: "custom", rank: 300})
	}
	roots = append(roots,
		discoveryRoot{path: options.UserDSHDir, source: "user-dsh", rank: 400},
		discoveryRoot{path: options.UserAgentsDir, source: "user-agents", rank: 500},
		discoveryRoot{path: options.GlobalDir, source: "global", rank: 550},
		discoveryRoot{path: options.BundledDir, source: "bundled", rank: 600},
	)
	winners := make(map[string]discoveredSkill)
	order := 0
	for _, root := range roots {
		items, err := m.discoverDirectory(root)
		if err != nil {
			m.SetDiscoveryStatus(false)
			return err
		}
		for _, item := range items {
			item.order = order
			order++
			current, exists := winners[item.skill.Name]
			if !exists || item.rank < current.rank || (item.rank == current.rank && item.order < current.order) {
				winners[item.skill.Name] = item
			}
		}
	}

	m.mu.Lock()
	for name := range m.discoveredNames {
		if builtin, ok := m.builtins[name]; ok {
			m.items[name] = builtin
		} else {
			delete(m.items, name)
		}
	}
	m.discoveredNames = make(map[string]struct{}, len(winners))
	for name, item := range winners {
		m.items[name] = skillEntry{skill: item.skill, path: item.path, rank: item.rank}
		m.discoveredNames[name] = struct{}{}
	}
	m.complete = true
	m.revision++
	m.mu.Unlock()
	return nil
}

func (m *Manager) discoverDirectory(root discoveryRoot) ([]discoveredSkill, error) {
	root.path = strings.TrimSpace(root.path)
	if root.path == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(root.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read skills directory %s: %w", root.path, err)
	}
	discovered := make([]discoveredSkill, 0, len(entries))
	for _, entry := range entries {
		if root.source == "user-dsh" && entry.IsDir() && entry.Name() == ".system" {
			continue
		}
		path := ""
		if entry.IsDir() {
			path = filepath.Join(root.path, entry.Name(), "SKILL.md")
		} else if strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			// DeepSeek also accepts a flat skill file directly under a catalog.
			path = filepath.Join(root.path, entry.Name())
		} else {
			continue
		}
		metadata, err := readFrontmatter(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("discover skill %s: %w", path, err)
		}
		name := strings.TrimSpace(metadata.Name)
		if name == "" {
			name = strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		}
		if !validSkillName(name) {
			return nil, fmt.Errorf("discover skill %s: invalid name %q", path, name)
		}
		description := strings.TrimSpace(metadata.Description)
		if description == "" {
			return nil, fmt.Errorf("discover skill %s: description is required", path)
		}
		candidate := Skill{
			Name:         name,
			Description:  description,
			Tools:        normalizeTools(metadata.Tools),
			WhenToUse:    strings.TrimSpace(metadata.WhenToUse),
			Source:       root.source,
			Provider:     "filesystem",
			Path:         path,
			ResourceBase: filepath.Dir(path),
			Invocation:   InvocationPolicy{ModelInvocable: true, UserInvocable: true},
		}
		if metadata.DisableModelInvocation != nil {
			candidate.Invocation.ModelInvocable = !*metadata.DisableModelInvocation
			candidate.Invocation.Configured = true
		}
		if metadata.UserInvocable != nil {
			candidate.Invocation.UserInvocable = *metadata.UserInvocable
			candidate.Invocation.Configured = true
		}
		if !validSkillMetadata(candidate) || len(candidate.Tools) != len(metadata.Tools) {
			return nil, fmt.Errorf("discover skill %s: invalid tool metadata", path)
		}
		discovered = append(discovered, discoveredSkill{skill: candidate, path: path, rank: root.rank})
	}
	return discovered, nil
}

func (m *Manager) registerLazy(skill Skill, path string) {
	if !validSkillMetadata(skill) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[skill.Name] = skillEntry{skill: skill, path: path}
}

// Load resolves a Skill's instruction body on first use. Metadata remains
// inexpensive to list, while prompts are cached after a successful load.
func (m *Manager) Load(name string) (Skill, bool, error) {
	return m.loadFiltered(name, nil, "")
}

func (m *Manager) LoadForModel(name string) (Skill, bool, error) {
	return m.loadFiltered(name, func(policy InvocationPolicy) bool { return policy.ModelInvocable }, "model")
}

func (m *Manager) LoadForUser(name string) (Skill, bool, error) {
	return m.loadFiltered(name, func(policy InvocationPolicy) bool { return policy.UserInvocable }, "user")
}

func (m *Manager) loadFiltered(name string, include func(InvocationPolicy) bool, audience string) (Skill, bool, error) {
	m.mu.Lock()
	entry, ok := m.items[name]
	m.mu.Unlock()
	if !ok {
		return Skill{}, false, nil
	}
	if include != nil && !include(entry.skill.Invocation) {
		return Skill{}, false, fmt.Errorf("skill %q is not %s-invocable", name, audience)
	}
	if entry.path == "" {
		return entry.skill, true, nil
	}

	prompt, err := readPrompt(entry.path)
	if err != nil {
		return Skill{}, true, fmt.Errorf("load skill %q: %w", name, err)
	}
	entry.skill.Prompt = prompt
	entry.path = ""

	m.mu.Lock()
	current, stillRegistered := m.items[name]
	if stillRegistered && current.path != "" {
		m.items[name] = entry
		current = entry
	}
	m.mu.Unlock()
	return current.skill, true, nil
}

// ReadResource reads a file shipped beside a filesystem Skill. Resource paths
// are confined to the Skill directory, preventing traversal into the host.
func (m *Manager) ReadResource(name, resource string) ([]byte, error) {
	skill, ok, err := m.Load(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("skill %q not found", name)
	}
	base := strings.TrimSpace(skill.ResourceBase)
	if base == "" {
		return nil, fmt.Errorf("skill %q has no resource directory", name)
	}
	basePath, err := filepath.Abs(base)
	if err != nil {
		return nil, fmt.Errorf("resolve skill resource directory: %w", err)
	}
	clean := filepath.Clean(resource)
	if clean == "." || filepath.IsAbs(resource) {
		return nil, errors.New("invalid skill resource path")
	}
	target, err := filepath.Abs(filepath.Join(basePath, clean))
	if err != nil {
		return nil, fmt.Errorf("resolve skill resource: %w", err)
	}
	rel, err := filepath.Rel(basePath, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errors.New("skill resource path escapes skill directory")
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, errors.New("skill resource is a directory")
	}
	return os.ReadFile(target)
}

// Get preserves the original lookup API for callers that cannot surface a
// loading error. New command and tool paths should use Load instead.
func (m *Manager) Get(name string) (Skill, bool) {
	skill, ok, err := m.Load(name)
	return skill, ok && err == nil
}

func (m *Manager) List() []Skill {
	return m.listFiltered(nil)
}

// ListForModel returns only Skills that may be surfaced to model prompts.
// Discovery remains metadata-only; callers must use Load when they need a body.
func (m *Manager) ListForModel() []Skill {
	return m.listFiltered(func(policy InvocationPolicy) bool { return policy.ModelInvocable })
}

// ListForUser returns only Skills that may be selected from user-facing entry points.
func (m *Manager) ListForUser() []Skill {
	return m.listFiltered(func(policy InvocationPolicy) bool { return policy.UserInvocable })
}

func (m *Manager) listFiltered(include func(InvocationPolicy) bool) []Skill {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Skill, 0, len(m.items))
	for _, entry := range m.items {
		if include != nil && !include(entry.skill.Invocation) {
			continue
		}
		out = append(out, entry.skill)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func (m *Manager) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Snapshot{Count: len(m.items), Complete: m.complete, Revision: m.revision}
}

// SetDiscoveryStatus lets filesystem watchers report an incomplete refresh
// while retaining the last good catalog and revision.
func (m *Manager) SetDiscoveryStatus(complete bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.complete != complete {
		m.complete = complete
		if complete {
			m.revision++
		}
	}
}

// WriteManifest writes model-facing Skill metadata without leaking prompt
// bodies into the project manifest.
func (m *Manager) WriteManifest(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	type manifestSkill struct {
		Name         string           `json:"name"`
		Description  string           `json:"description"`
		Tools        []string         `json:"tools,omitempty"`
		WhenToUse    string           `json:"whenToUse,omitempty"`
		Invocation   InvocationPolicy `json:"invocation"`
		Source       string           `json:"source,omitempty"`
		Provider     string           `json:"provider,omitempty"`
		Path         string           `json:"path,omitempty"`
		ResourceBase string           `json:"resourceBase,omitempty"`
	}
	items := m.List()
	payload := struct {
		Skills []manifestSkill `json:"skills"`
	}{Skills: make([]manifestSkill, 0, len(items))}
	for _, skill := range items {
		if !validSkillMetadata(skill) {
			return errors.New("invalid Skill metadata")
		}
		payload.Skills = append(payload.Skills, manifestSkill{
			Name:         skill.Name,
			Description:  skill.Description,
			Tools:        append([]string(nil), skill.Tools...),
			WhenToUse:    skill.WhenToUse,
			Invocation:   skill.Invocation,
			Source:       skill.Source,
			Provider:     skill.Provider,
			Path:         skill.Path,
			ResourceBase: skill.ResourceBase,
		})
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encode skills manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create skills manifest directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write skills manifest: %w", err)
	}
	return nil
}

func normalizeTools(tools []string) []string {
	out := make([]string, len(tools))
	for i, tool := range tools {
		out[i] = strings.TrimSpace(tool)
	}
	return out
}

func readFrontmatter(path string) (skillFrontmatter, error) {
	file, err := os.Open(path)
	if err != nil {
		return skillFrontmatter{}, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	first, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return skillFrontmatter{}, fmt.Errorf("read frontmatter: %w", err)
	}
	if strings.TrimSpace(strings.TrimPrefix(first, "\ufeff")) != "---" {
		return skillFrontmatter{}, errors.New("missing YAML frontmatter")
	}

	var lines []string
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return skillFrontmatter{}, fmt.Errorf("read frontmatter: %w", readErr)
		}
		if strings.TrimSpace(line) == "---" {
			break
		}
		lines = append(lines, line)
		if errors.Is(readErr, io.EOF) {
			return skillFrontmatter{}, errors.New("missing closing YAML frontmatter")
		}
	}

	var metadata skillFrontmatter
	if err := yaml.Unmarshal([]byte(strings.Join(lines, "")), &metadata); err != nil {
		return skillFrontmatter{}, fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	return metadata, nil
}

func readPrompt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	if !strings.HasPrefix(text, "---") {
		return "", errors.New("missing YAML frontmatter")
	}
	parts := strings.SplitN(text, "\n---", 2)
	if len(parts) != 2 {
		return "", errors.New("missing closing YAML frontmatter")
	}
	body := strings.TrimPrefix(parts[1], "\n")
	return strings.TrimSpace(body), nil
}
