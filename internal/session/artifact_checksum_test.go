package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestModelTaskRootArtifactChecksumsAcrossBinaryFormats(t *testing.T) {
	const compact = `{"id":"fixture-artifact","name":"Report, \"quoted\"","parts":[{"text":"literal, \"quoted\", slash\\path"},{"data_json":"{\"left\":1,\"right\":2}"}]}`
	const spaced = `{"id":"fixture-artifact", "name":"Report, \"quoted\"", "parts":[{"text":"literal, \"quoted\", slash\\path"}, {"data_json":"{\"left\":1,\"right\":2}"}]}`
	for _, tc := range []struct {
		name       string
		body       string
		tamper     bool
		wantAccept bool
	}{
		{name: "compact", body: compact, wantAccept: true},
		{name: "legacy_structural_spaces", body: spaced, wantAccept: true},
		{name: "tampered_content", body: compact, tamper: true},
		{name: "changed_string_commas", body: strings.ReplaceAll(compact, ",", ", ")},
		{name: "unrecognized_whitespace", body: " " + compact},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			runner, parent, actor := agentTestRunner(t, &independentAgentFixture{})
			child := agentTestCall(t, runner, actor, "artifact-format-child", "SpawnAgent", `{"kind":"explore","title":"inspect","objective":"verify generic artifact"}`)
			if err := runner.Close(); err != nil {
				t.Fatal(err)
			}
			childActor := actor
			childActor.SessionID = ""
			childActor, err := childActor.BindSession(child.ChildSessionId)
			if err != nil {
				t.Fatal(err)
			}
			artifact := &pb.AgentArtifact{}
			if err := protojson.Unmarshal([]byte(compact), artifact); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256([]byte(tc.body))
			artifact.Checksum = hex.EncodeToString(digest[:])
			if tc.tamper {
				artifact.Parts[0] = &pb.AgentPart{Payload: &pb.AgentPart_Text{Text: "changed content"}}
			}
			encoded, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(artifact)
			if err != nil {
				t.Fatal(err)
			}
			ledger := runner.workbench.ledger
			before, err := ReadVerifiedSnapshot(ctx, ledger, child.ChildSessionId)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.Append(ctx, child.ChildSessionId, int64(len(before.Events)), "agent/task-artifact", agentTaskMutation{
				RequestID: artifact.Id, Artifact: encoded,
			}); err != nil {
				t.Fatal(err)
			}
			before, err = ReadVerifiedSnapshot(ctx, ledger, child.ChildSessionId)
			if err != nil {
				t.Fatalf("canonical chain or Surface fixture failed: %v", err)
			}
			root, err := ModelTaskRoot(ctx, ledger, childActor)
			if tc.wantAccept {
				if err != nil || root != parent.ID {
					t.Errorf("artifact format prevented authenticated root recovery: root=%q error=%v", root, err)
				}
				reader := NewSessionRunner(NewWorkbench(ledger, nil), nil, nil, SessionRunnerOptions{WorkerCount: 1, AgentWorkerCount: 1})
				defer reader.Close()
				replayed := reader.ExecuteAgentTool(ctx, childActor, child.ChildSessionId, orchestrator.ToolCall{
					ID: artifact.Id, Name: "PublishArtifact", ParametersJSON: `{"artifact":` + compact + `}`,
				})
				retained := &pb.AgentArtifact{}
				if replayed.Error != "" || protojson.Unmarshal([]byte(replayed.Output), retained) != nil || retained.Checksum != artifact.Checksum {
					t.Errorf("artifact replay changed the retained checksum: error=%q", replayed.Error)
				}
				conflict := reader.ExecuteAgentTool(ctx, childActor, child.ChildSessionId, orchestrator.ToolCall{
					ID: artifact.Id, Name: "PublishArtifact", ParametersJSON: `{"artifact":{"name":"changed","parts":[{"text":"changed content"}]}}`,
				})
				if conflict.Error == "" {
					t.Error("same artifact ID accepted different content")
				}
			} else if !errors.Is(err, ErrEventIntegrity) {
				t.Errorf("invalid artifact checksum was accepted: root=%q error=%v", root, err)
			}
			after, err := ReadVerifiedSnapshot(ctx, ledger, child.ChildSessionId)
			if err != nil || !reflect.DeepEqual(before.Events, after.Events) {
				t.Fatalf("artifact validation changed the canonical prefix: %v", err)
			}
		})
	}
}

func TestIndependentAgentArtifactPublicationUsesStableChecksum(t *testing.T) {
	runner, _, actor := agentTestRunner(t, &independentAgentFixture{})
	child := agentTestCall(t, runner, actor, "stable-artifact-child", "SpawnAgent", `{"kind":"explore","title":"inspect","objective":"publish generic artifact"}`)
	childActor := actor
	childActor.SessionID = ""
	childActor, err := childActor.BindSession(child.ChildSessionId)
	if err != nil {
		t.Fatal(err)
	}
	result := runner.ExecuteAgentTool(context.Background(), childActor, child.ChildSessionId, orchestrator.ToolCall{
		ID: "stable-publish", Name: "PublishArtifact", ParametersJSON: `{"artifact":{"name":"Report","parts":[{"text":"stable artifact"}]}}`,
	})
	artifact := &pb.AgentArtifact{}
	if result.Error != "" || protojson.Unmarshal([]byte(result.Output), artifact) != nil {
		t.Fatalf("public artifact publication failed: %s", result.Error)
	}
	// SHA-256 of the independent literal {"id":"stable-publish","name":"Report","parts":[{"text":"stable artifact"}]}.
	if artifact.Checksum != "4e8bf2833c13898761866bf1e8c1d7f84a181170fb036b1477dc0bd9313053e9" {
		t.Fatalf("artifact producer emitted an unstable checksum: %s", artifact.Checksum)
	}
}
