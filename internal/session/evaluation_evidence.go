package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	"code-agent/internal/identity"
)

type EvaluationToolEvidence struct {
	RuntimeID           string `json:"runtime_id"`
	Stopped             bool   `json:"stopped"`
	OutcomeUnknown      bool   `json:"outcome_unknown"`
	IsError             bool   `json:"is_error"`
	ResultEventID       string `json:"result_event_id"`
	ResultEventChecksum string `json:"result_event_checksum"`
	SessionID           string `json:"session_id"`
	RunID               string `json:"run_id"`
	ToolCallID          string `json:"tool_call_id"`
	ToolName            string `json:"tool_name"`
	DispatchCount       int    `json:"dispatch_count"`
	ResultCount         int    `json:"result_count"`
	UnknownCount        int    `json:"unknown_count"`
	CommitDoneCount     int    `json:"commit_done_count"`
}

type EvaluationVerification struct {
	SessionID     string `json:"session_id"`
	ToolCallID    string `json:"tool_call_id"`
	CommandSHA256 string `json:"command_sha256"`
	ExitCode      int32  `json:"exit_code"`
}

type EvaluationExecutionEvidence struct {
	RetryEvidence            *EvaluationRetryEvidence  `json:"-"`
	Verified                 bool                      `json:"verified"`
	ChildSessionIDs          []string                  `json:"child_session_ids"`
	RootCompactionCount      int                       `json:"root_compaction_count"`
	CompactionCount          int                       `json:"compaction_count"`
	FaultReadyCount          int                       `json:"fault_ready_count"`
	FaultBoundaries          []EvaluationFaultEvidence `json:"fault_boundaries"`
	ToolCallCount            int                       `json:"tool_call_count"`
	ToolDispatchCount        int                       `json:"tool_dispatch_count"`
	ToolResultCount          int                       `json:"tool_result_count"`
	ToolUnknownCount         int                       `json:"tool_unknown_count"`
	Tools                    []EvaluationToolEvidence  `json:"tools"`
	VisibleVerificationCount *int                      `json:"visible_verification_count"`
	VisibleVerifications     []EvaluationVerification  `json:"visible_verifications"`
	PhysicalExecutionCount   *int                      `json:"physical_execution_count"`
	DuplicateEffects         *int                      `json:"duplicate_effects"`
	MemoryReads              EvaluationMemoryEvidence  `json:"memory_reads"`
}

// ReadEvaluationExecutionEvidence counts verified Go facts in the authorized
// task tree. Tool arguments and result bodies never enter receipts.
func ReadEvaluationExecutionEvidence(ctx context.Context, ledger EventLog, actor identity.Actor, commands []string) (EvaluationExecutionEvidence, error) {
	evidence := EvaluationExecutionEvidence{ChildSessionIDs: []string{}, Tools: []EvaluationToolEvidence{}, VisibleVerifications: []EvaluationVerification{}, FaultBoundaries: []EvaluationFaultEvidence{}}
	evidence.MemoryReads.Reads, evidence.MemoryReads.Injections = []EvaluationMemoryRead{}, []EvaluationMemoryRead{}
	if len(commands) > 0 {
		zero := 0
		evidence.VisibleVerificationCount = &zero
	}
	root, err := ModelTaskRoot(ctx, ledger, actor)
	if err != nil || root != actor.SessionID {
		return evidence, ErrSessionNotFound
	}
	seen := map[string]bool{}
	var rootOwner uint
	sourceRefsHash := ""
	var visit func(string) error
	visit = func(id string) error {
		if seen[id] || len(seen) >= 128 {
			return ErrEventIntegrity
		}
		seen[id] = true
		snapshot, err := ReadVerifiedSnapshot(ctx, ledger, id)
		if err != nil {
			return err
		}
		current := actor
		current.SessionID = id
		owner, _, err := agentParent(snapshot.Events, current)
		if err != nil || rootOwner != 0 && owner != rootOwner {
			return ErrSessionNotFound
		}
		rootOwner = owner
		faults, err := readEvaluationFaultEvidence(snapshot.Events)
		if err != nil {
			return err
		}
		evidence.FaultBoundaries = append(evidence.FaultBoundaries, faults...)
		if id == root {
			binding, err := ReadEvaluation(snapshot.Events)
			if err != nil {
				return err
			}
			if binding != nil {
				sourceRefsHash = evaluationMemoryRefsHash(binding)
				retry, retryErr := projectEvaluationRetry(binding, snapshot.Events)
				if retryErr != nil {
					return retryErr
				}
				if retry != nil {
					evidence.RetryEvidence = &retry.evidence
				}
			}
		}
		observations := map[string]*EvaluationToolEvidence{}
		prepared := map[string]toolCallPayload{}
		results := map[string]toolResultPayload{}
		for _, event := range snapshot.Events {
			switch event.Type {
			case "evaluation/memory-read", "evaluation/memory-injected":
				var fact EvaluationMemoryRead
				if json.Unmarshal(event.Payload, &fact) != nil || fact.SchemaVersion != 1 || fact.RunID == "" || fact.ReadKind != "automatic" && fact.ReadKind != "explicit" || fact.ReturnedBytes < 0 || len(fact.PayloadSHA256) != 64 || fact.SourceRefsSHA256 != sourceRefsHash || fact.ReturnedEntries != nil && *fact.ReturnedEntries < 0 {
					return ErrEventIntegrity
				}
				if event.Type == "evaluation/memory-injected" {
					if fact.ReadKind != "automatic" || !fact.Succeeded {
						return ErrEventIntegrity
					}
					evidence.MemoryReads.InjectedCount++
					evidence.MemoryReads.InjectedBytes += fact.ReturnedBytes
					if fact.ReturnedEntries != nil {
						evidence.MemoryReads.InjectedEntries += *fact.ReturnedEntries
					}
					evidence.MemoryReads.Injections = append(evidence.MemoryReads.Injections, fact)
				} else {
					evidence.MemoryReads.ReadCount++
					evidence.MemoryReads.ReturnedBytes += fact.ReturnedBytes
					if fact.ReadKind == "automatic" {
						evidence.MemoryReads.AutomaticReadCount++
					} else {
						evidence.MemoryReads.ExplicitReadCount++
					}
					if fact.ReturnedEntries == nil {
						evidence.MemoryReads.UnknownEntryCounts++
					} else {
						evidence.MemoryReads.ReturnedEntries += *fact.ReturnedEntries
					}
					evidence.MemoryReads.Reads = append(evidence.MemoryReads.Reads, fact)
				}
			case compactionEventType:
				evidence.CompactionCount++
				if id == root {
					evidence.RootCompactionCount++
				}
			case "evaluation/fault-ready":
				evidence.FaultReadyCount++
			case "tool/call", toolDispatchedType, "tool/result", toolUnknownEventType, commitDoneEventType:
				var fact struct {
					RunID      string `json:"run_id"`
					ToolCallID string `json:"tool_call_id"`
					ToolName   string `json:"tool_name"`
				}
				if json.Unmarshal(event.Payload, &fact) != nil || fact.RunID == "" || fact.ToolCallID == "" {
					return ErrEventIntegrity
				}
				key := invocationHistoryKey(fact.RunID, fact.ToolCallID)
				item := observations[key]
				if item == nil {
					item = &EvaluationToolEvidence{SessionID: id, RunID: fact.RunID, ToolCallID: fact.ToolCallID}
					observations[key] = item
				}
				if fact.ToolName != "" {
					item.ToolName = fact.ToolName
				}
				switch event.Type {
				case "tool/call":
					evidence.ToolCallCount++
					var call toolCallPayload
					if json.Unmarshal(event.Payload, &call) != nil {
						return ErrEventIntegrity
					}
					prepared[key] = call
				case toolDispatchedType:
					item.DispatchCount++
					evidence.ToolDispatchCount++
				case toolUnknownEventType:
					item.UnknownCount++
					evidence.ToolUnknownCount++
				case commitDoneEventType:
					item.CommitDoneCount++
				case "tool/result":
					item.ResultCount++
					evidence.ToolResultCount++
					var result toolResultPayload
					if json.Unmarshal(event.Payload, &result) != nil {
						return ErrEventIntegrity
					}
					results[key] = result
					item.RuntimeID, item.Stopped, item.OutcomeUnknown = result.RuntimeID, result.Stopped, result.OutcomeUnknown
					item.IsError = result.Error != ""
					item.ResultEventID, item.ResultEventChecksum = event.EventID, event.Checksum
				}
			}
		}
		keys := make([]string, 0, len(observations))
		for key := range observations {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			item := *observations[key]
			evidence.Tools = append(evidence.Tools, item)
			call, result := prepared[key], results[key]
			if item.ToolName != "Bash" || item.DispatchCount == 0 || item.ResultCount == 0 || result.OutcomeUnknown || result.RuntimeID == "" || !result.Stopped {
				continue
			}
			var parameters struct {
				Command string `json:"command"`
			}
			if json.Unmarshal([]byte(call.ArgumentsJSON), &parameters) != nil {
				continue
			}
			for _, declared := range commands {
				if declared != parameters.Command {
					continue
				}
				*evidence.VisibleVerificationCount++
				hash := sha256.Sum256([]byte(declared))
				evidence.VisibleVerifications = append(evidence.VisibleVerifications, EvaluationVerification{id, item.ToolCallID, hex.EncodeToString(hash[:]), result.ExitCode})
				break
			}
		}
		for _, event := range snapshot.Events {
			if event.Type != "agent/task-linked" {
				continue
			}
			var link agentTaskLink
			if json.Unmarshal(event.Payload, &link) != nil {
				return ErrEventIntegrity
			}
			task, created, err := readAgentTask(ctx, ledger, owner, link.TaskID, id)
			childActor := current
			if task != nil {
				childActor.SessionID = task.ChildSessionId
			}
			if err != nil || task.ChildSessionId != link.ChildSessionID || created.Signature != link.Signature || created.Actor.ScopeKey() != childActor.ScopeKey() {
				return ErrEventIntegrity
			}
			evidence.ChildSessionIDs = append(evidence.ChildSessionIDs, task.ChildSessionId)
			if err := visit(task.ChildSessionId); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(root); err != nil {
		return evidence, err
	}
	sort.Strings(evidence.ChildSessionIDs)
	evidence.Verified = true
	return evidence, nil
}
