package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"code-agent/internal/session"
)

// ErrTrajectoryIntegrity means that a trusted ledger could be read, but its
// event payloads do not provide a safe, replayable trajectory. Memory
// extraction is fail-closed for this class of input.
var ErrTrajectoryIntegrity = errors.New("session trajectory integrity failure")

const (
	defaultTrajectoryOverviewChars = 4000
	trajectoryOutcomeCompleted     = "completed"
	trajectoryOutcomeFailed        = "failed"
	trajectoryOutcomeIncomplete    = "incomplete"
)

// TrajectoryOptions controls the bounded, deterministic projection built from
// a Session Ledger. Zero values select conservative defaults.
type TrajectoryOptions struct {
	MaxOverviewChars int `json:"max_overview_chars,omitempty"`
}

// TrajectorySource identifies one immutable ledger fact used by a derived
// trajectory. The event payload itself remains in the Session Ledger.
type TrajectorySource struct {
	Seq      int64  `json:"seq"`
	EventID  string `json:"event_id"`
	Checksum string `json:"checksum"`
	Type     string `json:"type"`
}

// SessionTrajectory is a read-only, replayable projection of trusted session
// facts. It is intentionally separate from Memory: incomplete projections can
// be inspected without being promoted to long-term memory.
type SessionTrajectory struct {
	SessionID      string             `json:"session_id"`
	FirstSeq       int64              `json:"first_seq"`
	LastSeq        int64              `json:"last_seq"`
	Sources        []TrajectorySource `json:"sources"`
	Outcome        string             `json:"outcome"`
	Complete       bool               `json:"complete"`
	Overview       string             `json:"overview"`
	SourceChecksum string             `json:"source_checksum"`
}

// resolveTrajectorySource binds a derived source reference back to the
// verified immutable event. Never index the ledger from an untrusted sequence
// without checking both its range and its identity fields first.
func resolveTrajectorySource(events []session.Event, source TrajectorySource) (session.Event, error) {
	if source.Seq < 0 || source.Seq >= int64(len(events)) {
		return session.Event{}, fmt.Errorf("%w: source sequence %d is out of range", ErrTrajectoryIntegrity, source.Seq)
	}
	event := events[int(source.Seq)]
	if event.Seq != source.Seq || event.EventID != source.EventID || event.Checksum != source.Checksum || event.Type != source.Type {
		return session.Event{}, fmt.Errorf("%w: source sequence %d identity mismatch", ErrTrajectoryIntegrity, source.Seq)
	}
	return event, nil
}

// BuildSessionTrajectory verifies and deterministically projects a session's
// immutable event stream. It never writes to the ledger or to the Memory
// store.
func BuildSessionTrajectory(ctx context.Context, ledger session.EventLog, sessionID string, options TrajectoryOptions) (SessionTrajectory, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return SessionTrajectory{}, fmt.Errorf("%w: session id is required", ErrTrajectoryIntegrity)
	}
	if ledger == nil {
		return SessionTrajectory{}, fmt.Errorf("%w: session ledger is required", ErrTrajectoryIntegrity)
	}
	snapshot, err := session.ReadVerifiedSnapshot(ctx, ledger, sessionID)
	if err != nil {
		return SessionTrajectory{}, fmt.Errorf("verify session ledger snapshot: %w", err)
	}
	return buildSessionTrajectory(snapshot, sessionID, options)
}

func buildSessionTrajectory(snapshot session.LedgerSnapshot, sessionID string, options TrajectoryOptions) (SessionTrajectory, error) {
	if len(snapshot.Events) > 0 && (snapshot.Events[0].Type == "session/state" || snapshot.Events[0].Type == "legacy/import") {
		return buildLegacyTrajectory(snapshot, sessionID)
	}
	maxOverview := options.MaxOverviewChars
	if maxOverview == 0 {
		maxOverview = defaultTrajectoryOverviewChars
	}
	if maxOverview < 128 {
		return SessionTrajectory{}, fmt.Errorf("%w: overview limit is too small", ErrTrajectoryIntegrity)
	}
	surface, events := snapshot.Surface, snapshot.Events
	if len(events) == 0 {
		return SessionTrajectory{}, fmt.Errorf("%w: session has no events", ErrTrajectoryIntegrity)
	}

	// Surface-marked events are the canonical model-visible branch. Log-only
	// events (which have no SurfaceOp) remain eligible for audit/provenance,
	// while hidden surface events must not be replayed into memory. Keeping
	// legacy nil-SurfaceOp events eligible preserves compatibility with older
	// adapters that predate explicit surface operations.
	activeSurface := make(map[string]struct{}, len(surface))
	for _, event := range surface {
		activeSurface[event.EventID] = struct{}{}
	}
	selected := make([]session.Event, 0, len(events))
	for _, event := range trajectoryBranch(events) {
		if event.SurfaceOp != nil {
			if _, ok := activeSurface[event.EventID]; !ok {
				continue
			}
		}
		selected = append(selected, event)
	}

	projection := trajectoryProjection{
		openTools: make(map[string]string),
		runs:      make(map[string]struct{}),
		terminals: make(map[string]string),
	}
	for _, event := range selected {
		if err := projection.consume(event); err != nil {
			return SessionTrajectory{}, err
		}
	}
	if len(projection.sources) == 0 {
		return SessionTrajectory{}, fmt.Errorf("%w: no trajectory events for session %s", ErrTrajectoryIntegrity, sessionID)
	}

	outcome := trajectoryOutcomeIncomplete
	complete := false
	if projection.terminalSeen && !projection.uncertain && len(projection.openTools) == 0 && projection.allRunsTerminal() {
		outcome = projection.terminalOutcome
		complete = outcome == trajectoryOutcomeCompleted || outcome == trajectoryOutcomeFailed
	}
	if projection.terminalSeen && projection.terminalOutcome != "" {
		// A terminal fact is still useful for an incomplete trajectory, but the
		// outcome must describe the observed terminal evidence rather than hide it.
		outcome = projection.terminalOutcome
		if !complete {
			outcome = trajectoryOutcomeIncomplete
		}
	}

	overview := strings.Join(projection.lines, "\n")
	overview = boundTrajectoryOverview(overview, maxOverview)
	trajectory := SessionTrajectory{
		SessionID: sessionID,
		FirstSeq:  projection.firstSeq,
		LastSeq:   projection.lastSeq,
		Sources:   append([]TrajectorySource(nil), projection.sources...),
		Outcome:   outcome,
		Complete:  complete,
		Overview:  overview,
	}
	trajectory.SourceChecksum = checksumTrajectory(trajectory)
	return trajectory, nil
}

// Follow rollback anchors backwards so abandoned log-only facts are excluded
// as well as hidden surface events. Rewinding to an earlier branch still works.
func trajectoryBranch(events []session.Event) []session.Event {
	reversed := make([]session.Event, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Type == "session/rewind" || event.Type == "session/continued" {
			var marker struct {
				TargetSeq int64 `json:"target_seq"`
			}
			_ = json.Unmarshal(event.Payload, &marker)
			i = int(marker.TargetSeq) + 1
			continue
		}
		reversed = append(reversed, event)
	}
	branch := make([]session.Event, len(reversed))
	for i := range reversed {
		branch[len(reversed)-1-i] = reversed[i]
	}
	return branch
}

// SaveSessionTrajectory derives a trajectory and promotes it only after a
// complete terminal run with closed tool calls. Incomplete trajectories are
// returned with a nil Memory and never write a file.
func (m *Manager) SaveSessionTrajectory(ctx context.Context, ledger session.EventLog, sessionID string, options TrajectoryOptions) (SessionTrajectory, *Memory, error) {
	trajectory, err := BuildSessionTrajectory(ctx, ledger, sessionID, options)
	if err != nil {
		return SessionTrajectory{}, nil, err
	}
	if !trajectory.Complete {
		return trajectory, nil, nil
	}

	item := trajectoryMemory(trajectory)
	if existing, ok := m.Get(item.ID); ok && sameTrajectoryMemory(existing, item) {
		return trajectory, &existing, nil
	}
	if existing, ok := m.Get(item.ID); ok {
		item.CreatedAt = existing.CreatedAt
	}
	saved, err := m.Save(item)
	if err != nil {
		return trajectory, nil, err
	}
	return trajectory, &saved, nil
}

func trajectoryMemory(trajectory SessionTrajectory) Memory {
	digest := trajectoryDigest(trajectory.SessionID)
	name := slugify("session-trajectory-" + trajectory.SessionID + "-" + digest[:12])
	if name == "" {
		name = "session-trajectory-" + digest[:16]
	}
	return Memory{
		ID:             "trajectory-" + digest,
		Name:           name,
		Content:        trajectory.Overview,
		Tags:           []string{"session", "trajectory", trajectory.Outcome},
		Namespace:      "user",
		Kind:           "trajectories",
		Detail:         "overview",
		SourceURI:      "session://" + url.PathEscape(trajectory.SessionID) + "/trajectory/" + strconv.FormatInt(trajectory.LastSeq, 10),
		SourceChecksum: trajectory.SourceChecksum,
		SessionID:      trajectory.SessionID,
	}
}

func sameTrajectoryMemory(existing, next Memory) bool {
	return existing.SourceChecksum == next.SourceChecksum &&
		existing.Content == next.Content &&
		existing.SourceURI == next.SourceURI &&
		existing.SessionID == next.SessionID &&
		existing.Kind == next.Kind &&
		existing.Detail == next.Detail
}

type trajectoryProjection struct {
	firstSeq        int64
	lastSeq         int64
	haveSource      bool
	sources         []TrajectorySource
	lines           []string
	openTools       map[string]string
	runs            map[string]struct{}
	terminals       map[string]string
	terminalSeen    bool
	terminalOutcome string
	uncertain       bool
}

func (p *trajectoryProjection) consume(event session.Event) error {
	switch event.Type {
	case "user/message", "assistant/message":
		p.terminalSeen = false
		p.terminalOutcome = ""
		text, err := messageText(event)
		if err != nil {
			return err
		}
		label := "Assistant"
		if event.Type == "user/message" {
			label = "User"
		}
		line := label
		if text != "" {
			line += ": " + safeTrajectoryText(text)
		}
		p.add(event, line)
	case "tool/call":
		var payload struct {
			RunID      string `json:"run_id"`
			ToolCallID string `json:"tool_call_id"`
			ToolName   string `json:"tool_name"`
		}
		if err := decodeObject(event, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(payload.RunID) == "" || strings.TrimSpace(payload.ToolCallID) == "" || strings.TrimSpace(payload.ToolName) == "" {
			return trajectoryPayloadError(event, "tool call identity is required")
		}
		key := trajectoryToolKey(payload.RunID, payload.ToolCallID)
		if _, terminal := p.terminals[payload.RunID]; terminal {
			return trajectoryPayloadError(event, "tool call follows its run terminal")
		}
		if _, exists := p.openTools[key]; exists {
			return trajectoryPayloadError(event, "duplicate open tool call")
		}
		p.openTools[key] = payload.ToolName
		p.addRun(payload.RunID)
		p.add(event, "Tool call: "+safeTrajectoryText(safeIdentifier(payload.ToolName)))
	case "tool/result":
		var payload struct {
			RunID      string `json:"run_id"`
			ToolCallID string `json:"tool_call_id"`
			ToolName   string `json:"tool_name"`
			ExitCode   int32  `json:"exit_code"`
		}
		if err := decodeObject(event, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(payload.RunID) == "" || strings.TrimSpace(payload.ToolCallID) == "" || strings.TrimSpace(payload.ToolName) == "" {
			return trajectoryPayloadError(event, "tool result identity is required")
		}
		key := trajectoryToolKey(payload.RunID, payload.ToolCallID)
		if toolName, exists := p.openTools[key]; !exists || toolName != payload.ToolName {
			return trajectoryPayloadError(event, "tool result has no matching call")
		}
		delete(p.openTools, key)
		p.addRun(payload.RunID)
		p.add(event, fmt.Sprintf("Tool result: %s (exit %d)", safeTrajectoryText(safeIdentifier(payload.ToolName)), payload.ExitCode))
	case "code/modified":
		var payload struct {
			RunID        string  `json:"run_id"`
			ToolCallID   string  `json:"tool_call_id"`
			ToolName     string  `json:"tool_name"`
			Path         string  `json:"path"`
			Operation    string  `json:"operation"`
			Summary      string  `json:"summary"`
			BeforeSHA256 string  `json:"before_sha256"`
			AfterSHA256  string  `json:"after_sha256"`
			DiffSHA256   string  `json:"diff_sha256"`
			Before       *string `json:"before,omitempty"`
			After        *string `json:"after,omitempty"`
		}
		if err := decodeObject(event, &payload); err != nil {
			return err
		}
		if toolName, exists := p.openTools[trajectoryToolKey(payload.RunID, payload.ToolCallID)]; !exists || toolName != payload.ToolName {
			return trajectoryPayloadError(event, "code modification has no matching open tool call")
		}
		path := strings.TrimSpace(payload.Path)
		if strings.TrimSpace(payload.RunID) == "" || strings.TrimSpace(payload.ToolCallID) == "" || strings.TrimSpace(payload.ToolName) == "" || path == "" || strings.ContainsAny(path, "\r\n\t") || strings.HasPrefix(path, "/") || strings.Contains(path, "..\\") || strings.Contains(path, "../") {
			return trajectoryPayloadError(event, "code modification path is invalid")
		}
		for _, digest := range []string{payload.BeforeSHA256, payload.AfterSHA256, payload.DiffSHA256} {
			if !isSHA256Digest(digest) {
				return trajectoryPayloadError(event, "code modification digest is invalid")
			}
		}
		if (payload.Before != nil && !matchesSHA256(*payload.Before, payload.BeforeSHA256)) ||
			(payload.After != nil && !matchesSHA256(*payload.After, payload.AfterSHA256)) ||
			(payload.Before != nil && payload.After != nil &&
				!matchesTransitionSHA256(*payload.Before, *payload.After, payload.DiffSHA256)) {
			return trajectoryPayloadError(event, "code modification receipt does not match captured bodies")
		}
		operation := safeIdentifier(payload.Operation)
		if operation == "" {
			operation = "modify"
		}
		p.addRun(payload.RunID)
		description := safeTrajectoryText(payload.Summary)
		if description == "" {
			description = operation + " " + path
		}
		p.add(event, "code/modified: "+description)
	case "session/run-completed", "session/run-failed":
		var payload struct {
			RunID string `json:"run_id"`
		}
		if err := decodeObject(event, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(payload.RunID) == "" {
			return trajectoryPayloadError(event, "run identity is required")
		}
		outcome := trajectoryOutcomeCompleted
		if event.Type == "session/run-failed" {
			outcome = trajectoryOutcomeFailed
		}
		runID := strings.TrimSpace(payload.RunID)
		p.addRun(runID)
		if previous, exists := p.terminals[runID]; exists {
			return trajectoryPayloadError(event, "duplicate run terminal: "+previous)
		}
		p.terminals[runID] = outcome
		p.terminalSeen = true
		p.terminalOutcome = outcome
		p.add(event, "Run outcome: "+outcome)
	case "session/run-leased", "session/run-heartbeat":
		var payload struct {
			RunID string `json:"run_id"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil {
			p.addRun(payload.RunID)
		}
	case "tool/unknown":
		// An unknown tool outcome is explicit evidence that the call/result
		// pair cannot be replayed safely. Keep the fact in the projection so
		// callers can inspect it, but never promote it to long-term memory.
		if !json.Valid(event.Payload) {
			return trajectoryPayloadError(event, "unknown tool payload is invalid")
		}
		p.uncertain = true
		p.add(event, "Tool outcome: unknown")
	case "session/progress":
		var payload struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
		}
		if err := decodeObject(event, &payload); err != nil {
			return err
		}
		text := safeTrajectoryText(strings.TrimSpace(strings.Join([]string{payload.Title, payload.Summary}, " — ")))
		line := "Progress"
		if text != "" {
			line += ": " + text
		}
		p.add(event, line)
	case "session/plan-todo":
		// Keep the projection intentionally short; the full plan/todo value
		// remains available from the source event and is not copied to memory.
		var payload map[string]any
		if err := decodeObject(event, &payload); err != nil {
			return err
		}
		p.add(event, "Plan/todo updated")
	case "context/compaction":
		var payload struct {
			Summary string `json:"summary"`
		}
		if err := decodeObject(event, &payload); err != nil {
			return err
		}
		text := safeTrajectoryText(payload.Summary)
		line := "Compaction"
		if text != "" {
			line += ": " + text
		}
		p.add(event, line)
	default:
		// Heartbeats, leases, approvals, worktree transport and other
		// operational noise remain in the ledger but are not memory facts.
	}
	return nil
}

func (p *trajectoryProjection) add(event session.Event, line string) {
	if !p.haveSource {
		p.firstSeq = event.Seq
		p.haveSource = true
	}
	p.lastSeq = event.Seq
	p.sources = append(p.sources, TrajectorySource{Seq: event.Seq, EventID: event.EventID, Checksum: event.Checksum, Type: event.Type})
	if line != "" {
		p.lines = append(p.lines, line)
	}
}

func (p *trajectoryProjection) addRun(runID string) {
	if runID = strings.TrimSpace(runID); runID != "" {
		p.runs[runID] = struct{}{}
	}
}

func boundTrajectoryOverview(text string, maxChars int) string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	marker := "\n[earlier details omitted]\n"
	headChars := maxChars / 4
	tailChars := maxChars - headChars - len(marker)
	return string(runes[:headChars]) + marker + string(runes[len(runes)-tailChars:])
}

func (p *trajectoryProjection) allRunsTerminal() bool {
	if len(p.runs) == 0 {
		return p.terminalSeen
	}
	for runID := range p.runs {
		if _, ok := p.terminals[runID]; !ok {
			return false
		}
	}
	return true
}

func decodeObject(event session.Event, target any) error {
	if !json.Valid(event.Payload) {
		return trajectoryPayloadError(event, "payload is invalid JSON")
	}
	trimmed := strings.TrimSpace(string(event.Payload))
	if trimmed == "" || trimmed[0] != '{' {
		return trajectoryPayloadError(event, "payload must be a JSON object")
	}
	if err := json.Unmarshal(event.Payload, target); err != nil {
		return trajectoryPayloadError(event, err.Error())
	}
	return nil
}

func messageText(event session.Event) (string, error) {
	if len(event.Payload) == 0 {
		return "", trajectoryPayloadError(event, "message payload is empty")
	}
	var raw any
	if err := json.Unmarshal(event.Payload, &raw); err != nil {
		return "", trajectoryPayloadError(event, err.Error())
	}
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value), nil
	case map[string]any:
		for _, key := range []string{"content", "text", "message"} {
			if text, ok := value[key].(string); ok {
				return strings.TrimSpace(text), nil
			}
		}
		return "", nil
	default:
		return "", trajectoryPayloadError(event, "message payload must be a string or object")
	}
}

func trajectoryPayloadError(event session.Event, detail string) error {
	return fmt.Errorf("%w: seq %d (%s): %s", ErrTrajectoryIntegrity, event.Seq, event.Type, detail)
}

func trajectoryToolKey(runID, callID string) string {
	return strings.TrimSpace(runID) + "\x00" + strings.TrimSpace(callID)
}

func safeIdentifier(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if len([]rune(value)) > 96 {
		value = string([]rune(value)[:96])
	}
	return value
}

func isSHA256Digest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func matchesSHA256(value, expected string) bool {
	digest := sha256.Sum256([]byte(value))
	return strings.EqualFold(hex.EncodeToString(digest[:]), strings.TrimSpace(expected))
}

func matchesTransitionSHA256(before, after, expected string) bool {
	hash := sha256.New()
	_, _ = hash.Write([]byte(before))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(after))
	return strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), strings.TrimSpace(expected))
}

var trajectorySecretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(?:['"]?\b(?:api[_-]?key|access[_-]?token|refresh[_-]?token|authorization|password|passwd|secret|credential)\b['"]?\s*[:=]\s*)(?:['"][^'"\r\n]+['"]|[^\s,;]+)`),
	regexp.MustCompile(`(?i)\b(?:bearer\s+|sk-)[A-Za-z0-9._~+/=-]{6,}`),
	regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@`),
	regexp.MustCompile(`-----BEGIN [^-]+-----[\s\S]*?-----END [^-]+-----`),
}

func safeTrajectoryText(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	for _, pattern := range trajectorySecretPatterns {
		if pattern.MatchString(value) {
			value = pattern.ReplaceAllString(value, "[redacted]")
		}
	}
	if len([]rune(value)) > 480 {
		value = string([]rune(value)[:477]) + "..."
	}
	return value
}

func boundTrajectoryText(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= maximum {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:maximum-3])) + "..."
}

func checksumTrajectory(trajectory SessionTrajectory) string {
	payload := struct {
		SessionID string             `json:"session_id"`
		FirstSeq  int64              `json:"first_seq"`
		LastSeq   int64              `json:"last_seq"`
		Sources   []TrajectorySource `json:"sources"`
	}{trajectory.SessionID, trajectory.FirstSeq, trajectory.LastSeq, trajectory.Sources}
	encoded, _ := json.Marshal(payload)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func trajectoryDigest(sessionID string) string {
	digest := sha256.Sum256([]byte(sessionID))
	return hex.EncodeToString(digest[:])
}
