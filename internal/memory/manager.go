package memory

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Memory struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Stats struct {
	Count int
	Dir   string
	File  string
}

type Manager struct {
	mu        sync.Mutex
	dir       string
	indexFile string
	items     []Memory
}

func NewManager(dir string) *Manager {
	if dir == "" {
		dir = filepath.Join(".agent", "memory")
	}
	manager := &Manager{
		dir:       dir,
		indexFile: filepath.Join(dir, "MEMORY.md"),
	}
	_ = os.MkdirAll(dir, 0o755)
	_ = manager.load()
	_ = manager.writeIndex()
	return manager
}

func (m *Manager) Add(content string, tags ...string) Memory {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	item := Memory{
		ID:        nextID(now),
		Name:      m.uniqueName(content, now),
		Content:   strings.TrimSpace(content),
		Tags:      normalizeTags(tags),
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = m.saveLocked(item)
	return item
}

func (m *Manager) Save(item Memory) (Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	if item.ID == "" {
		item.ID = nextID(now)
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	item.UpdatedAt = now
	item.Content = strings.TrimSpace(item.Content)
	item.Tags = normalizeTags(item.Tags)
	if strings.TrimSpace(item.Name) == "" {
		item.Name = m.uniqueName(item.Content, now)
	} else {
		item.Name = slugify(item.Name)
		if item.Name == "" {
			item.Name = m.uniqueName(item.Content, now)
		}
	}

	if err := m.saveLocked(item); err != nil {
		return Memory{}, err
	}
	return item, nil
}

func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	needle := strings.TrimSpace(name)
	name = slugify(needle)
	for i, item := range m.items {
		if item.Name != name && item.Name != needle && item.ID != needle {
			continue
		}
		if err := os.Remove(m.pathFor(item.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		m.items = append(m.items[:i], m.items[i+1:]...)
		return m.writeIndex()
	}
	return fmt.Errorf("memory not found: %s", name)
}

func (m *Manager) Get(name string) (Memory, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	needle := strings.TrimSpace(name)
	name = slugify(needle)
	for _, item := range m.items {
		if item.Name == name || item.Name == needle || item.ID == needle {
			return cloneMemory(item), true
		}
	}
	return Memory{}, false
}

func (m *Manager) List() []Memory {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Memory, len(m.items))
	for i, item := range m.items {
		out[i] = cloneMemory(item)
	}
	return out
}

func (m *Manager) LoadRelevant(query string) []Memory {
	m.mu.Lock()
	defer m.mu.Unlock()

	tokens := queryTokens(query)
	if len(tokens) == 0 {
		out := make([]Memory, len(m.items))
		for i, item := range m.items {
			out[i] = cloneMemory(item)
		}
		return out
	}

	results := make([]Memory, 0)
	for _, item := range m.items {
		if memoryMatches(item, tokens) {
			results = append(results, cloneMemory(item))
		}
	}
	return results
}

func (m *Manager) Snapshot() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()

	return Stats{
		Count: len(m.items),
		Dir:   m.dir,
		File:  m.indexFile,
	}
}

func (m *Manager) load() error {
	pattern := filepath.Join(m.dir, "*.md")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	items := make([]Memory, 0, len(paths))
	for _, path := range paths {
		if filepath.Base(path) == "MEMORY.md" {
			continue
		}
		item, err := readMemoryFile(path)
		if err != nil {
			return err
		}
		if item.Name == "" {
			item.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		items = append(items, item)
	}
	sortMemories(items)
	m.items = items
	return nil
}

func (m *Manager) saveLocked(item Memory) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	if err := writeMemoryFile(m.pathFor(item.Name), item); err != nil {
		return err
	}

	replaced := false
	for i, existing := range m.items {
		if existing.Name == item.Name || existing.ID == item.ID {
			m.items[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		m.items = append(m.items, item)
	}
	sortMemories(m.items)
	return m.writeIndex()
}

func (m *Manager) writeIndex() error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}

	var builder strings.Builder
	builder.WriteString("# Memory Index\n\n")
	if len(m.items) == 0 {
		builder.WriteString("_No memories saved._\n")
	} else {
		for _, item := range m.items {
			tags := strings.Join(item.Tags, ", ")
			if tags == "" {
				tags = "none"
			}
			builder.WriteString(fmt.Sprintf(
				"- [%s](%s.md) | tags: %s | updated: %s | summary: %s\n",
				item.Name,
				item.Name,
				tags,
				item.UpdatedAt.Format(time.RFC3339),
				summary(item.Content, 96),
			))
		}
	}
	return os.WriteFile(m.indexFile, []byte(builder.String()), 0o644)
}

func (m *Manager) pathFor(name string) string {
	return filepath.Join(m.dir, name+".md")
}

func (m *Manager) uniqueName(content string, now time.Time) string {
	base := slugify(firstLine(content))
	if base == "" {
		base = "memory"
	}
	if len(base) > 48 {
		base = strings.Trim(base[:48], "-")
	}
	stamp := now.Format("20060102T150405")
	name := base + "-" + stamp
	for suffix := 2; ; suffix++ {
		if _, err := os.Stat(m.pathFor(name)); errors.Is(err, os.ErrNotExist) {
			return name
		}
		name = fmt.Sprintf("%s-%s-%d", base, stamp, suffix)
	}
}

func readMemoryFile(path string) (Memory, error) {
	file, err := os.Open(path)
	if err != nil {
		return Memory{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return Memory{}, fmt.Errorf("memory file missing frontmatter: %s", path)
	}

	meta := map[string]string{}
	bodyLines := []string{}
	inMeta := true
	closed := false
	for scanner.Scan() {
		line := scanner.Text()
		if inMeta {
			if strings.TrimSpace(line) == "---" {
				inMeta = false
				closed = true
				continue
			}
			key, value, ok := strings.Cut(line, ":")
			if ok {
				meta[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
			continue
		}
		bodyLines = append(bodyLines, line)
	}
	if err := scanner.Err(); err != nil {
		return Memory{}, err
	}
	if !closed {
		return Memory{}, fmt.Errorf("memory file missing closing frontmatter: %s", path)
	}

	createdAt, _ := time.Parse(time.RFC3339, meta["created_at"])
	updatedAt, _ := time.Parse(time.RFC3339, meta["updated_at"])
	return Memory{
		ID:        meta["id"],
		Name:      meta["name"],
		Content:   strings.TrimSpace(strings.Join(bodyLines, "\n")),
		Tags:      splitTags(meta["tags"]),
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

func writeMemoryFile(path string, item Memory) error {
	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString("id: " + item.ID + "\n")
	builder.WriteString("name: " + item.Name + "\n")
	builder.WriteString("tags: " + strings.Join(item.Tags, ", ") + "\n")
	builder.WriteString("created_at: " + item.CreatedAt.Format(time.RFC3339) + "\n")
	builder.WriteString("updated_at: " + item.UpdatedAt.Format(time.RFC3339) + "\n")
	builder.WriteString("---\n")
	builder.WriteString(strings.TrimSpace(item.Content))
	builder.WriteString("\n")
	return os.WriteFile(path, []byte(builder.String()), 0o644)
}

func sortMemories(items []Memory) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].Name < items[j].Name
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
}

func memoryMatches(item Memory, tokens []string) bool {
	fields := []string{
		strings.ToLower(item.Name),
		strings.ToLower(item.Content),
		strings.ToLower(strings.Join(item.Tags, " ")),
	}
	for _, token := range tokens {
		for _, field := range fields {
			if strings.Contains(field, token) {
				return true
			}
		}
	}
	return false
}

func cloneMemory(item Memory) Memory {
	out := item
	out.Tags = append([]string(nil), item.Tags...)
	return out
}

func normalizeTags(tags []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.Trim(strings.TrimSpace(tag), "#")
		tag = strings.ToLower(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

func splitTags(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return normalizeTags(strings.Split(value, ","))
}

func queryTokens(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	tokens := make([]string, 0, len(fields))
	for _, field := range fields {
		if len([]rune(field)) < 2 {
			continue
		}
		tokens = append(tokens, field)
	}
	if len(tokens) == 0 {
		return []string{query}
	}
	return tokens
}

func firstLine(content string) string {
	for _, line := range strings.Split(strings.TrimSpace(content), "\n") {
		if strings.TrimSpace(line) != "" {
			return truncateText(strings.TrimSpace(line), 80)
		}
	}
	return ""
}

func summary(content string, max int) string {
	value := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	value = strings.ReplaceAll(value, "|", "/")
	return truncateText(value, max)
}

func slugify(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func nextID(now time.Time) string {
	return now.Format("20060102T150405.000000000")
}

func truncateText(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	if max <= 3 {
		return string(runes[:max])
	}
	return strings.TrimSpace(string(runes[:max-3])) + "..."
}
