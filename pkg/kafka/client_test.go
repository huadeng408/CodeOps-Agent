package kafka

import (
	"testing"

	"code-agent/internal/model"
)

// TestShouldSkipByStatus pins the R1 load-bearing decision: a message without
// a RunID keeps legacy semantics (skip on prior SUCCESS), while a message with
// a RunID ALWAYS executes — controlled replays must never be skipped by a stale
// SUCCESS from a prior run.
func TestShouldSkipByStatus(t *testing.T) {
	cases := []struct {
		name     string
		previous *model.PipelineTask
		hasRunID bool
		want     bool
	}{
		{"legacy SUCCESS skips", &model.PipelineTask{Status: model.PipelineStatusSuccess}, false, true},
		{"run SUCCESS does NOT skip", &model.PipelineTask{Status: model.PipelineStatusSuccess}, true, false},
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
