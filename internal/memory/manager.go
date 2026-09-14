package memory

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Memory struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Content        string    `json:"content"`
	Tags           []string  `json:"tags,omitempty"`
	Namespace      string    `json:"namespace"`
	Kind           string    `json:"kind"`
	Detail         string    `json:"detail"`
	SourceURI      string    `json:"source_uri,omitempty"`
	SourceChecksum string    `json:"source_checksum,omitempty"`
	SessionID      string    `json:"session_id,omitempty"`
	Checksum       string    `json:"checksum"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Stats struct {
	Count int
	Dir   string
	File  string
}

type RecallOptions struct {
	Namespace string `json:"namespace,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

type RecallEntry struct {
	Memory          Memory `json:"memory"`
	Score           int    `json:"score"`
	EstimatedTokens int    `json:"estimated_tokens"`
}

type RecallStats struct {
	Candidates int `json:"candidates"`
	Returned   int `json:"returned"`
	Dropped    int `json:"dropped"`
	MaxTokens  int `json:"max_tokens"`
	UsedTokens int `json:"used_tokens"`
}

type RecallResult struct {
	Entries []RecallEntry `json:"entries"`
	Stats   RecallStats   `json:"stats"`
}

type MemoryEvent struct {
	Action         string    `json:"action"`
	MemoryID       string    `json:"memory_id"`
	MemoryName     string    `json:"memory_name"`
	Namespace      string    `json:"namespace"`
	Kind           string    `json:"kind"`
	Detail         string    `json:"detail"`
	SessionID      string    `json:"session_id,omitempty"`
	SourceChecksum string    `json:"source_checksum,omitempty"`
	MemoryChecksum string    `json:"memory_checksum"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type AuditSink func(MemoryEvent) error

type Manager struct {
	mu        sync.Mutex
	dir       string
	indexFile string
	items     []Memory
	initErr   error
	auditSink AuditSink
}

type fileSnapshot struct {
	path   string
	exists bool
	mode   os.FileMode
	isDir  bool
	data   []byte
	link   string
}

var (
	memoryCredentialPattern    = regexp.MustCompile(`(?i)(?:\b(?:openai|anthropic|deepseek|azure)?[_-]?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|password|passwd|secret)\b\s*[:=]\s*(?:['"][^'"\r\n]+['"]|[^\s,;]+)|\bauthorization\b\s*[:=]\s*(?:['"][^'"\r\n]+['"]|[^\s,;]+)|\b[a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@|\b(?:bearer\s+|sk-)[A-Za-z0-9][A-Za-z0-9._~+/=-]{5,}|\b(?:AKIA|ASIA)[0-9A-Z]{16}\b|-----BEGIN [^-]+-----)`)
	memorySensitiveNamePattern = regexp.MustCompile(`(?i)^(?:(?:openai|anthropic|deepseek|azure|aws|github|gitlab|db|database|mysql|postgres|redis)[-_])?(?:api[-_]?key|access[-_]?token|refresh[-_]?token|authorization(?:[-_]token)?|private[-_]?key|password|passwd|secret|credential|credentials)(?:$|[-_](?:token|key|value|credential|credentials))$`)
	memoryScopePattern         = regexp.MustCompile(`^[a-z0-9]+(?:[._/-][a-z0-9]+)*$`)
	memoryChecksumPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func NewManager(dir string) *Manager {
	return NewManagerWithAudit(dir, nil)
}

func NewManagerWithAudit(dir string, auditSink AuditSink) *Manager {
	if dir == "" {
		dir = filepath.Join(".agent", "memory")
	}
	manager := &Manager{
		dir:       dir,
		indexFile: filepath.Join(dir, "MEMORY.md"),
		auditSink: auditSink,
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		manager.initErr = err
		return manager
	}
	if err := manager.load(); err != nil {
		manager.initErr = err
		return manager
	}
	if err := manager.writeIndex(); err != nil {
		manager.initErr = err
	}
	return manager
}

// Err reports an initialization failure without changing the historical
// constructor shape. Callers must treat a non-nil error as fail-closed.
func (m *Manager) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.initErr
}

func (m *Manager) Add(content string, tags ...string) (Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.initErr != nil {
		return Memory{}, m.initErr
	}
	content = strings.TrimSpace(content)
	tags = normalizeTags(tags)
	if err := validateMemoryFields("", content, tags); err != nil {
		return Memory{}, err
	}
	now := time.Now().UTC()
	item := Memory{
		ID:        nextID(now),
		Name:      m.uniqueName(content, now),
		Content:   content,
		Tags:      tags,
		CreatedAt: now,
		UpdatedAt: now,
	}
	item, err := normalizeMemory(item)
	if err != nil {
		return Memory{}, err
	}
	item.Checksum = checksumMemory(item)
	if err := m.saveLocked(item); err != nil {
		return Memory{}, err
	}
	return item, nil
}

func (m *Manager) Save(item Memory) (Memory, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.initErr != nil {
		return Memory{}, m.initErr
	}
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
	var err error
	item, err = normalizeMemory(item)
	if err != nil {
		return Memory{}, err
	}
	item.Checksum = checksumMemory(item)

	if err := m.saveLocked(item); err != nil {
		return Memory{}, err
	}
	return item, nil
}

func (m *Manager) Delete(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.initErr != nil {
		return m.initErr
	}
	needle := strings.TrimSpace(name)
	name = slugify(needle)
	for i, item := range m.items {
		if item.Name != name && item.Name != needle && item.ID != needle {
			continue
		}
		path := m.pathFor(item.Name)
		snapshot, err := snapshotFiles(path, m.indexFile)
		if err != nil {
			return err
		}
		oldItems := cloneMemories(m.items)
		nextItems := make([]Memory, 0, len(m.items)-1)
		nextItems = append(nextItems, m.items[:i]...)
		nextItems = append(nextItems, m.items[i+1:]...)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		m.items = nextItems
		if err := m.writeIndex(); err != nil {
			return m.rollback(oldItems, snapshot, err)
		}
		if err := m.emitAudit("delete", item); err != nil {
			return m.rollback(oldItems, snapshot, err)
		}
		return nil
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
	result, err := m.Recall(query, RecallOptions{})
	if err != nil {
		return nil
	}
	items := make([]Memory, 0, len(result.Entries))
	for _, entry := range result.Entries {
		items = append(items, entry.Memory)
	}
	return items
}

func (m *Manager) Recall(query string, options RecallOptions) (RecallResult, error) {
	options.Namespace = strings.ToLower(strings.TrimSpace(options.Namespace))
	options.Kind = strings.ToLower(strings.TrimSpace(options.Kind))
	options.Detail = strings.ToLower(strings.TrimSpace(options.Detail))
	if options.Limit < 0 {
		return RecallResult{}, errors.New("memory recall limit must not be negative")
	}
	if options.MaxTokens < 0 {
		return RecallResult{}, errors.New("memory recall token budget must not be negative")
	}
	if options.Namespace != "" && !memoryScopePattern.MatchString(options.Namespace) {
		return RecallResult{}, errors.New("invalid memory recall namespace")
	}
	if options.Kind != "" && !memoryScopePattern.MatchString(options.Kind) {
		return RecallResult{}, errors.New("invalid memory recall kind")
	}
	if options.Detail != "" && options.Detail != "abstract" && options.Detail != "overview" && options.Detail != "full" {
		return RecallResult{}, errors.New("invalid memory recall detail")
	}

	m.mu.Lock()
	items := make([]Memory, len(m.items))
	for i, item := range m.items {
		items[i] = cloneMemory(item)
	}
	m.mu.Unlock()

	tokens := queryTokens(query)
	type scoredMemory struct {
		memory Memory
		score  int
	}
	candidates := make([]scoredMemory, 0, len(items))
	for _, item := range items {
		if options.Namespace != "" && item.Namespace != options.Namespace {
			continue
		}
		if options.Kind != "" && item.Kind != options.Kind {
			continue
		}
		if options.Detail != "" && item.Detail != options.Detail {
			continue
		}
		score := scoreMemory(item, query, tokens)
		if len(tokens) > 0 && score == 0 {
			continue
		}
		candidates = append(candidates, scoredMemory{memory: item, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		if !candidates[i].memory.UpdatedAt.Equal(candidates[j].memory.UpdatedAt) {
			return candidates[i].memory.UpdatedAt.After(candidates[j].memory.UpdatedAt)
		}
		return candidates[i].memory.Name < candidates[j].memory.Name
	})

	result := RecallResult{
		Entries: make([]RecallEntry, 0, len(candidates)),
		Stats: RecallStats{
			Candidates: len(candidates),
			MaxTokens:  options.MaxTokens,
		},
	}
	for _, candidate := range candidates {
		if options.Limit > 0 && len(result.Entries) >= options.Limit {
			break
		}
		estimated := estimateMemoryTokens(candidate.memory)
		if options.MaxTokens > 0 && result.Stats.UsedTokens+estimated > options.MaxTokens {
			continue
		}
		result.Entries = append(result.Entries, RecallEntry{
			Memory:          candidate.memory,
			Score:           candidate.score,
			EstimatedTokens: estimated,
		})
		result.Stats.UsedTokens += estimated
	}
	result.Stats.Returned = len(result.Entries)
	result.Stats.Dropped = result.Stats.Candidates - result.Stats.Returned
	return result, nil
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
		item, err = normalizeMemory(item)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	sortMemories(items)
	m.items = items
	return nil
}

func (m *Manager) saveLocked(item Memory) error {
	var err error
	item, err = normalizeMemory(item)
	if err != nil {
		return err
	}
	item.Checksum = checksumMemory(item)
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	stalePaths := make([]string, 0)
	for _, existing := range m.items {
		if existing.ID == item.ID && existing.Name != item.Name {
			stalePaths = append(stalePaths, m.pathFor(existing.Name))
		}
	}
	targetPath := m.pathFor(item.Name)
	paths := append([]string{targetPath, m.indexFile}, stalePaths...)
	snapshot, err := snapshotFiles(paths...)
	if err != nil {
		return err
	}
	oldItems := cloneMemories(m.items)
	if err := writeMemoryFile(targetPath, item); err != nil {
		return m.rollback(oldItems, snapshot, err)
	}
	for _, path := range stalePaths {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return m.rollback(oldItems, snapshot, err)
		}
	}
	filtered := make([]Memory, 0, len(m.items))
	for _, existing := range m.items {
		if existing.Name == item.Name || existing.ID == item.ID {
			continue
		}
		filtered = append(filtered, existing)
	}
	m.items = append(filtered, item)
	sortMemories(m.items)
	if err := m.writeIndex(); err != nil {
		return m.rollback(oldItems, snapshot, err)
	}
	if err := m.emitAudit("save", item); err != nil {
		return m.rollback(oldItems, snapshot, err)
	}
	return nil
}

func (m *Manager) rollback(oldItems []Memory, snapshot map[string]fileSnapshot, cause error) error {
	m.items = oldItems
	if err := restoreFiles(snapshot); err != nil {
		return errors.Join(cause, fmt.Errorf("restore memory transaction: %w", err))
	}
	return cause
}

func (m *Manager) emitAudit(action string, item Memory) error {
	if m.auditSink == nil {
		return nil
	}
	event := MemoryEvent{
		Action:         action,
		MemoryID:       item.ID,
		MemoryName:     item.Name,
		Namespace:      item.Namespace,
		Kind:           item.Kind,
		Detail:         item.Detail,
		SessionID:      item.SessionID,
		SourceChecksum: item.SourceChecksum,
		MemoryChecksum: item.Checksum,
		OccurredAt:     time.Now().UTC(),
	}
	if err := m.auditSink(event); err != nil {
		return fmt.Errorf("audit memory %s: %w", action, err)
	}
	return nil
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
				formatCanonicalTime(item.UpdatedAt),
				summary(item.Content, 96),
			))
		}
	}
	return atomicWriteFile(m.indexFile, []byte(builder.String()), 0o644)
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

	createdAt, _ := time.Parse(time.RFC3339Nano, meta["created_at"])
	updatedAt, _ := time.Parse(time.RFC3339Nano, meta["updated_at"])
	item := Memory{
		ID:             meta["id"],
		Name:           meta["name"],
		Content:        strings.TrimSpace(strings.Join(bodyLines, "\n")),
		Tags:           splitTags(meta["tags"]),
		Namespace:      meta["namespace"],
		Kind:           meta["kind"],
		Detail:         meta["detail"],
		SourceURI:      meta["source_uri"],
		SourceChecksum: meta["source_checksum"],
		SessionID:      meta["session_id"],
		Checksum:       meta["checksum"],
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
	}
	item, err = normalizeMemory(item)
	if err != nil {
		return Memory{}, err
	}
	expected := checksumMemory(item)
	if item.Checksum != "" && item.Checksum != expected {
		return Memory{}, fmt.Errorf("memory checksum mismatch: %s", path)
	}
	item.Checksum = expected
	return item, nil
}

func writeMemoryFile(path string, item Memory) error {
	var builder strings.Builder
	builder.WriteString("---\n")
	builder.WriteString("id: " + item.ID + "\n")
	builder.WriteString("name: " + item.Name + "\n")
	builder.WriteString("tags: " + strings.Join(item.Tags, ", ") + "\n")
	builder.WriteString("namespace: " + item.Namespace + "\n")
	builder.WriteString("kind: " + item.Kind + "\n")
	builder.WriteString("detail: " + item.Detail + "\n")
	if item.SourceURI != "" {
		builder.WriteString("source_uri: " + item.SourceURI + "\n")
		builder.WriteString("source_checksum: " + item.SourceChecksum + "\n")
	}
	if item.SessionID != "" {
		builder.WriteString("session_id: " + item.SessionID + "\n")
	}
	builder.WriteString("checksum: " + item.Checksum + "\n")
	builder.WriteString("created_at: " + formatCanonicalTime(item.CreatedAt) + "\n")
	builder.WriteString("updated_at: " + formatCanonicalTime(item.UpdatedAt) + "\n")
	builder.WriteString("---\n")
	builder.WriteString(strings.TrimSpace(item.Content))
	builder.WriteString("\n")
	return atomicWriteFile(path, []byte(builder.String()), 0o644)
}

// atomicWriteFile writes a file through a same-directory temporary and then
// replaces the destination. The backup dance keeps replacement portable on
// Windows, where rename does not overwrite an existing file.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return replaceFile(tmpName, path)
}

func replaceFile(tmpPath, targetPath string) error {
	targetInfo, err := os.Lstat(targetPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(tmpPath, targetPath); err != nil {
			return err
		}
		return nil
	}
	if err != nil {
		return err
	}
	if targetInfo.IsDir() {
		return fmt.Errorf("cannot replace directory: %s", targetPath)
	}

	backup, err := os.CreateTemp(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".backup-")
	if err != nil {
		return err
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		_ = os.Remove(backupPath)
		return err
	}
	if err := os.Remove(backupPath); err != nil {
		return err
	}
	if err := os.Rename(targetPath, backupPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		restoreErr := os.Rename(backupPath, targetPath)
		return errors.Join(err, restoreErr)
	}
	if err := os.Remove(backupPath); err != nil {
		// Keep the operation atomic even if cleanup is temporarily blocked.
		removeErr := os.Remove(targetPath)
		restoreErr := os.Rename(backupPath, targetPath)
		return errors.Join(err, removeErr, restoreErr)
	}
	return nil
}

func snapshotFiles(paths ...string) (map[string]fileSnapshot, error) {
	snapshot := make(map[string]fileSnapshot, len(paths))
	for _, path := range paths {
		if _, ok := snapshot[path]; ok {
			continue
		}
		entry, err := snapshotFile(path)
		if err != nil {
			return nil, err
		}
		snapshot[path] = entry
	}
	return snapshot, nil
}

func snapshotFile(path string) (fileSnapshot, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileSnapshot{path: path}, nil
	}
	if err != nil {
		return fileSnapshot{}, err
	}
	snapshot := fileSnapshot{
		path:   path,
		exists: true,
		mode:   info.Mode(),
		isDir:  info.IsDir(),
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(path)
		if err != nil {
			return fileSnapshot{}, err
		}
		snapshot.link = link
		return snapshot, nil
	}
	if info.IsDir() {
		return snapshot, nil
	}
	snapshot.data, err = os.ReadFile(path)
	if err != nil {
		return fileSnapshot{}, err
	}
	return snapshot, nil
}

func restoreFiles(snapshot map[string]fileSnapshot) error {
	var restoreErr error
	for path, entry := range snapshot {
		if err := restoreFile(entry); err != nil {
			restoreErr = errors.Join(restoreErr, fmt.Errorf("%s: %w", path, err))
		}
	}
	return restoreErr
}

func restoreFile(snapshot fileSnapshot) error {
	current, err := os.Lstat(snapshot.path)
	if errors.Is(err, os.ErrNotExist) {
		current = nil
		err = nil
	}
	if err != nil {
		return err
	}
	if !snapshot.exists {
		if current == nil {
			return nil
		}
		if current.IsDir() {
			// Never recursively delete an unexpected directory while rolling back.
			return os.Remove(snapshot.path)
		}
		return os.Remove(snapshot.path)
	}

	if snapshot.isDir {
		if current != nil && !current.IsDir() {
			if err := os.Remove(snapshot.path); err != nil {
				return err
			}
			current = nil
		}
		if current == nil {
			return os.Mkdir(snapshot.path, snapshot.mode.Perm())
		}
		return os.Chmod(snapshot.path, snapshot.mode.Perm())
	}

	if current != nil {
		if current.IsDir() {
			return fmt.Errorf("cannot replace directory during restore")
		}
		if err := os.Remove(snapshot.path); err != nil {
			return err
		}
	}
	if snapshot.mode&os.ModeSymlink != 0 {
		return os.Symlink(snapshot.link, snapshot.path)
	}
	if err := os.WriteFile(snapshot.path, snapshot.data, snapshot.mode.Perm()); err != nil {
		return err
	}
	return os.Chmod(snapshot.path, snapshot.mode.Perm())
}

func sortMemories(items []Memory) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].Name < items[j].Name
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
}

func scoreMemory(item Memory, query string, tokens []string) int {
	phrase := strings.ToLower(strings.TrimSpace(query))
	name := strings.NewReplacer("-", " ", "_", " ", ".", " ", "/", " ").Replace(strings.ToLower(item.Name))
	tags := strings.ToLower(strings.Join(item.Tags, " "))
	metadata := strings.ToLower(strings.Join([]string{item.Namespace, item.Kind, item.Detail, item.SourceURI, item.SessionID}, " "))
	content := strings.ToLower(item.Content)
	score := 0
	if phrase != "" {
		for _, field := range []struct {
			value  string
			weight int
		}{{name, 24}, {tags, 16}, {metadata, 8}, {content, 4}} {
			if strings.Contains(field.value, phrase) {
				score += field.weight
			}
		}
	}
	for _, token := range tokens {
		for _, field := range []struct {
			value  string
			weight int
		}{{name, 6}, {tags, 4}, {metadata, 2}, {content, 1}} {
			if strings.Contains(field.value, token) {
				score += field.weight
			}
		}
	}
	return score
}

func estimateMemoryTokens(item Memory) int {
	text := strings.Join([]string{
		item.Name,
		item.Content,
		strings.Join(item.Tags, " "),
		item.Namespace,
		item.Kind,
		item.Detail,
		item.SourceURI,
	}, "\n")
	tokens := (len([]byte(text)) + 3) / 4
	if tokens < 1 {
		return 1
	}
	return tokens
}

func cloneMemory(item Memory) Memory {
	out := item
	out.Tags = append([]string(nil), item.Tags...)
	return out
}

func cloneMemories(items []Memory) []Memory {
	out := make([]Memory, len(items))
	for i, item := range items {
		out[i] = cloneMemory(item)
	}
	return out
}

func normalizeMemory(item Memory) (Memory, error) {
	item.Content = strings.TrimSpace(item.Content)
	item.Tags = normalizeTags(item.Tags)
	item.Namespace = normalizeMemoryScope(item.Namespace, "user")
	item.Kind = normalizeMemoryScope(item.Kind, "default")
	item.Detail = strings.ToLower(strings.TrimSpace(item.Detail))
	if item.Detail == "" {
		item.Detail = "full"
	}
	item.SourceURI = strings.TrimSpace(item.SourceURI)
	item.SourceChecksum = strings.ToLower(strings.TrimSpace(item.SourceChecksum))
	item.SessionID = strings.TrimSpace(item.SessionID)
	item.Checksum = strings.ToLower(strings.TrimSpace(item.Checksum))
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}
	item.CreatedAt = canonicalTime(item.CreatedAt)
	item.UpdatedAt = canonicalTime(item.UpdatedAt)
	if err := validateMemoryFields(item.Name, item.Content, item.Tags); err != nil {
		return Memory{}, err
	}
	if !memoryScopePattern.MatchString(item.Namespace) || !memoryScopePattern.MatchString(item.Kind) {
		return Memory{}, errors.New("invalid memory namespace or kind")
	}
	if item.Detail != "abstract" && item.Detail != "overview" && item.Detail != "full" {
		return Memory{}, errors.New("invalid memory detail")
	}
	if (item.SourceURI == "") != (item.SourceChecksum == "") {
		return Memory{}, errors.New("memory source URI and checksum must be provided together")
	}
	if item.SourceChecksum != "" && !memoryChecksumPattern.MatchString(item.SourceChecksum) {
		return Memory{}, errors.New("invalid memory source checksum")
	}
	for _, value := range []string{item.Namespace, item.Kind, item.SourceURI, item.SessionID} {
		if strings.ContainsAny(value, "\r\n\t") || memoryCredentialPattern.MatchString(value) {
			return Memory{}, errors.New("sensitive or invalid memory metadata is not allowed")
		}
	}
	if item.Checksum != "" && !memoryChecksumPattern.MatchString(item.Checksum) {
		return Memory{}, errors.New("invalid memory checksum")
	}
	return item, nil
}

func normalizeMemoryScope(value, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return fallback
	}
	return value
}

func checksumMemory(item Memory) string {
	payload := map[string]string{
		"content":         item.Content,
		"created_at":      formatCanonicalTime(item.CreatedAt),
		"detail":          item.Detail,
		"id":              item.ID,
		"kind":            item.Kind,
		"name":            item.Name,
		"namespace":       item.Namespace,
		"session_id":      item.SessionID,
		"source_checksum": item.SourceChecksum,
		"source_uri":      item.SourceURI,
		"updated_at":      formatCanonicalTime(item.UpdatedAt),
	}
	// Keep tags as an array, including the empty array, to match Python's
	// canonical payload and avoid a nil-vs-empty checksum split.
	payloadWithTags := map[string]any{}
	for key, value := range payload {
		payloadWithTags[key] = value
	}
	payloadWithTags["tags"] = item.Tags
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payloadWithTags); err != nil {
		return ""
	}
	data := bytes.TrimSuffix(encoded.Bytes(), []byte{'\n'})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func canonicalTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func formatCanonicalTime(value time.Time) string {
	value = canonicalTime(value)
	text := value.Format("2006-01-02T15:04:05.000000Z")
	if dot := strings.IndexByte(text, '.'); dot >= 0 {
		base := text[:dot]
		fraction := strings.TrimRight(strings.TrimSuffix(text[dot+1:], "Z"), "0")
		if fraction == "" {
			return base + "Z"
		}
		return base + "." + fraction + "Z"
	}
	return text
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

func validateMemoryFields(name, content string, tags []string) error {
	if memoryCredentialPattern.MatchString(content) {
		return errors.New("sensitive memory content is not allowed")
	}
	if isSensitiveMemoryName(name) {
		return errors.New("sensitive memory name is not allowed")
	}
	for _, tag := range tags {
		if isSensitiveMemoryName(tag) {
			return errors.New("sensitive memory tag is not allowed")
		}
	}
	return nil
}

func isSensitiveMemoryName(value string) bool {
	return memorySensitiveNamePattern.MatchString(strings.ToLower(strings.TrimSpace(value)))
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
	return canonicalTime(now).Format("20060102T150405.000000")
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
