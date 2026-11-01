package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type EvaluationMemoryRead struct {
	SchemaVersion    int    `json:"schema_version"`
	RunID            string `json:"run_id"`
	ReadKind         string `json:"read_kind"`
	ToolName         string `json:"tool_name"`
	ToolCallID       string `json:"tool_call_id,omitempty"`
	SourceRefsSHA256 string `json:"source_refs_sha256"`
	ReturnedBytes    int    `json:"returned_bytes"`
	PayloadSHA256    string `json:"payload_sha256"`
	ReturnedEntries  *int   `json:"returned_entries"`
	Succeeded        bool   `json:"succeeded"`
}

type EvaluationMemoryEvidence struct {
	ReadCount          int                    `json:"read_count"`
	AutomaticReadCount int                    `json:"automatic_read_count"`
	ExplicitReadCount  int                    `json:"explicit_read_count"`
	ReturnedBytes      int                    `json:"returned_bytes"`
	ReturnedEntries    int                    `json:"returned_entries"`
	UnknownEntryCounts int                    `json:"unknown_entry_counts"`
	InjectedCount      int                    `json:"injected_count"`
	InjectedBytes      int                    `json:"injected_bytes"`
	InjectedEntries    int                    `json:"injected_entries"`
	Reads              []EvaluationMemoryRead `json:"reads"`
	Injections         []EvaluationMemoryRead `json:"injections"`
}

func evaluationMemoryRefsHash(binding *EvaluationBinding) string {
	refs := binding.Policy.FrozenMemory
	if refs == nil {
		refs = []FrozenMemoryReference{}
	}
	data, _ := json.Marshal(refs)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func memoryReturnedEntries(payload string) *int {
	count := 0
	if payload == "" {
		return &count
	}
	var value any
	if json.Unmarshal([]byte(payload), &value) != nil {
		return nil
	}
	switch data := value.(type) {
	case nil:
		count = 0
	case []any:
		count = len(data)
	case map[string]any:
		if entries, exists := data["entries"]; exists && entries == nil {
			count = 0
		} else if entries, ok := data["entries"].([]any); ok {
			count = len(entries)
		} else if id, ok := data["id"].(string); ok && id != "" {
			count = 1
		} else {
			return nil
		}
	default:
		return nil
	}
	return &count
}

func (r *SessionRunner) recordEvaluationMemory(ctx context.Context, key runKey, lease runLeasePayload, binding *EvaluationBinding, readKind, tool, callID, payload string, succeeded, injected bool) error {
	if binding == nil {
		return nil
	}
	if !binding.Policy.MemoryRead || readKind != "automatic" && readKind != "explicit" {
		return ErrSessionStateConflict
	}
	hash := sha256.Sum256([]byte(payload))
	fact := EvaluationMemoryRead{SchemaVersion: 1, RunID: key.runID, ReadKind: readKind, ToolName: tool, ToolCallID: callID,
		SourceRefsSHA256: evaluationMemoryRefsHash(binding), ReturnedBytes: len(payload), PayloadSHA256: hex.EncodeToString(hash[:]), ReturnedEntries: memoryReturnedEntries(payload), Succeeded: succeeded}
	typ := "evaluation/memory-read"
	if injected {
		typ = "evaluation/memory-injected"
	}
	return r.appendLeasedFact(ctx, key, lease.LeaseID, typ, fact)
}

func (r *SessionRunner) recordEvaluationMemoryInjection(ctx context.Context, key runKey, lease runLeasePayload, payload string) error {
	if payload == "" {
		return nil
	}
	events, err := r.workbench.ledger.Events(ctx, key.sessionID)
	if err != nil {
		return err
	}
	binding, err := r.evaluationBinding(ctx, events)
	if err != nil {
		return err
	}
	return r.recordEvaluationMemory(ctx, key, lease, binding, "automatic", "RecallMemory", "", payload, true, true)
}
