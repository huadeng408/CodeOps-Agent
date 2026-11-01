package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TaskWorkspaceEvidence identifies a retained Go preparation. Verified binds
// identity and path; AdmissionReady additionally requires the current tree,
// tool outcomes, lease and selected runtime to be admissible.
type TaskWorkspaceEvidence struct {
	ApprovedRoot          string    `json:"approved_root"`
	WorkingDir            string    `json:"working_dir"`
	WorkspaceID           string    `json:"workspace_id"`
	LeaseID               string    `json:"lease_id"`
	BaselineChecksum      string    `json:"baseline_checksum"`
	LeaseExpiresAt        time.Time `json:"lease_expires_at"`
	Verified              bool      `json:"verified"`
	AdmissionReady        bool      `json:"admission_ready"`
	PreparedEventID       string    `json:"prepared_event_id"`
	PreparedEventChecksum string    `json:"prepared_event_checksum"`
}

func (t *TaskWorkspaces) Evidence(ctx context.Context, owner uint, id string) (TaskWorkspaceEvidence, error) {
	snapshot, view, err := t.read(ctx, owner, id)
	if err != nil || !t.approved(ctx, owner, view.WorkingDir) {
		return TaskWorkspaceEvidence{}, ErrSessionNotFound
	}
	prepared, state, err := taskPreparation(snapshot.Events)
	if err != nil || prepared == nil || state != "prepared" {
		return TaskWorkspaceEvidence{}, ErrSessionStateConflict
	}
	actual := filepath.Join(t.storage, prepared.Task.LeaseID)
	resolved, err := filepath.EvalSymlinks(actual)
	if err != nil || !strings.EqualFold(filepath.Clean(resolved), filepath.Clean(actual)) {
		return TaskWorkspaceEvidence{}, ErrSessionStateConflict
	}
	if info, err := os.Stat(actual); err != nil || !info.IsDir() {
		return TaskWorkspaceEvidence{}, ErrSessionStateConflict
	}
	projected, err := t.project(ctx, snapshot.Events, view)
	if err != nil {
		return TaskWorkspaceEvidence{}, err
	}
	grade, err := ReadEvaluationGrade(snapshot.Events)
	if err != nil {
		return TaskWorkspaceEvidence{}, err
	}
	proof := TaskWorkspaceEvidence{ApprovedRoot: t.storage, WorkingDir: actual, WorkspaceID: prepared.Task.LeaseID, LeaseID: prepared.Task.LeaseID, BaselineChecksum: prepared.Task.Baseline.Checksum, LeaseExpiresAt: prepared.Task.LeaseExpiresAt, Verified: true,
		AdmissionReady: grade == nil && projected.State == "prepared" && projected.TestAvailable && taskToolOutcomesKnown(snapshot.Events, "") == nil && t.borrowedAgentsAdmissible(ctx, owner, id, snapshot.Events, "") == nil}
	for _, event := range snapshot.Events {
		if event.Type == taskPreparedEvent {
			proof.PreparedEventID, proof.PreparedEventChecksum = event.EventID, event.Checksum
		}
	}
	if proof.PreparedEventID == "" {
		return TaskWorkspaceEvidence{}, ErrEventIntegrity
	}
	return proof, nil
}
