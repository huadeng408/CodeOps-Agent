package admission

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"

	"code-agent/internal/session"
)

// Limits are frozen in the root Session; every child and auxiliary call uses
// the same root. A missing binding retains the ordinary product budget policy.
func (budget *Budget) evaluationBudget(ctx context.Context, taskID string) (int64, time.Time, error) {
	snapshot, err := session.ReadVerifiedSnapshot(ctx, budget.ledger, taskID)
	if err != nil {
		return 0, time.Time{}, err
	}
	binding, err := session.ReadEvaluation(snapshot.Events)
	if err != nil || binding == nil {
		return 0, time.Time{}, err
	}
	if binding.MaxTokens <= 0 || binding.MaxTokens > VerificationTokenLimit || binding.WallClockSeconds <= 0 || binding.WallClockSeconds > 86400 || math.IsNaN(binding.WallClockSeconds) || math.IsInf(binding.WallClockSeconds, 0) || len(snapshot.Events) == 0 || snapshot.Events[0].CreatedAt.IsZero() {
		return 0, time.Time{}, ErrInvalidAdmission
	}
	return binding.MaxTokens, snapshot.Events[0].CreatedAt.Add(time.Duration(binding.WallClockSeconds * float64(time.Second))), nil
}

func (budget *Budget) checkEvaluationReservation(ctx context.Context, state projection, owner uint, taskID string, tokens int64) error {
	remaining, deadline, err := budget.evaluationBudget(ctx, taskID)
	if err != nil || remaining == 0 {
		return err
	}
	if !time.Now().Before(deadline) {
		return ErrWallClockBudget
	}
	for _, previous := range state.calls {
		if previous.owner != owner || previous.taskID != taskID {
			continue
		}
		if previous.boundViolated && !previous.settled {
			return ErrUsageUnknown
		}
		cost := previous.reserved
		if previous.settled {
			cost = previous.used
		}
		if cost > remaining {
			return ErrBudgetExhausted
		}
		remaining -= cost
	}
	if tokens > remaining {
		return ErrBudgetExhausted
	}
	return nil
}

func (gateway *Gateway) recordEvaluationModel(ctx context.Context, taskID, callID string, response any, profile session.EvaluationModelProfile) error {
	snapshot, err := session.ReadVerifiedSnapshot(ctx, gateway.budget.ledger, taskID)
	if err != nil {
		return err
	}
	binding, err := session.ReadEvaluation(snapshot.Events)
	if err != nil || binding == nil {
		return err
	}
	object, ok := response.(map[string]any)
	if !ok {
		return errors.New("invalid evaluation model response")
	}
	model, _ := object["model"].(string)
	fingerprint, _ := object["system_fingerprint"].(string)
	responseID, _ := object["id"].(string)
	if len(model) > 256 || len(fingerprint) > 256 || gateway.provider.APIKey != "" && (strings.Contains(model, gateway.provider.APIKey) || strings.Contains(fingerprint, gateway.provider.APIKey)) {
		return errors.New("invalid evaluation model identity")
	}
	responseHash := ""
	if responseID != "" {
		hash := sha256.Sum256([]byte(responseID))
		responseHash = hex.EncodeToString(hash[:])
	}
	for attempt := 0; attempt < 8; attempt++ {
		snapshot, err = session.ReadVerifiedSnapshot(ctx, gateway.budget.ledger, taskID)
		if err != nil {
			return err
		}
		_, err = gateway.budget.ledger.Append(ctx, taskID, int64(len(snapshot.Events)), "evaluation/model-response", map[string]any{"call_id": callID, "requested_model": gateway.provider.Model, "reported_model": model, "system_fingerprint": fingerprint, "response_id_sha256": responseHash, "effective_request_profile": profile})
		if errors.Is(err, session.ErrSequenceConflict) {
			continue
		}
		if err != nil {
			return err
		}
		if model != gateway.provider.expectedReportedModel() {
			return errors.New("evaluation model substitution")
		}
		return nil
	}
	return session.ErrSequenceConflict
}
