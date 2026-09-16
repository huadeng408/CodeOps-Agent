package memory

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"code-agent/internal/session"
)

type CLIStore interface {
	Err() error
	Add(string, ...string) (Memory, error)
	Delete(string) error
	Get(string) (Memory, bool)
	List() []Memory
	LoadRelevant(string) []Memory
	Snapshot() Stats
}

// CLIAdapter preserves the slash-command interface but all new writes use
// LedgerMemory. Legacy Markdown is not read or rewritten at construction.
type CLIAdapter struct {
	module     *LedgerMemory
	current    func() session.Session
	initErr    error
	initialize func() (*LedgerMemory, error)
}

func NewLazyLedgerCLI(initialize func() (*LedgerMemory, error), current func() session.Session) *CLIAdapter {
	return &CLIAdapter{initialize: initialize, current: current}
}

func NewLedgerCLI(module *LedgerMemory, current func() session.Session, initErr error) *CLIAdapter {
	return &CLIAdapter{module: module, current: current, initErr: initErr}
}

func (a *CLIAdapter) Err() error {
	if a.initErr != nil {
		return a.initErr
	}
	if a.current == nil {
		return errors.New("memory ledger unavailable")
	}
	if a.current().ID == "" {
		return nil
	}
	if a.module == nil && a.initialize != nil {
		a.module, a.initErr = a.initialize()
		if a.initErr != nil {
			return a.initErr
		}
	}
	if a.module == nil || a.module.ledger == nil {
		return errors.New("memory ledger unavailable")
	}
	_, err := a.owner()
	return err
}

func (a *CLIAdapter) owner() (uint, error) {
	if a.module == nil {
		return 0, errors.New("memory ledger unavailable")
	}
	state := a.current()
	snapshot, err := session.ReadVerifiedSnapshot(context.Background(), a.module.ledger, state.ID)
	if err != nil {
		return 0, err
	}
	return trajectoryOwner(snapshot.Events)
}

func (a *CLIAdapter) Add(content string, tags ...string) (Memory, error) {
	if err := a.Err(); err != nil {
		return Memory{}, err
	}
	if err := validateMemoryFields("", content, normalizeTags(tags)); err != nil {
		return Memory{}, err
	}
	key := "note-" + trajectoryDigest(content)[:16]
	owner, err := a.owner()
	if err != nil {
		return Memory{}, err
	}
	_, entries, _, err := a.module.catalog(context.Background(), owner)
	if err != nil {
		return Memory{}, err
	}
	id := experienceID("preferences", key)
	_, err = a.module.Manage(context.Background(), a.current().ID, session.MemoryCommand{Action: "remember", Kind: "preferences", Key: key, Content: content, Tags: normalizeTags(tags), ExpectedRevision: entries[id].Revision})
	if err != nil {
		return Memory{}, err
	}
	item, ok := a.Get(id)
	if !ok {
		return Memory{}, errors.New("memory projection unavailable")
	}
	return item, nil
}

func (a *CLIAdapter) List() []Memory {
	if a.Err() != nil {
		return nil
	}
	owner, err := a.owner()
	if err != nil {
		return nil
	}
	items, err := a.module.experienceItems(context.Background(), owner, "full", "")
	if err != nil {
		return nil
	}
	return items
}

func (a *CLIAdapter) Get(name string) (Memory, bool) {
	for _, item := range a.List() {
		if item.Name == name || item.ID == name {
			return item, true
		}
	}
	return Memory{}, false
}

func (a *CLIAdapter) Delete(name string) error {
	item, ok := a.Get(name)
	if !ok {
		return errors.New("memory not found")
	}
	owner, err := a.owner()
	if err != nil {
		return err
	}
	_, entries, _, err := a.module.catalog(context.Background(), owner)
	if err != nil {
		return err
	}
	_, err = a.module.Manage(context.Background(), a.current().ID, session.MemoryCommand{Action: "forget", ID: item.ID, ExpectedRevision: entries[item.ID].Revision})
	return err
}

func (a *CLIAdapter) LoadRelevant(query string) []Memory {
	owner, err := a.owner()
	if err != nil {
		return nil
	}
	encoded, err := a.module.RecallWithOptions(context.Background(), owner, session.MemoryQuery{Query: query, Detail: "overview", MaxTokens: 1200})
	if err != nil {
		return nil
	}
	var recalled RecallResult
	if json.Unmarshal([]byte(encoded), &recalled) != nil {
		return nil
	}
	items := []Memory{}
	for _, entry := range recalled.Entries {
		items = append(items, entry.Memory)
	}
	return items
}

func (a *CLIAdapter) Snapshot() Stats {
	return Stats{Count: len(a.List()), Dir: "Session Ledger", File: "memory/catalog"}
}

// ImportLegacy is an explicit, one-way import of validated memory records.
// It never writes back to the legacy directory or copies its index.
func (a *CLIAdapter) ImportLegacy(dir string) (int, error) {
	legacy := &Manager{dir: dir}
	if err := legacy.load(); err != nil {
		return 0, err
	}
	count := 0
	for _, item := range legacy.items {
		if strings.TrimSpace(item.Content) == "" {
			continue
		}
		if _, err := a.Add(item.Content, item.Tags...); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}
