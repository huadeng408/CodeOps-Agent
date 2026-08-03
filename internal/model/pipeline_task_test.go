package model

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPipelineTaskRunIDJSONRoundTrip(t *testing.T) {
	task := PipelineTask{
		FileMD5: "796c9a98",
		Stage:   "chunk",
		RunID:   "run-replay-1754000000000000000",
	}
	if task.RunID == "" {
		t.Fatal("PipelineTask must expose a RunID field")
	}
	b, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded["runId"] != task.RunID {
		t.Fatalf("RunID must serialize under json key \"runId\", got %v", decoded["runId"])
	}
}

func TestPipelineTaskRunIDGormTagIsVarChar96(t *testing.T) {
	field, ok := reflect.TypeOf(PipelineTask{}).FieldByName("RunID")
	if !ok {
		t.Fatal("PipelineTask must have a RunID field")
	}
	tag := string(field.Tag)
	if !strings.Contains(tag, "type:varchar(96)") {
		t.Fatalf("RunID gorm tag must constrain to varchar(96) so it fits the existing idempotency_key column family, got %q", tag)
	}
}
