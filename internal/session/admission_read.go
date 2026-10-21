package session

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// Retirement preserves the lost outcome; it never authorizes replay or completion.
func canceledPlainFileRead(events []Event, call toolCallPayload) bool {
	if call.ToolName != "Read" || call.MCPBinding != nil {
		return false
	}
	run, err := projectRun(events, call.RunID)
	if err != nil || run.cancelRequest == nil {
		return false
	}
	var arguments map[string]any
	if json.Unmarshal([]byte(call.ArgumentsJSON), &arguments) != nil {
		return false
	}
	var target string
	for _, key := range []string{"path", "file"} {
		if value, ok := arguments[key].(string); ok {
			target = value
			break
		}
	}
	// PDF Read launches MinerU; ordinary file reads do not create a runtime.
	if target == "" || strings.EqualFold(filepath.Ext(filepath.Clean(strings.ReplaceAll(target, "\\", "/"))), ".pdf") {
		return false
	}
	facts := map[string]Event{}
	var operationID string
	var cancelSeq int64
	for _, event := range events {
		if event.Type == runCancelRequestedEventType {
			var cancellation runCancelPayload
			if json.Unmarshal(event.Payload, &cancellation) != nil {
				return false
			}
			if cancellation.RunID == call.RunID {
				if cancelSeq != 0 {
					return false
				}
				cancelSeq = event.Seq
			}
			continue
		}
		switch event.Type {
		case "tool/call", commitPendingEventType, toolDispatchedType, toolUnknownEventType, commitDoneEventType, "tool/result", codeModifiedEventType:
		default:
			continue
		}
		var payload struct {
			toolCallPayload
			RuntimeID string `json:"runtime_id"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil {
			return false
		}
		if payload.RunID != call.RunID || payload.ToolCallID != call.ToolCallID {
			continue
		}
		if event.Type == "tool/result" || event.Type == codeModifiedEventType ||
			payload.ToolName != call.ToolName || payload.ArgumentsJSON != call.ArgumentsJSON ||
			payload.MCPBinding != nil || payload.RuntimeID != "" || facts[event.Type].EventID != "" {
			return false
		}
		if event.Type == "tool/call" {
			operationID = payload.OperationID
		}
		if event.Type != commitPendingEventType && event.Type != commitDoneEventType && payload.OperationID != operationID {
			return false
		}
		facts[event.Type] = event
	}
	prepared, pending, dispatched, unknown, done := facts["tool/call"], facts[commitPendingEventType], facts[toolDispatchedType], facts[toolUnknownEventType], facts[commitDoneEventType]
	if prepared.SurfaceOp == nil || prepared.SurfaceOp.Op != "append" || pending.EventID == "" ||
		dispatched.EventID == "" || unknown.EventID == "" || done.EventID == "" ||
		!(prepared.Seq < pending.Seq && pending.Seq < dispatched.Seq && dispatched.Seq < unknown.Seq && unknown.Seq < done.Seq && dispatched.Seq < cancelSeq) {
		return false
	}
	var intent, closure commitPayload
	return json.Unmarshal(pending.Payload, &intent) == nil && json.Unmarshal(done.Payload, &closure) == nil &&
		intent.CommitID == commitIDFor(call.RunID, call.ToolCallID) && closure.CommitID == intent.CommitID &&
		closure.PendingSeq == pending.Seq && closure.PendingChecksum == pending.Checksum
}
