package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"code-agent/internal/session"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestProductionLocalCoreExportsWorkspacePreparationTrace(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-first product preparation")
	}
	if os.Getenv("CODE_AGENT_RUN_LOCAL_CORE_E2E") != "1" {
		t.Skip("set CODE_AGENT_RUN_LOCAL_CORE_E2E=1 for the production HTTP entry")
	}
	t.Setenv("otel_exporter_otlp_endpoint", "http://unapproved.invalid")
	t.Setenv("otEl_Exporter_Otlp_Headers", "fixture-unapproved-header")
	spans := make(chan *tracepb.Span, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var request collector.ExportTraceServiceRequest
		if err != nil || r.URL.Path != "/v1/traces" || proto.Unmarshal(data, &request) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.Name == "workspace.prepare" {
						select {
						case spans <- span:
						default:
						}
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer receiver.Close()
	fixture := startProductionLocalCore(t)
	fixture.stop(t)
	repository, storage := t.TempDir(), t.TempDir()
	seedSpawnGitRepo(t, repository)
	config, err := os.ReadFile(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	paths, _ := json.Marshal(filepath.ToSlash(repository))
	store, _ := json.Marshal(filepath.ToSlash(storage))
	config = bytes.Replace(config, []byte("harness:\n"), []byte("harness:\n  repository_root: "+string(paths)+"\n  task_workspace_root: "+string(store)+"\n"), 1)
	if err := os.WriteFile(fixture.config, config, 0600); err != nil {
		t.Fatal(err)
	}
	for _, setting := range fixture.environment {
		if strings.HasPrefix(strings.ToUpper(setting), "OTEL_") {
			t.Fatal("trace fixture inherited an external exporter configuration")
		}
	}
	fixture.environment = append(fixture.environment, "OTEL_EXPORTER_OTLP_ENDPOINT="+receiver.URL, "OTEL_BSP_SCHEDULE_DELAY=100")
	fixture.start(t)
	fixture.authenticate(t)
	body, _ := json.Marshal(map[string]string{"projectName": "trace-repo", "title": "Prepare trace", "workingDir": repository})
	response, err := fixture.client.Post(fixture.base+"/api/v1/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var created struct{ Data session.SessionView }
	err = json.NewDecoder(response.Body).Decode(&created)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || created.Data.ID == "" {
		t.Fatal("owned Session was not created")
	}
	response, err = fixture.client.Post(fixture.base+"/api/v1/sessions/"+created.Data.ID+"/task-workspace/prepare", "application/json", strings.NewReader(`{"expectedSeq":1,"requestId":"trace-prepare"}`))
	if err != nil {
		t.Fatal(err)
	}
	var prepared struct{ Data session.TaskWorkspaceView }
	err = json.NewDecoder(response.Body).Decode(&prepared)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || prepared.Data.State != "prepared" {
		t.Fatal("approved preparation failed")
	}
	select {
	case span := <-spans:
		attributes := map[string]string{}
		for _, attribute := range span.Attributes {
			attributes[attribute.Key] = attribute.Value.GetStringValue()
		}
		if len(span.TraceId) != 16 || len(span.SpanId) != 8 || span.EndTimeUnixNano <= span.StartTimeUnixNano || attributes["session.id"] != created.Data.ID || attributes["workspace.lease_id"] != prepared.Data.WorkspaceID || attributes["workspace.baseline"] != prepared.Data.Baseline.Checksum {
			t.Fatal("exported preparation trace lacks attributable completed work")
		}
		encoded, _ := proto.Marshal(span)
		if bytes.Contains(encoded, []byte(fixture.key)) || bytes.Contains(encoded, []byte(fixture.password)) {
			t.Fatal("trace contains authentication credentials")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("production local core did not export workspace.prepare")
	}
}
