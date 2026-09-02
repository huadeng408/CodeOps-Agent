package skills

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"go.yaml.in/yaml/v3"
)

type Skill struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Prompt      string   `json:"prompt"`
	Tools       []string `json:"tools,omitempty"`
}

type Snapshot struct {
	Count int
}

// DiscoveryOptions describes the three Skill precedence levels. Later levels
// override earlier ones, so a project can replace a global default safely.
type DiscoveryOptions struct {
	GlobalDir   string
	Directories []string
	ProjectDir  string
}

type skillEntry struct {
	skill Skill
	path  string
}

type skillFrontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Tools       []string `yaml:"tools"`
}

type Manager struct {
	mu    sync.Mutex
	items map[string]skillEntry
}

func NewManager() *Manager {
	manager := &Manager{items: make(map[string]skillEntry)}
	for _, skill := range goalSkills() {
		manager.Register(skill)
	}
	return manager
}

func (m *Manager) Register(skill Skill) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.items[skill.Name] = skillEntry{skill: skill}
}

// Discover registers SKILL.md metadata without reading instruction bodies.
// Missing directories are normal because global and project Skill directories
// are both optional.
func (m *Manager) Discover(options DiscoveryOptions) error {
	for _, root := range append([]string{options.GlobalDir}, options.Directories...) {
		if err := m.discoverDirectory(root); err != nil {
			return err
		}
	}
	return m.discoverDirectory(options.ProjectDir)
}

func (m *Manager) discoverDirectory(root string) error {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}

	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read skills directory %s: %w", root, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "SKILL.md")
		metadata, err := readFrontmatter(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("discover skill %s: %w", path, err)
		}
		name := strings.TrimSpace(metadata.Name)
		if name == "" {
			name = entry.Name()
		}
		if strings.ContainsAny(name, "\\/\r\n\t ") {
			return fmt.Errorf("discover skill %s: invalid name %q", path, name)
		}
		description := strings.TrimSpace(metadata.Description)
		if description == "" {
			return fmt.Errorf("discover skill %s: description is required", path)
		}
		m.registerLazy(Skill{
			Name:        name,
			Description: description,
			Tools:       append([]string(nil), metadata.Tools...),
		}, path)
	}
	return nil
}

func (m *Manager) registerLazy(skill Skill, path string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[skill.Name] = skillEntry{skill: skill, path: path}
}

// Load resolves a Skill's instruction body on first use. Metadata remains
// inexpensive to list, while prompts are cached after a successful load.
func (m *Manager) Load(name string) (Skill, bool, error) {
	m.mu.Lock()
	entry, ok := m.items[name]
	m.mu.Unlock()
	if !ok {
		return Skill{}, false, nil
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

// Get preserves the original lookup API for callers that cannot surface a
// loading error. New command and tool paths should use Load instead.
func (m *Manager) Get(name string) (Skill, bool) {
	skill, ok, err := m.Load(name)
	return skill, ok && err == nil
}

func (m *Manager) List() []Skill {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Skill, 0, len(m.items))
	for _, entry := range m.items {
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
	return Snapshot{Count: len(m.items)}
}

// WriteManifest writes model-facing Skill metadata without leaking prompt
// bodies into the project manifest.
func (m *Manager) WriteManifest(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	type manifestSkill struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tools       []string `json:"tools,omitempty"`
	}
	items := m.List()
	payload := struct {
		Skills []manifestSkill `json:"skills"`
	}{Skills: make([]manifestSkill, 0, len(items))}
	for _, skill := range items {
		payload.Skills = append(payload.Skills, manifestSkill{
			Name:        skill.Name,
			Description: skill.Description,
			Tools:       append([]string(nil), skill.Tools...),
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
