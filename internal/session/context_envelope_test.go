package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
)

func TestBuildContextEnvelopeProducesMetadataOnlyLayers(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "first line\nsecond line\n"
	if err := os.WriteFile(filepath.Join(root, "src", "example.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PRIVATE_CONTEXT_VALUE=fixture-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	events := []Event{
		{Seq: 0, Type: userMessageEventType, Checksum: strings.Repeat("a", 64), Payload: json.RawMessage(`{"content":"PRIVATE_CONTEXT_VALUE"}`)},
		{Seq: 1, Type: codeModifiedEventType, Checksum: strings.Repeat("b", 64), Payload: json.RawMessage(`{"path":"src/example.txt","operation":"Write"}`)},
		{Seq: 2, Type: "tool/result", Checksum: strings.Repeat("c", 64), Payload: json.RawMessage(`{"tool_name":"Read","output":"fixture-secret","exit_code":0}`)},
	}

	envelope, err := buildContextEnvelope(root, events)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.SchemaVersion != contextEnvelopeVersion || envelope.LedgerSeq != 2 || envelope.LedgerChecksum != strings.Repeat("c", 64) {
		t.Fatalf("envelope identity = %+v", envelope)
	}
	foundP0 := false
	for _, item := range envelope.P0 {
		if item.Path == ".env" {
			t.Fatal("sensitive filename entered P0")
		}
		foundP0 = foundP0 || item.Path == "src/example.txt"
	}
	if !foundP0 || len(envelope.P1) != 1 || envelope.P1[0].Path != "src/example.txt" || envelope.P1[0].LineCount != 2 || len(envelope.P1[0].Sha256) != 64 {
		t.Fatalf("layered file metadata = %+v", envelope)
	}
	if len(envelope.P3Candidates) != 1 || envelope.P3Candidates[0] != "src/example.txt" {
		t.Fatalf("P3 candidates = %v", envelope.P3Candidates)
	}
	raw, err := protojson.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), content) || strings.Contains(string(raw), "fixture-secret") || strings.Contains(string(raw), "PRIVATE_CONTEXT_VALUE") {
		t.Fatalf("context envelope leaked raw content: %s", raw)
	}
}
