package session

import (
	"context"
	"encoding/json"
	"errors"

	pb "code-agent/gen/codeagentpb"
	"code-agent/internal/orchestrator"
)

type evaluationCompactionAdapter interface {
	CompactConversation(context.Context, orchestrator.ConversationRequest, orchestrator.ConversationHandlers) (*pb.CompactionUpdate, error)
}

func (r *SessionRunner) forceEvaluationCompaction(ctx context.Context, key runKey, lease runLeasePayload, request orchestrator.ConversationRequest, handlers orchestrator.ConversationHandlers) (orchestrator.ConversationRequest, error) {
	if request.AgentTask != nil || len(request.History) < 2 {
		return request, nil
	}
	snapshot, err := ReadVerifiedSnapshot(ctx, r.workbench.ledger, key.sessionID)
	if err != nil {
		return request, err
	}
	binding, err := ReadEvaluation(snapshot.Events)
	if err != nil || binding == nil || !binding.Policy.ForceCompaction {
		return request, err
	}
	for _, event := range snapshot.Events {
		if event.Type == compactionEventType {
			var compact compactionPayload
			if json.Unmarshal(event.Payload, &compact) != nil {
				return request, ErrEventIntegrity
			}
			if compact.RunID == key.runID {
				return request, nil
			}
		}
	}
	adapter, ok := r.conversation.(evaluationCompactionAdapter)
	if !ok || handlers.Compaction == nil {
		return request, errors.New("evaluation compaction transport unavailable")
	}
	update, err := adapter.CompactConversation(ctx, request, handlers)
	if err != nil {
		return request, err
	}
	if update == nil || update.RemovedMessages == 0 {
		return request, nil
	}
	// Rebuild exclusively from the newly persisted canonical Surface. This also
	// revalidates its lease, source prefix and active user input before Converse.
	return r.conversationRequest(ctx, key, lease)
}
