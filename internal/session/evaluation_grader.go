package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"go/format"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"code-agent/internal/identity"
	"code-agent/internal/safety"
	"code-agent/internal/sandbox"
)

const evaluationGradeIntentEvent = "evaluation/grade-intent"
const evaluationGradeResultEvent = "evaluation/grade-result"

type EvaluationGradeCommand struct {
	OwnerID                     uint
	Actor                       identity.Actor
	SessionID                   string
	RequestID                   string
	ExpectedSessionHeadChecksum string
	ChecksPath                  string
	ChecksSHA256                string
}

// EvaluationGradeView deliberately excludes assertion bodies, paths and raw output.
type EvaluationGradeView struct {
	State                  string `json:"state"`
	Passed                 *bool  `json:"passed"`
	ChecksTotal            *int   `json:"checks_total"`
	ChecksPassed           *int   `json:"checks_passed"`
	CandidateSHA256        string `json:"candidate_sha256"`
	CandidateArchiveSHA256 string `json:"candidate_archive_sha256"`
	InputSHA256            string `json:"input_sha256"`
	CheckerSHA256          string `json:"checker_sha256"`
	ChecksSHA256           string `json:"checks_sha256"`
	OutputSHA256           string `json:"output_sha256"`
	OperationID            string `json:"operation_id"`
	RuntimeID              string `json:"runtime_id"`
	Stopped                bool   `json:"stopped"`
	ExitCode               *int   `json:"exit_code"`
	IntentEventID          string `json:"intent_event_id"`
	IntentEventChecksum    string `json:"intent_event_checksum"`
	ResultEventID          string `json:"result_event_id"`
	ResultEventChecksum    string `json:"result_event_checksum"`
	AlreadyRecorded        bool   `json:"already_recorded"`
	Reason                 string `json:"reason,omitempty"`

	LeaseRevalidation *EvaluationLeaseRevalidation `json:"lease_revalidation,omitempty"`
}

type EvaluationLeaseRevalidation struct {
	Verified           bool      `json:"verified"`
	BeforeHeadChecksum string    `json:"before_head_checksum"`
	AfterHeadChecksum  string    `json:"after_head_checksum"`
	EventID            string    `json:"event_id"`
	EventChecksum      string    `json:"event_checksum"`
	LeaseID            string    `json:"lease_id"`
	PreviousExpiresAt  time.Time `json:"previous_expires_at"`
	ExpiresAt          time.Time `json:"expires_at"`
}

type evaluationGradePayload struct {
	SchemaVersion         int                 `json:"schema_version"`
	OwnerID               uint                `json:"owner_id"`
	Actor                 identity.Actor      `json:"actor"`
	RequestID             string              `json:"request_id"`
	BindingChecksum       string              `json:"binding_checksum"`
	SourceHeadChecksum    string              `json:"source_head_checksum"`
	ProposalEventID       string              `json:"proposal_event_id"`
	ProposalEventChecksum string              `json:"proposal_event_checksum"`
	PreparedEventID       string              `json:"prepared_event_id,omitempty"`
	PreparedEventChecksum string              `json:"prepared_event_checksum,omitempty"`
	AssertionCount        int                 `json:"assertion_count"`
	View                  EvaluationGradeView `json:"view"`
}

type evaluationGradePart struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type evaluationGradeOutput struct {
	SchemaVersion          int    `json:"schema_version"`
	Valid                  bool   `json:"valid"`
	Passed                 *bool  `json:"passed"`
	ChecksTotal            *int   `json:"checks_total"`
	ChecksPassed           *int   `json:"checks_passed"`
	CandidateSHA256        string `json:"candidate_sha256"`
	CandidateArchiveSHA256 string `json:"candidate_archive_sha256"`
	InputSHA256            string `json:"input_sha256"`
	CheckerSHA256          string `json:"checker_sha256"`
	ChecksSHA256           string `json:"checks_sha256"`
	ErrorCode              string `json:"error_code"`
}

func evaluationGradeHash(value string) bool {
	return regexp.MustCompile("^[0-9a-f]{64}$").MatchString(value)
}

func evaluationGradeRuntime(operation, runtimeID string) bool {
	return regexp.MustCompile("^" + operation + "/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/[0-9a-f]{64}$").MatchString(runtimeID)
}

func readEvaluationGrade(events []Event) (*evaluationGradePayload, error) {
	var found *evaluationGradePayload
	prior := map[string]Event{}
	for index, event := range events {
		if event.Type != evaluationGradeIntentEvent && event.Type != evaluationGradeResultEvent {
			prior[event.EventID] = event
			continue
		}
		var payload evaluationGradePayload
		if json.Unmarshal(event.Payload, &payload) != nil || payload.SchemaVersion != 1 && payload.SchemaVersion != 2 || payload.OwnerID == 0 || payload.Actor.Validate() != nil || payload.Actor.SessionID != event.SessionID || payload.RequestID == "" || !evaluationGradeHash(payload.BindingChecksum) || !evaluationGradeHash(payload.SourceHeadChecksum) || !evaluationGradeHash(payload.View.CandidateSHA256) || !evaluationGradeHash(payload.View.ChecksSHA256) || !evaluationGradeHash(payload.View.CheckerSHA256) || !evaluationGradeHash(payload.View.OperationID) || payload.AssertionCount < 1 || payload.AssertionCount > 256 {
			return nil, ErrEventIntegrity
		}
		// Revalidation belongs to a refused preflight, never to a stored grade.
		if payload.View.LeaseRevalidation != nil {
			return nil, ErrEventIntegrity
		}
		if event.Type == evaluationGradeIntentEvent {
			binding, bindingErr := ReadEvaluation(events[:index])
			bindingBytes, _ := json.Marshal(binding)
			if payload.SchemaVersion == 1 {
				proposalEvent, exists := prior[payload.ProposalEventID]
				var proposal taskProposalPayload
				if bindingErr != nil || binding == nil || hashCodeState(string(bindingBytes)) != payload.BindingChecksum || payload.SourceHeadChecksum != event.PrevChecksum || !exists || proposalEvent.Type != taskProposalOutcomeEvent || proposalEvent.Checksum != payload.ProposalEventChecksum || json.Unmarshal(proposalEvent.Payload, &proposal) != nil || proposal.OwnerID != payload.OwnerID || proposal.View.FinalStateSHA256 != payload.View.CandidateSHA256 || proposal.View.Test.InputSHA256 != payload.View.CandidateSHA256 || !proposal.View.Test.Stopped {
					return nil, ErrEventIntegrity
				}
			} else {
				preparedEvent, exists := prior[payload.PreparedEventID]
				prepared, state, prepareErr := taskPreparation(events[:index])
				var source taskPreparePayload
				sessionView, sessionErr := reduceSessionView(events[:index])
				if bindingErr != nil || binding == nil || hashCodeState(string(bindingBytes)) != payload.BindingChecksum || payload.SourceHeadChecksum != event.PrevChecksum || !exists || preparedEvent.Type != taskPreparedEvent || preparedEvent.Checksum != payload.PreparedEventChecksum || prepareErr != nil || prepared == nil || state != "prepared" || json.Unmarshal(preparedEvent.Payload, &source) != nil || source.Task.LeaseID != prepared.Task.LeaseID || source.Task.Baseline.Checksum != prepared.Task.Baseline.Checksum || sessionErr != nil || sessionView.UserID != payload.OwnerID || payload.ProposalEventID != "" || payload.ProposalEventChecksum != "" || taskToolOutcomesKnown(events[:index], "") != nil {
					return nil, ErrEventIntegrity
				}
				_, inputs, inputErr := proposalFiles(prepared.Task.Baseline, events[:index])
				inventory, _ := json.Marshal(inputs)
				if inputErr != nil || hashCodeState(string(inventory)) != payload.View.CandidateSHA256 {
					return nil, ErrEventIntegrity
				}
			}
			if found != nil || payload.View.State != "unknown" || payload.View.Passed != nil || payload.View.ChecksTotal != nil || payload.View.ChecksPassed != nil || payload.View.RuntimeID != "" || payload.View.Stopped || payload.View.ExitCode != nil || payload.View.IntentEventID != "" || payload.View.ResultEventID != "" || payload.View.AlreadyRecorded {
				return nil, ErrEventIntegrity
			}
			payload.View.IntentEventID, payload.View.IntentEventChecksum = event.EventID, event.Checksum
			found = &payload
			continue
		}
		if found == nil || payload.SchemaVersion != found.SchemaVersion || found.View.ResultEventID != "" || payload.RequestID != found.RequestID || payload.OwnerID != found.OwnerID || payload.Actor.ScopeKey() != found.Actor.ScopeKey() || payload.BindingChecksum != found.BindingChecksum || payload.SourceHeadChecksum != found.SourceHeadChecksum || payload.ProposalEventID != found.ProposalEventID || payload.ProposalEventChecksum != found.ProposalEventChecksum || payload.PreparedEventID != found.PreparedEventID || payload.PreparedEventChecksum != found.PreparedEventChecksum || payload.AssertionCount != found.AssertionCount || payload.View.OperationID != found.View.OperationID || payload.View.CandidateSHA256 != found.View.CandidateSHA256 || payload.View.ChecksSHA256 != found.View.ChecksSHA256 || payload.View.CheckerSHA256 != found.View.CheckerSHA256 || payload.View.IntentEventID != found.View.IntentEventID || payload.View.IntentEventChecksum != found.View.IntentEventChecksum || payload.View.ResultEventID != "" || payload.View.ResultEventChecksum != "" {
			return nil, ErrEventIntegrity
		}
		if payload.View.RuntimeID != "" && !evaluationGradeRuntime(payload.View.OperationID, payload.View.RuntimeID) {
			return nil, ErrEventIntegrity
		}
		switch payload.View.State {
		case "completed":
			v := payload.View
			if !v.Stopped || v.ExitCode == nil || *v.ExitCode != 0 || v.Passed == nil || v.ChecksTotal == nil || *v.ChecksTotal != payload.AssertionCount || v.ChecksPassed == nil || *v.ChecksPassed < 0 || *v.ChecksPassed > *v.ChecksTotal || *v.Passed != (*v.ChecksPassed == *v.ChecksTotal) || !evaluationGradeHash(v.CandidateArchiveSHA256) || !evaluationGradeHash(v.InputSHA256) || !evaluationGradeHash(v.OutputSHA256) || v.RuntimeID == "" {
				return nil, ErrEventIntegrity
			}
		case "blocked", "scorer_error", "unknown":
			if payload.View.Passed != nil || payload.View.ChecksTotal != nil || payload.View.ChecksPassed != nil {
				return nil, ErrEventIntegrity
			}
		default:
			return nil, ErrEventIntegrity
		}
		payload.View.ResultEventID, payload.View.ResultEventChecksum = event.EventID, event.Checksum
		found = &payload
	}
	return found, nil
}

// ReadEvaluationGrade projects only canonical Go grading facts; pending intent stays unknown.
func ReadEvaluationGrade(events []Event) (*EvaluationGradeView, error) {
	payload, err := readEvaluationGrade(events)
	if err != nil || payload == nil {
		return nil, err
	}
	result := payload.View
	return &result, nil
}

func evaluationGradeOutside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && (relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func readEvaluationChecks(ctx context.Context, path, pin, repository, storage string) ([]byte, int, error) {
	if !filepath.IsAbs(path) || !evaluationGradeHash(pin) || !evaluationGradeOutside(repository, path) || !evaluationGradeOutside(storage, path) {
		return nil, 0, ErrInvalidSessionInput
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !strings.EqualFold(filepath.Clean(path), filepath.Clean(resolved)) {
		return nil, 0, ErrInvalidSessionInput
	}
	release, err := safety.PinDirectories(filepath.Dir(path))
	if err != nil {
		return nil, 0, ErrSessionStateConflict
	}
	defer release()
	releaseFile, err := safety.PinReadOnlyFiles(path)
	if err != nil {
		return nil, 0, ErrSessionStateConflict
	}
	defer releaseFile()
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, ErrInvalidSessionInput
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 || ctx.Err() != nil {
		return nil, 0, ErrInvalidSessionInput
	}
	data, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || len(data) > 1<<20 || !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) || int64(len(data)) != info.Size() || hashCodeState(string(data)) != pin || !utf8.Valid(data) {
		return nil, 0, ErrInvalidSessionInput
	}
	var checks struct {
		SchemaVersion int               `json:"schema_version"`
		Files         []json.RawMessage `json:"files"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&checks) != nil || decoder.Decode(new(any)) != io.EOF || checks.SchemaVersion != 1 || len(checks.Files) == 0 || len(checks.Files) > 256 {
		return nil, 0, ErrInvalidSessionInput
	}
	return data, len(checks.Files), nil
}

// ValidateEvaluationGradeChecks performs the operator input check before proposal
// execution. It exposes no assertion contents and writes no Session facts.
func (t *TaskWorkspaces) ValidateEvaluationGradeChecks(ctx context.Context, path, pin string) error {
	_, _, err := readEvaluationChecks(ctx, path, pin, t.repository, t.storage)
	return err
}

func writeEvaluationGradeFile(root *os.Root, name string, data []byte) error {
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	return errors.Join(writeErr, file.Sync(), file.Close())
}

func (t *TaskWorkspaces) stageEvaluationGrade(ctx context.Context, operation string, frozen *sandbox.FrozenInput, checks, program []byte) (string, string, func(), error) {
	directory := filepath.Join(t.storage, "evaluation-grader-"+operation)
	releaseDirs, err := safety.PinDirectories(t.storage)
	if err != nil {
		return "", "", nil, err
	}
	defer releaseDirs()
	if err = os.Mkdir(directory, 0700); err != nil {
		return "", "", nil, err
	}
	if err = safety.ProtectPrivatePath(directory); err != nil {
		return "", "", nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", "", nil, err
	}
	defer root.Close()
	manifest, err := json.Marshal(struct {
		SchemaVersion          int                 `json:"schema_version"`
		CandidateSHA256        string              `json:"candidate_sha256"`
		CandidateArchiveSHA256 string              `json:"candidate_archive_sha256"`
		Files                  []sandbox.InputFile `json:"files"`
	}{1, frozen.SourceSHA256, frozen.ArchiveSHA256, frozen.Files})
	if err != nil {
		return "", "", nil, err
	}
	for name, data := range map[string][]byte{"candidate.json": manifest, "checks.json": checks, "grader.go": program} {
		if err = writeEvaluationGradeFile(root, name, data); err != nil {
			return "", "", nil, err
		}
	}
	sourceRelease, err := safety.PinReadOnlyFiles(frozen.Path)
	if err != nil {
		return "", "", nil, err
	}
	defer sourceRelease()
	source, err := os.Open(frozen.Path)
	if err != nil {
		return "", "", nil, err
	}
	defer source.Close()
	target, err := root.OpenFile("candidate.tar", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", "", nil, err
	}
	_, copyErr := io.Copy(target, io.LimitReader(source, 320<<20+1))
	err = errors.Join(copyErr, target.Sync(), target.Close())
	if err != nil {
		return "", "", nil, err
	}
	names := []string{"candidate.json", "candidate.tar", "checks.json", "grader.go"}
	paths := make([]string, 0, len(names))
	for _, name := range names {
		path := filepath.Join(directory, name)
		if err = safety.ProtectPrivatePath(path); err != nil {
			return "", "", nil, err
		}
		paths = append(paths, path)
	}
	release, err := safety.PinReadOnlyFiles(paths...)
	if err != nil {
		return "", "", nil, err
	}
	releaseGradeDirs, err := safety.PinDirectories(t.storage, directory)
	if err != nil {
		release()
		return "", "", nil, err
	}
	retainedPins := false
	defer func() {
		if !retainedPins {
			releaseGradeDirs()
		}
	}()
	parts := make([]evaluationGradePart, 0, len(names))
	for _, name := range names {
		file, err := root.Open(name)
		if err != nil {
			release()
			return "", "", nil, err
		}
		hash := sha256.New()
		size, err := io.Copy(hash, io.LimitReader(file, 320<<20+1))
		closeErr := file.Close()
		if err != nil || closeErr != nil || size > 320<<20 || ctx.Err() != nil {
			release()
			return "", "", nil, ErrSessionStateConflict
		}
		pin := hex.EncodeToString(hash.Sum(nil))
		if name == "candidate.tar" && pin != frozen.ArchiveSHA256 {
			release()
			return "", "", nil, ErrSessionStateConflict
		}
		parts = append(parts, evaluationGradePart{name, pin, size})
	}
	encoded, _ := json.Marshal(parts)
	retainedPins = true
	return directory, hashCodeState(string(encoded)), func() { release(); releaseGradeDirs() }, nil
}

// GradeEvaluation executes one immutable hidden grading intent. A retained intent,
// including an unknown one, is never dispatched again.
func (t *TaskWorkspaces) GradeEvaluation(ctx context.Context, command EvaluationGradeCommand) (EvaluationGradeView, error) {
	preflight := func(reason string, err error) (EvaluationGradeView, error) {
		return EvaluationGradeView{State: "blocked", Reason: reason}, err
	}
	if command.Actor.Validate() != nil || command.Actor.SessionID != command.SessionID || command.RequestID == "" || len(command.RequestID) > 128 || strings.IndexFunc(command.RequestID, unicode.IsControl) >= 0 || !evaluationGradeHash(command.ChecksSHA256) {
		return preflight("grade_command_invalid", ErrInvalidSessionInput)
	}
	snapshot, sessionView, err := t.read(ctx, command.OwnerID, command.SessionID)
	if err != nil || !t.approved(ctx, command.OwnerID, sessionView.WorkingDir) {
		return preflight("grade_session_unavailable", ErrSessionNotFound)
	}
	root, err := ModelTaskRoot(ctx, t.workbench.ledger, command.Actor)
	if err != nil || root != command.SessionID {
		return preflight("grade_actor_scope_mismatch", ErrSessionNotFound)
	}
	binding, err := ReadEvaluation(snapshot.Events)
	if err != nil || binding == nil || binding.RuntimeContract != "production_opensandbox" || binding.Lifecycle != "hold" {
		return preflight("grade_binding_mismatch", ErrSessionStateConflict)
	}
	repositoryPath, repositoryErr := filepath.Rel(t.repository, binding.SourceWorkspace)
	storagePath, storageErr := filepath.Rel(t.storage, binding.Runtime.TaskWorkspaceRoot)
	if repositoryErr != nil || storageErr != nil || repositoryPath != "." || storagePath != "." {
		return preflight("grade_binding_mismatch", ErrSessionStateConflict)
	}
	bindingBytes, _ := json.Marshal(binding)
	bindingHash := hashCodeState(string(bindingBytes))
	retained, err := readEvaluationGrade(snapshot.Events)
	if err != nil {
		return preflight("grade_evidence_invalid", err)
	}
	if retained != nil {
		if retained.OwnerID != command.OwnerID || retained.Actor.ScopeKey() != command.Actor.ScopeKey() || retained.BindingChecksum != bindingHash || retained.RequestID != command.RequestID || retained.View.ChecksSHA256 != command.ChecksSHA256 {
			return preflight("grade_request_mismatch", ErrSessionStateConflict)
		}
		result := retained.View
		result.AlreadyRecorded = true
		return result, nil
	}
	finalized, err := ReadEvaluationFinalization(snapshot.Events)
	if err != nil || finalized != nil || len(snapshot.Events) == 0 || snapshot.Events[len(snapshot.Events)-1].Checksum != command.ExpectedSessionHeadChecksum {
		return preflight("grade_session_changed", ErrSessionStateConflict)
	}
	if sessionView.Run != nil && (sessionView.Run.Status == RunQueued || sessionView.Run.Status == RunRunning || sessionView.Run.Status == RunCanceling) {
		return preflight("grade_model_run_not_completed", ErrSessionStateConflict)
	}
	checks, count, err := readEvaluationChecks(ctx, command.ChecksPath, command.ChecksSHA256, t.repository, t.storage)
	if err != nil {
		return preflight("grade_checks_unavailable", err)
	}
	prepared, state, err := taskPreparation(snapshot.Events)
	if err != nil || prepared == nil || state != "prepared" {
		return preflight("grade_candidate_unavailable", ErrSessionStateConflict)
	}
	proof, err := t.Evidence(ctx, command.OwnerID, command.SessionID)
	workspacePath, workspaceErr := filepath.Rel(binding.Workspace, proof.WorkingDir)
	if err != nil || !proof.Verified || workspaceErr != nil || workspacePath != "." || t.baselineRuntime == nil || t.baselineRuntime.Runner == nil || t.baselineRuntime.BeginProposal == nil || t.baselineRuntime.Toolchain != "go" {
		return preflight("grade_workspace_unavailable", ErrSessionStateConflict)
	}
	current, err := t.project(ctx, snapshot.Events, sessionView)
	if err != nil || !current.Available || !current.TestAvailable || current.Test != nil && !current.Test.Stopped || current.Proposal != nil && !current.Proposal.Test.Stopped || current.Application != nil {
		return preflight("grade_candidate_unavailable", ErrSessionStateConflict)
	}
	if time.Now().After(prepared.Task.LeaseExpiresAt) {
		if sessionView.Run == nil || sessionView.Run.Status != RunCompleted {
			return preflight("grade_model_run_not_completed", ErrSessionStateConflict)
		}
		if err := ModelTaskIdle(ctx, t.workbench.ledger, command.Actor); err != nil {
			return preflight("grade_candidate_unavailable", err)
		}
		_, renewed, renewalErr := t.modelWorkspace(ctx, command.OwnerID, command.SessionID, "", command.ExpectedSessionHeadChecksum)
		if renewed != nil {
			after, readErr := ReadVerifiedSnapshot(ctx, t.workbench.ledger, command.SessionID)
			if readErr != nil || len(after.Events) != len(snapshot.Events)+1 || after.Events[len(after.Events)-1].EventID != renewed.EventID || after.Events[len(after.Events)-1].Checksum != renewed.Checksum {
				return preflight("grade_session_changed", ErrSequenceConflict)
			}
			var renewal taskLeaseRenewalPayload
			if renewed.Type != taskLeaseRenewedEvent || renewed.SessionID != command.SessionID || renewed.PrevChecksum != command.ExpectedSessionHeadChecksum || json.Unmarshal(renewed.Payload, &renewal) != nil || renewal.LeaseID != prepared.Task.LeaseID || !renewal.PreviousExpiresAt.Equal(prepared.Task.LeaseExpiresAt) {
				return preflight("grade_evidence_invalid", ErrEventIntegrity)
			}
			if _, _, err := taskPreparation(after.Events); err != nil {
				return preflight("grade_evidence_invalid", err)
			}
			return EvaluationGradeView{State: "blocked", Reason: "grade_session_changed", LeaseRevalidation: &EvaluationLeaseRevalidation{
				Verified: true, BeforeHeadChecksum: command.ExpectedSessionHeadChecksum, AfterHeadChecksum: renewed.Checksum,
				EventID: renewed.EventID, EventChecksum: renewed.Checksum, LeaseID: renewal.LeaseID,
				PreviousExpiresAt: renewal.PreviousExpiresAt, ExpiresAt: renewal.ExpiresAt,
			}}, ErrSequenceConflict
		}
		if errors.Is(renewalErr, ErrSequenceConflict) {
			return preflight("grade_session_changed", ErrSequenceConflict)
		}
		if renewalErr != nil {
			return preflight("grade_workspace_unavailable", renewalErr)
		}
	}
	if !proof.AdmissionReady {
		return preflight("grade_workspace_unavailable", ErrSessionStateConflict)
	}
	if !current.ProposalAvailable {
		return preflight("grade_candidate_unavailable", ErrSessionStateConflict)
	}
	_, inputs, err := proposalFiles(prepared.Task.Baseline, snapshot.Events)
	if err != nil {
		return preflight("grade_candidate_unavailable", err)
	}
	inventory, _ := json.Marshal(inputs)
	candidateHash := hashCodeState(string(inventory))
	program, err := format.Source([]byte(evaluationGraderProgram))
	if err != nil {
		return preflight("grade_checker_unavailable", ErrSessionStateConflict)
	}
	checkerHash := hashCodeState(string(program))
	operation := hashCodeState("evaluation-grade/v2\x00" + command.SessionID + "\x00" + command.RequestID + "\x00" + candidateHash + "\x00" + command.ChecksSHA256 + "\x00" + checkerHash)
	payload := evaluationGradePayload{SchemaVersion: 2, OwnerID: command.OwnerID, Actor: command.Actor, RequestID: command.RequestID, BindingChecksum: bindingHash, SourceHeadChecksum: command.ExpectedSessionHeadChecksum, PreparedEventID: proof.PreparedEventID, PreparedEventChecksum: proof.PreparedEventChecksum, AssertionCount: count,
		View: EvaluationGradeView{State: "unknown", CandidateSHA256: candidateHash, CheckerSHA256: checkerHash, ChecksSHA256: command.ChecksSHA256, OperationID: operation}}
	intent, err := t.workbench.ledger.Append(ctx, command.SessionID, int64(len(snapshot.Events)), evaluationGradeIntentEvent, payload)
	if err != nil {
		return preflight("grade_intent_not_recorded", err)
	}
	t.workbench.signal(command.SessionID)
	payload.View.IntentEventID, payload.View.IntentEventChecksum = intent.EventID, intent.Checksum
	gradeCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	closeScope, beginErr := t.baselineRuntime.BeginProposal(gradeCtx, command.OwnerID, command.SessionID)
	knownStopped := true
	payload.View.State, payload.View.Reason = "blocked", "grader_admission_unavailable"
	if beginErr == nil {
		frozen, freezeErr := sandbox.FreezeInput(gradeCtx, proof.WorkingDir, t.storage, operation, inputs)
		if freezeErr == nil && frozen.SourceSHA256 == payload.View.CandidateSHA256 {
			payload.View.CandidateArchiveSHA256 = frozen.ArchiveSHA256
			directory, inputHash, release, stageErr := t.stageEvaluationGrade(gradeCtx, operation, frozen, checks, program)
			if stageErr == nil {
				payload.View.InputSHA256 = inputHash
				latest, headErr := ReadVerifiedSnapshot(gradeCtx, t.workbench.ledger, command.SessionID)
				if headErr == nil && latest.Events[len(latest.Events)-1].Checksum == intent.Checksum {
					result, runErr := t.baselineRuntime.Runner.Run(gradeCtx, sandbox.Request{Workspace: directory, WorkingDir: directory, OperationID: operation, Program: "go", Args: []string{"run", "grader.go"}})
					payload.View.RuntimeID, payload.View.Stopped = result.RuntimeID, result.Stopped
					payload.View.OutputSHA256 = hashCodeState(result.Output)
					if runErr == nil {
						code := result.ExitCode
						payload.View.ExitCode = &code
					}
					knownStopped = result.Stopped || (errors.Is(runErr, sandbox.ErrSandboxUnavailable) && !errors.Is(runErr, sandbox.ErrSandboxOutcomeUnknown) && result.RuntimeID == "")
					payload.View.State, payload.View.Reason = "unknown", "grader_runtime_outcome_unknown"
					if knownStopped {
						payload.View.State, payload.View.Reason = "scorer_error", "grader_output_invalid"
						switch {
						case errors.Is(runErr, sandbox.ErrSandboxOutcomeUnknown):
							payload.View.State, payload.View.Reason = "unknown", "grader_runtime_outcome_unknown"
						case errors.Is(runErr, sandbox.ErrSandboxUnavailable):
							payload.View.Reason = "grader_runtime_unavailable"
						case runErr != nil:
							payload.View.Reason = "grader_runtime_failed"
						case result.ExitCode != 0:
							payload.View.Reason = "grader_execution_failed"
						default:
							var output evaluationGradeOutput
							decoder := json.NewDecoder(strings.NewReader(result.Output))
							decoder.DisallowUnknownFields()
							if runErr == nil && result.Stopped && result.ExitCode == 0 && !result.Truncated && len(result.Output) <= 16384 && evaluationGradeRuntime(operation, result.RuntimeID) && decoder.Decode(&output) == nil && decoder.Decode(new(any)) == io.EOF && output.SchemaVersion == 1 && output.Valid && output.ErrorCode == "" && output.CandidateSHA256 == payload.View.CandidateSHA256 && output.CandidateArchiveSHA256 == payload.View.CandidateArchiveSHA256 && output.InputSHA256 == inputHash && output.CheckerSHA256 == checkerHash && output.ChecksSHA256 == command.ChecksSHA256 && output.Passed != nil && output.ChecksTotal != nil && *output.ChecksTotal == count && output.ChecksPassed != nil && *output.ChecksPassed >= 0 && *output.ChecksPassed <= count && *output.Passed == (*output.ChecksPassed == count) {
								payload.View.State, payload.View.Reason = "completed", ""
								payload.View.Passed, payload.View.ChecksTotal, payload.View.ChecksPassed = output.Passed, output.ChecksTotal, output.ChecksPassed
							}
						}
					}
				} else {
					payload.View.Reason = "session_changed_before_grade"
				}
				release()
			} else {
				payload.View.Reason = "grader_input_unavailable"
			}
		} else {
			payload.View.Reason = "candidate_freeze_unavailable"
		}
	}
	if knownStopped {
		payload.View.Stopped = true
		if closeScope != nil && closeScope() != nil {
			payload.View.State, payload.View.Reason = "unknown", "grader_scope_stop_unconfirmed"
			payload.View.Passed, payload.View.ChecksTotal, payload.View.ChecksPassed = nil, nil, nil
		}
	}
	recordCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer stop()
	unrecorded := func() EvaluationGradeView {
		view := payload.View
		view.State, view.Reason, view.Stopped = "unknown", "grader_result_not_recorded", false
		view.Passed, view.ChecksTotal, view.ChecksPassed, view.ExitCode = nil, nil, nil, nil
		return view
	}
	for attempt := 0; attempt < 16; attempt++ {
		latest, readErr := ReadVerifiedSnapshot(recordCtx, t.workbench.ledger, command.SessionID)
		if readErr != nil {
			return unrecorded(), readErr
		}
		if latest.Events[len(latest.Events)-1].Checksum != intent.Checksum {
			payload.View.State, payload.View.Reason = "unknown", "session_changed_during_grade"
			payload.View.Passed, payload.View.ChecksTotal, payload.View.ChecksPassed = nil, nil, nil
		}
		result, appendErr := t.workbench.ledger.Append(recordCtx, command.SessionID, int64(len(latest.Events)), evaluationGradeResultEvent, payload)
		if appendErr == nil {
			t.workbench.signal(command.SessionID)
			confirmed, readErr := ReadVerifiedSnapshot(recordCtx, t.workbench.ledger, command.SessionID)
			if readErr != nil {
				return unrecorded(), readErr
			}
			view, readErr := ReadEvaluationGrade(confirmed.Events)
			if readErr != nil || view == nil || view.ResultEventID != result.EventID || view.ResultEventChecksum != result.Checksum {
				return unrecorded(), ErrEventIntegrity
			}
			return *view, nil
		}
		if !errors.Is(appendErr, ErrSequenceConflict) {
			return unrecorded(), appendErr
		}
	}
	return unrecorded(), ErrSequenceConflict
}
