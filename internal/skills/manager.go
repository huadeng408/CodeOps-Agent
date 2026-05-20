package skills

import (
	"sort"
	"sync"
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

type Manager struct {
	mu     sync.Mutex
	items  map[string]Skill
}

func NewManager() *Manager {
	manager := &Manager{items: make(map[string]Skill)}
	for _, skill := range []Skill{
		initSkill(),
		reviewSkill(),
		securitySkill(),
	} {
		manager.Register(skill)
	}
	return manager
}

func (m *Manager) Register(skill Skill) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.items[skill.Name] = skill
}

func (m *Manager) Get(name string) (Skill, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	skill, ok := m.items[name]
	return skill, ok
}

func (m *Manager) List() []Skill {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Skill, 0, len(m.items))
	for _, skill := range m.items {
		out = append(out, skill)
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
