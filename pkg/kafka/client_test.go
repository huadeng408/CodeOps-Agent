package kafka

import (
	"testing"

	"code-agent/internal/model"
	"code-agent/pkg/tasks"
)

// TestShouldSkipByStatus keeps controlled runs replayable across RunIDs while
// discarding duplicate delivery after a stage in the same run already succeeds.
func TestShouldSkipByStatus(t *testing.T) {
	cases := []struct {
		name     string
		previous *model.PipelineTask
		hasRunID bool
		want     bool
	}{
		{"legacy SUCCESS skips", &model.PipelineTask{Status: model.PipelineStatusSuccess}, false, true},
		{"run SUCCESS skips duplicate delivery", &model.PipelineTask{Status: model.PipelineStatusSuccess}, true, true},
		{"legacy PROCESSING does not skip", &model.PipelineTask{Status: model.PipelineStatusProcessing}, false, false},
		{"run PROCESSING does not skip", &model.PipelineTask{Status: model.PipelineStatusProcessing}, true, false},
		{"legacy FAILED does not skip", &model.PipelineTask{Status: model.PipelineStatusFailed}, false, false},
		{"run FAILED does not skip", &model.PipelineTask{Status: model.PipelineStatusFailed}, true, false},
		{"legacy PENDING does not skip", &model.PipelineTask{Status: model.PipelineStatusPending}, false, false},
		{"run PENDING does not skip", &model.PipelineTask{Status: model.PipelineStatusPending}, true, false},
		{"nil previous never skips (legacy)", nil, false, false},
		{"nil previous never skips (run)", nil, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldSkipByStatus(tc.previous, tc.hasRunID); got != tc.want {
				t.Fatalf("shouldSkipByStatus(%+v, hasRunID=%v) = %v, want %v", tc.previous, tc.hasRunID, got, tc.want)
			}
		})
	}
}

func TestTaskMessageKeyIsStableAndRunScoped(t *testing.T) {
	task := tasks.FileProcessingTask{FileMD5: "md5", Stage: tasks.StageChunk, TaskChunkID: 7, RunID: "run-1"}
	if got := taskMessageKey(task); got != "run-1:md5:chunk:7" {
		t.Fatalf("taskMessageKey=%q want run-1:md5:chunk:7", got)
	}
	if got := taskMessageKey(task); got != taskMessageKey(task) {
		t.Fatalf("taskMessageKey must be deterministic, got %q then %q", got, taskMessageKey(task))
	}
	other := task
	other.RunID = "run-2"
	if taskMessageKey(other) == taskMessageKey(task) {
		t.Fatal("different run IDs must not share the same Kafka partition key")
	}
}

func TestTaskMessageKeyUsesLegacyNamespaceWithoutRunID(t *testing.T) {
	task := tasks.FileProcessingTask{FileMD5: "md5", Stage: tasks.StageParse}
	if got := taskMessageKey(task); got != "md5:parse:-1" {
		t.Fatalf("taskMessageKey=%q want md5:parse:-1", got)
	}
}
