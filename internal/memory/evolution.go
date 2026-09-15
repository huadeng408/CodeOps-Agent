package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/identity"
	"code-agent/internal/session"
)

type MemoryReflector func(context.Context, *pb.MemoryReflectionRequest) (*pb.MemoryReflectionResponse, error)

var memoryKey = regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,127}$`)
var experienceKinds = map[string]bool{"profile": true, "preferences": true, "entities": true, "events": true, "cases": true, "patterns": true}

type memoryOrigin struct {
	SessionID      string             `json:"session_id"`
	SourceChecksum string             `json:"source_checksum"`
	Sources        []TrajectorySource `json:"sources"`
	Manual         bool               `json:"manual,omitempty"`
}

type Experience struct {
	ID        string         `json:"id"`
	Kind      string         `json:"kind"`
	Key       string         `json:"key"`
	Tags      []string       `json:"tags,omitempty"`
	Abstract  string         `json:"abstract"`
	Overview  string         `json:"overview"`
	Content   string         `json:"content"`
	Revision  uint64         `json:"revision"`
	Deleted   bool           `json:"deleted,omitempty"`
	ExpiresAt *time.Time     `json:"expires_at,omitempty"`
	Origins   []memoryOrigin `json:"origins"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type catalogMutation struct {
	SchemaVersion int          `json:"schema_version"`
	OwnerID       uint         `json:"owner_id"`
	SourceID      string       `json:"source_id,omitempty"`
	Entries       []Experience `json:"entries"`
}

func catalogID(owner uint) string          { return fmt.Sprintf("memory-user-%d", owner) }
func experienceID(kind, key string) string { return "experience-" + trajectoryDigest(kind+"\x00"+key) }

// This stream lives in the Session Ledger, not in a separate memory database.
// One owner-stream CAS makes cross-session revisions and tombstones atomic.
func (m *LedgerMemory) catalog(ctx context.Context, owner uint) (session.LedgerSnapshot, map[string]Experience, map[string]bool, error) {
	snapshot, err := session.ReadVerifiedSnapshot(ctx, m.ledger, catalogID(owner))
	if errors.Is(err, session.ErrSessionNotFound) {
		return session.LedgerSnapshot{}, map[string]Experience{}, map[string]bool{}, nil
	}
	if err != nil {
		return snapshot, nil, nil, err
	}
	entries, applied := map[string]Experience{}, map[string]bool{}
	for i, e := range snapshot.Events {
		if i == 0 {
			var header struct {
				SchemaVersion int  `json:"schema_version"`
				OwnerID       uint `json:"owner_id"`
			}
			if e.Type != "memory/catalog-created" || json.Unmarshal(e.Payload, &header) != nil || header.SchemaVersion != 1 || header.OwnerID != owner {
				return snapshot, nil, nil, ErrTrajectoryIntegrity
			}
			continue
		}
		var mutation catalogMutation
		if e.Type != "memory/catalog-updated" || json.Unmarshal(e.Payload, &mutation) != nil || mutation.SchemaVersion != 1 || mutation.OwnerID != owner || len(mutation.Entries) > 8 {
			return snapshot, nil, nil, ErrTrajectoryIntegrity
		}
		for _, item := range mutation.Entries {
			previous := entries[item.ID]
			if item.ID != experienceID(item.Kind, item.Key) || item.Revision != previous.Revision+1 || validateExperience(item) != nil {
				return snapshot, nil, nil, ErrTrajectoryIntegrity
			}
			entries[item.ID] = item
		}
		if mutation.SourceID != "" {
			applied[mutation.SourceID] = true
		}
	}
	return snapshot, entries, applied, nil
}

func validateExperience(item Experience) error {
	if !experienceKinds[item.Kind] || !memoryKey.MatchString(item.Key) || item.Revision == 0 || len(item.Origins) == 0 || len(item.Origins) > 64 || len(item.Tags) > 32 {
		return errors.New("invalid memory identity or provenance")
	}
	for _, tag := range item.Tags {
		if utf8.RuneCountInString(tag) > 64 || !utf8.ValidString(tag) {
			return errors.New("invalid memory tag")
		}
	}
	if err := validateMemoryFields("", item.Content, normalizeTags(item.Tags)); err != nil {
		return err
	}
	for _, field := range []struct {
		text  string
		limit int
	}{{item.Abstract, 240}, {item.Overview, 1200}, {item.Content, 4000}} {
		text, limit := field.text, field.limit
		if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > limit || sensitiveMemoryText(text) {
			return errors.New("unsafe memory content")
		}
	}
	return nil
}

func sensitiveMemoryText(text string) bool {
	if memoryCredentialPattern.MatchString(text) {
		return true
	}
	for _, pattern := range trajectorySecretPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func (m *LedgerMemory) updateCatalog(ctx context.Context, owner uint, sourceID string, update func(map[string]Experience) ([]Experience, error)) error {
	if owner == 0 {
		return session.ErrSessionOwnerRequired
	}
	for attempt := 0; attempt < 16; attempt++ {
		snapshot, entries, applied, err := m.catalog(ctx, owner)
		if err != nil {
			return err
		}
		if len(snapshot.Events) == 0 {
			_, err = m.ledger.Append(ctx, catalogID(owner), 0, "memory/catalog-created", map[string]any{"schema_version": 1, "owner_id": owner})
			if err == nil || errors.Is(err, session.ErrSequenceConflict) {
				continue
			}
			return err
		}
		if sourceID != "" && applied[sourceID] {
			return nil
		}
		changes, err := update(entries)
		if err != nil {
			return err
		}
		if len(changes) == 0 && sourceID == "" {
			return nil
		}
		_, err = m.ledger.Append(ctx, catalogID(owner), int64(len(snapshot.Events)), "memory/catalog-updated", catalogMutation{SchemaVersion: 1, OwnerID: owner, SourceID: sourceID, Entries: changes})
		if err == nil {
			return nil
		}
		if !errors.Is(err, session.ErrSequenceConflict) {
			return err
		}
	}
	return session.ErrSequenceConflict
}

func actorFromMemoryEvents(events []session.Event) (identity.Actor, error) {
	for i := len(events) - 1; i >= 0; i-- {
		var payload struct {
			Actor identity.Actor `json:"actor"`
		}
		if json.Unmarshal(events[i].Payload, &payload) == nil && payload.Actor.SessionID == events[i].SessionID && payload.Actor.Validate() == nil {
			return payload.Actor, nil
		}
	}
	return identity.Actor{}, session.ErrSessionOwnerRequired
}

func (m *LedgerMemory) reflectTrajectory(ctx context.Context, snapshot session.LedgerSnapshot, owner uint, commit trajectoryCommit) error {
	if m.reflector == nil {
		return nil
	}
	sourceID := commit.Trajectory.SessionID + ":" + commit.Trajectory.SourceChecksum
	_, _, applied, err := m.catalog(ctx, owner)
	if err != nil || applied[sourceID] {
		return err
	}
	actor, err := actorFromMemoryEvents(snapshot.Events)
	if err != nil {
		return err
	}
	request := &pb.MemoryReflectionRequest{SessionId: commit.Trajectory.SessionID, SourceChecksum: commit.Trajectory.SourceChecksum,
		Actor: &pb.ActorContext{SchemaVersion: actor.SchemaVersion, ActorId: actor.ActorID, Subject: actor.Subject, TenantId: actor.TenantID, Roles: actor.Roles, SessionId: actor.SessionID}}
	sources := commit.Trajectory.Sources
	if len(sources) > 64 {
		sources = sources[len(sources)-64:]
	}
	for _, source := range sources {
		e, err := resolveTrajectorySource(snapshot.Events, source)
		if err != nil {
			return err
		}
		text := source.Type
		if e.Type == "user/message" || e.Type == "assistant/message" {
			body, err := messageText(e)
			if err != nil {
				return err
			}
			text += ": " + summary(safeTrajectoryText(body), 1500)
		} else if e.Type == "memory/cli-turn-completed" {
			text += ": " + summary(commit.Trajectory.Overview, 2000)
		}
		limit := 24000 / len(sources)
		if len(text) > limit {
			for limit > 0 && !utf8.RuneStart(text[limit]) {
				limit--
			}
			text = text[:limit]
		}
		request.Sources = append(request.Sources, &pb.MemorySource{EventId: source.EventID, Checksum: source.Checksum, Text: text})
	}
	var response *pb.MemoryReflectionResponse
	for _, event := range snapshot.Events {
		if event.Type != "memory/reflection-proposed" {
			continue
		}
		var proposal pb.MemoryReflectionResponse
		if json.Unmarshal(event.Payload, &proposal) != nil {
			return ErrTrajectoryIntegrity
		}
		if proposal.SourceChecksum == request.SourceChecksum {
			response = &proposal
			break
		}
	}
	if response == nil {
		response, err = m.reflector(ctx, request)
		if err != nil || response == nil || response.ErrorCode != "" {
			return errors.New("memory reflection unavailable")
		}
		if err = validateReflection(request, response); err != nil {
			return err
		}
		for attempt := 0; attempt < 8; attempt++ {
			current, readErr := session.ReadVerifiedSnapshot(ctx, m.ledger, request.SessionId)
			if readErr != nil {
				return readErr
			}
			active, _, readErr := activeTrajectoryCommit(current, owner)
			if readErr != nil || active == nil || active.Trajectory.SourceChecksum != request.SourceChecksum {
				return ErrIncompleteTrajectory
			}
			// Concurrent commits adopt the first durable proposal for this source.
			adopted := false
			for _, event := range current.Events {
				if event.Type != "memory/reflection-proposed" {
					continue
				}
				var proposal pb.MemoryReflectionResponse
				if json.Unmarshal(event.Payload, &proposal) != nil {
					return ErrTrajectoryIntegrity
				}
				if proposal.SourceChecksum == request.SourceChecksum {
					response = &proposal
					err = nil
					adopted = true
					break
				}
			}
			if adopted {
				break
			}
			_, err = m.ledger.Append(ctx, request.SessionId, int64(len(current.Events)), "memory/reflection-proposed", response)
			if err == nil {
				break
			}
			if !errors.Is(err, session.ErrSequenceConflict) {
				return err
			}
		}
		if err != nil {
			return err
		}
	}
	if err := validateReflection(request, response); err != nil {
		return err
	}
	return m.updateCatalog(ctx, owner, sourceID, func(entries map[string]Experience) ([]Experience, error) {
		current, err := session.ReadVerifiedSnapshot(ctx, m.ledger, request.SessionId)
		if err != nil {
			return nil, err
		}
		active, _, err := activeTrajectoryCommit(current, owner)
		if err != nil || active == nil || active.Trajectory.SourceChecksum != request.SourceChecksum {
			return nil, ErrIncompleteTrajectory
		}
		changes := make([]Experience, 0, len(response.Candidates))
		for _, candidate := range response.Candidates {
			id := experienceID(candidate.Kind, candidate.Key)
			previous := entries[id]
			if previous.Deleted {
				continue
			}
			origin := memoryOrigin{SessionID: request.SessionId, SourceChecksum: request.SourceChecksum}
			for _, source := range sources {
				for _, ref := range candidate.SourceEventIds {
					if ref == source.EventID {
						origin.Sources = append(origin.Sources, source)
					}
				}
			}
			origins := append(append([]memoryOrigin(nil), previous.Origins...), origin)
			if len(origins) > 64 {
				origins = origins[len(origins)-64:]
			}
			item := Experience{ID: id, Kind: candidate.Kind, Key: candidate.Key, Abstract: candidate.Abstract, Overview: candidate.Overview, Content: candidate.Content,
				Revision: previous.Revision + 1, Origins: origins, UpdatedAt: time.Now().UTC(), ExpiresAt: previous.ExpiresAt, Tags: previous.Tags}
			if err := validateExperience(item); err != nil {
				return nil, err
			}
			changes = append(changes, item)
		}
		return changes, nil
	})
}

func validateReflection(request *pb.MemoryReflectionRequest, response *pb.MemoryReflectionResponse) error {
	if response.SourceChecksum != request.SourceChecksum || response.ErrorCode != "" || len(response.Candidates) > 8 {
		return ErrTrajectoryIntegrity
	}
	ids, keys := map[string]bool{}, map[string]bool{}
	for _, source := range request.Sources {
		ids[source.EventId] = true
	}
	for _, candidate := range response.Candidates {
		if candidate == nil || !experienceKinds[candidate.Kind] || !memoryKey.MatchString(candidate.Key) || len(candidate.SourceEventIds) == 0 || len(candidate.SourceEventIds) > 64 {
			return ErrTrajectoryIntegrity
		}
		key := candidate.Kind + ":" + candidate.Key
		if keys[key] {
			return ErrTrajectoryIntegrity
		}
		keys[key] = true
		seen := map[string]bool{}
		for _, id := range candidate.SourceEventIds {
			if !ids[id] || seen[id] {
				return ErrTrajectoryIntegrity
			}
			seen[id] = true
		}
		item := Experience{Kind: candidate.Kind, Key: candidate.Key, Abstract: candidate.Abstract, Overview: candidate.Overview, Content: candidate.Content, Revision: 1, Origins: []memoryOrigin{{}}}
		if validateExperience(item) != nil {
			return ErrTrajectoryIntegrity
		}
	}
	return nil
}

func (m *LedgerMemory) sourceSessions(ctx context.Context, owner uint) ([]session.SessionView, error) {
	ids, err := m.ledger.SessionIDs(ctx)
	if err != nil {
		return nil, err
	}
	var views []session.SessionView
	for _, id := range ids {
		events, err := m.ledger.Events(ctx, id)
		if err != nil {
			return nil, err
		}
		actual, ownerErr := trajectoryOwner(events)
		if ownerErr != nil || actual != owner {
			continue
		}
		views = append(views, session.SessionView{ID: id})
	}
	return views, nil
}

func (m *LedgerMemory) experienceItems(ctx context.Context, owner uint, detail string) ([]Memory, error) {
	if detail == "" {
		detail = "overview"
	}
	if detail != "abstract" && detail != "overview" && detail != "full" {
		return nil, errors.New("invalid memory detail")
	}
	_, entries, _, err := m.catalog(ctx, owner)
	if err != nil {
		return nil, err
	}
	var items []Memory
	for _, entry := range entries {
		if entry.Deleted || entry.ExpiresAt != nil && !entry.ExpiresAt.After(time.Now()) {
			continue
		}
		validOrigins := make([]memoryOrigin, 0, len(entry.Origins))
		for _, origin := range entry.Origins[len(entry.Origins)-1:] {
			snapshot, err := session.ReadVerifiedSnapshot(ctx, m.ledger, origin.SessionID)
			if err != nil {
				return nil, err
			}
			actualOwner, err := trajectoryOwner(snapshot.Events)
			if errors.Is(err, session.ErrSessionNotFound) {
				continue
			}
			if err != nil || actualOwner != owner {
				return nil, ErrTrajectoryIntegrity
			}
			branch := map[string]string{}
			active := map[string]bool{}
			for _, fact := range snapshot.Surface {
				active[fact.EventID] = true
			}
			for _, fact := range trajectoryBranch(snapshot.Events) {
				if fact.SurfaceOp == nil || active[fact.EventID] {
					branch[fact.EventID] = fact.Checksum
				}
			}
			valid := len(origin.Sources) > 0
			for _, ref := range origin.Sources {
				if branch[ref.EventID] != ref.Checksum {
					valid = false
				}
			}
			if valid {
				validOrigins = append(validOrigins, origin)
			}
		}
		if len(validOrigins) == 0 {
			continue
		}
		origin := validOrigins[len(validOrigins)-1]
		content := entry.Overview
		if detail == "abstract" {
			content = entry.Abstract
		} else if detail == "full" {
			content = entry.Content
		}
		item := Memory{ID: entry.ID, Name: entry.Key, Content: content, Tags: normalizeTags(append(append([]string(nil), entry.Tags...), entry.Kind)), Namespace: "user", Kind: entry.Kind, Detail: detail,
			SourceURI: "session://" + origin.SessionID + "/memory/" + entry.ID, SourceChecksum: origin.SourceChecksum, SessionID: origin.SessionID, UpdatedAt: entry.UpdatedAt, CreatedAt: entry.UpdatedAt}
		item.Checksum = checksumMemory(item)
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

func (m *LedgerMemory) Manage(ctx context.Context, sourceSessionID string, command session.MemoryCommand) (string, error) {
	snapshot, err := session.ReadVerifiedSnapshot(ctx, m.ledger, sourceSessionID)
	if err != nil {
		return "", err
	}
	owner, err := trajectoryOwner(snapshot.Events)
	if err != nil {
		return "", err
	}
	_, entries, _, err := m.catalog(ctx, owner)
	if err != nil {
		return "", err
	}
	if command.Action == "list" {
		items, err := m.experienceItems(ctx, owner, "abstract")
		if err != nil {
			return "", err
		}
		if len(items) > 100 {
			items = items[:100]
		}
		result, err := json.Marshal(items)
		return string(result), err
	}
	if command.Action == "read" {
		item, ok := entries[command.ID]
		if !ok || item.Deleted || item.ExpiresAt != nil && !item.ExpiresAt.After(time.Now()) {
			return "", errors.New("memory not found")
		}
		valid, err := m.experienceItems(ctx, owner, "full")
		if err != nil {
			return "", err
		}
		found := false
		for _, live := range valid {
			if live.ID == item.ID {
				found = true
			}
		}
		if !found {
			return "", errors.New("memory source unavailable")
		}
		result, err := json.Marshal(item)
		return string(result), err
	}
	if command.Action != "remember" && command.Action != "forget" && command.Action != "retain" {
		return "", errors.New("invalid memory action")
	}
	if command.TTLSeconds < 0 || command.TTLSeconds > 365*24*3600 {
		return "", errors.New("invalid memory retention")
	}
	id := command.ID
	if command.Action == "remember" {
		if command.Kind == "" {
			command.Kind = "preferences"
		}
		if command.Key == "" {
			command.Key = "note-" + trajectoryDigest(command.Content)[:16]
		}
		id = experienceID(command.Kind, command.Key)
	}
	var saved Experience
	err = m.updateCatalog(ctx, owner, "", func(current map[string]Experience) ([]Experience, error) {
		previous, exists := current[id]
		if previous.Revision != command.ExpectedRevision {
			return nil, session.ErrSequenceConflict
		}
		if command.Action != "remember" && !exists {
			return nil, errors.New("memory not found")
		}
		if previous.Deleted && command.Action == "retain" {
			return nil, errors.New("forgotten memory requires explicit remember")
		}
		saved = previous
		if command.Action == "remember" {
			fact := snapshot.Events[len(snapshot.Events)-1]
			digest := sha256.Sum256(fact.Payload)
			saved = Experience{ID: id, Kind: command.Kind, Key: command.Key, Abstract: summary(command.Content, 240), Overview: summary(command.Content, 1200), Content: command.Content,
				Tags: normalizeTags(command.Tags),
				Origins: []memoryOrigin{{SessionID: sourceSessionID, SourceChecksum: hex.EncodeToString(digest[:]), Manual: true,
					Sources: []TrajectorySource{{Seq: fact.Seq, EventID: fact.EventID, Checksum: fact.Checksum, Type: fact.Type}}}}}
		}
		saved.Revision = previous.Revision + 1
		saved.UpdatedAt = time.Now().UTC()
		saved.Deleted = command.Action == "forget"
		if command.TTLSeconds > 0 {
			expiry := saved.UpdatedAt.Add(time.Duration(command.TTLSeconds) * time.Second)
			saved.ExpiresAt = &expiry
		} else if command.Action == "retain" {
			saved.ExpiresAt = nil
		}
		if err := validateExperience(saved); err != nil {
			return nil, err
		}
		return []Experience{saved}, nil
	})
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(saved)
	return string(encoded), err
}
