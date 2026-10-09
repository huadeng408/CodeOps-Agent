package admission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"code-agent/internal/session"
)

const VerificationTokenLimit int64 = 100_000_000
const RelayConcurrencyLimit = 10

var (
	ErrBudgetExhausted  = errors.New("verification token budget exhausted")
	ErrTaskBusy         = errors.New("verification batch already has another task")
	ErrInvalidAdmission = errors.New("invalid token admission request")
	ErrUsageUnknown     = errors.New("verification usage requires reconciliation")
	ErrCallReserved     = errors.New("model call is already reserved; do not dispatch again")
	ErrRelayBusy        = errors.New("verification model concurrency limit reached")
	ErrTaskClosed       = errors.New("verification task has already finished")
)

type Batch struct {
	ID             string
	LimitTokens    int64
	UsedTokens     int64
	ReservedTokens int64
	TaskID         string
	TaskOwnerID    uint
	UnknownUsage   bool
	CostStatus     string
}

type Budget struct{ ledger session.EventLog }

func NewBudget(ledger session.EventLog) *Budget { return &Budget{ledger: ledger} }

type budgetFact struct {
	OwnerID       uint   `json:"owner_id,omitempty"`
	Limit         int64  `json:"limit_tokens,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	CallID        string `json:"call_id,omitempty"`
	Tokens        int64  `json:"tokens,omitempty"`
	Purpose       string `json:"purpose,omitempty"`
	InputTokens   int64  `json:"input_tokens,omitempty"`
	OutputTokens  int64  `json:"output_tokens,omitempty"`
	CachedTokens  int64  `json:"cached_tokens,omitempty"`
	CostStatus    string `json:"cost_status,omitempty"`
	BoundViolated bool   `json:"bound_violated,omitempty"`
}

type call struct {
	owner         uint
	taskID        string
	reserved      int64
	used          int64
	settled       bool
	unknown       bool
	boundViolated bool
}

type projection struct {
	Batch
	seq         int64
	calls       map[string]call
	closedTasks map[string]bool
}

func (state projection) outstanding() int {
	total := 0
	for _, request := range state.calls {
		if !request.settled {
			total++
		}
	}
	return total
}

func (state projection) unknownUsage() bool {
	for _, request := range state.calls {
		if request.unknown && !request.settled {
			return true
		}
	}
	return false
}

// All processes in the verification batch must use the same canonical Ledger.
const verificationLedgerID = session.VerificationAdmissionLedgerID

func taskKey(owner uint, taskID string) string { return fmt.Sprintf("%d:%s", owner, taskID) }

func validID(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func (budget *Budget) read(ctx context.Context, owner uint) (projection, error) {
	state := projection{Batch: Batch{LimitTokens: VerificationTokenLimit, CostStatus: "unknown"}, calls: map[string]call{}, closedTasks: map[string]bool{}}
	if owner == 0 || budget.ledger == nil {
		return state, ErrInvalidAdmission
	}
	snapshot, err := session.ReadVerifiedSnapshot(ctx, budget.ledger, verificationLedgerID)
	if err != nil {
		return state, err
	}
	for index, event := range snapshot.Events {
		var fact budgetFact
		if err := json.Unmarshal(event.Payload, &fact); err != nil {
			return state, session.ErrEventIntegrity
		}
		switch event.Type {
		case session.VerificationAdmissionCreatedEventType:
			if index != 0 || fact.OwnerID == 0 || fact.Limit != VerificationTokenLimit {
				return state, session.ErrEventIntegrity
			}
			state.ID = event.EventID
		case "admission/call_reserved":
			if state.ID == "" || fact.OwnerID == 0 || !validID(fact.CallID) || !validID(fact.TaskID) || state.unknownUsage() || state.closedTasks[taskKey(fact.OwnerID, fact.TaskID)] || fact.Tokens <= 0 || fact.Tokens > VerificationTokenLimit {
				return state, session.ErrEventIntegrity
			}
			if _, exists := state.calls[fact.CallID]; exists || (state.TaskID != "" && (state.TaskID != fact.TaskID || state.TaskOwnerID != fact.OwnerID)) || state.outstanding() >= RelayConcurrencyLimit || fact.Tokens > state.LimitTokens-state.UsedTokens-state.ReservedTokens {
				return state, session.ErrEventIntegrity
			}
			state.TaskID, state.TaskOwnerID = fact.TaskID, fact.OwnerID
			state.calls[fact.CallID] = call{owner: fact.OwnerID, taskID: fact.TaskID, reserved: fact.Tokens}
			state.ReservedTokens += fact.Tokens
		case "admission/call_settled":
			request, exists := state.calls[fact.CallID]
			if !exists || fact.OwnerID != request.owner || request.settled || request.boundViolated || fact.Tokens < 0 || fact.Tokens > request.reserved {
				return state, session.ErrEventIntegrity
			}
			if fact.CostStatus != "" && (fact.CostStatus != "unknown" || fact.InputTokens < 0 || fact.OutputTokens < 0 || fact.InputTokens > fact.Tokens || fact.OutputTokens != fact.Tokens-fact.InputTokens || fact.CachedTokens < 0 || fact.CachedTokens > fact.InputTokens) {
				return state, session.ErrEventIntegrity
			}
			state.ReservedTokens -= request.reserved
			state.UsedTokens += fact.Tokens
			request.settled, request.used = true, fact.Tokens
			state.calls[fact.CallID] = request
		case "admission/call_unknown":
			request, exists := state.calls[fact.CallID]
			if !exists || fact.OwnerID != request.owner || request.settled || fact.Tokens < 0 || (request.unknown && ((!fact.BoundViolated && fact.Tokens <= request.reserved) || request.boundViolated)) {
				return state, session.ErrEventIntegrity
			}
			request.unknown = true
			request.boundViolated = request.boundViolated || fact.BoundViolated || fact.Tokens > request.reserved
			state.calls[fact.CallID] = request
		case "admission/task_finished":
			if state.TaskID == "" || state.TaskID != fact.TaskID || state.TaskOwnerID != fact.OwnerID || state.outstanding() != 0 {
				return state, session.ErrEventIntegrity
			}
			state.closedTasks[taskKey(fact.OwnerID, fact.TaskID)] = true
			state.TaskID, state.TaskOwnerID = "", 0
		default:
			return state, session.ErrEventIntegrity
		}
		state.seq++
	}
	state.UnknownUsage = state.unknownUsage()
	return state, nil
}

func (budget *Budget) Open(ctx context.Context, owner uint) (Batch, error) {
	for attempt := 0; attempt < 8; attempt++ {
		state, err := budget.read(ctx, owner)
		if err != nil || state.ID != "" {
			return state.Batch, err
		}
		event, err := budget.ledger.Append(ctx, verificationLedgerID, 0, session.VerificationAdmissionCreatedEventType, budgetFact{OwnerID: owner, Limit: VerificationTokenLimit})
		if errors.Is(err, session.ErrSequenceConflict) {
			continue
		}
		if err != nil {
			return Batch{}, err
		}
		state.ID = event.EventID
		return state.Batch, nil
	}
	return Batch{}, session.ErrSequenceConflict
}

// Inspect is a read-only projection for authenticated operator surfaces.
func (budget *Budget) Inspect(ctx context.Context, owner uint) (Batch, error) {
	state, err := budget.read(ctx, owner)
	return state.Batch, err
}

func (budget *Budget) Reserve(ctx context.Context, owner uint, taskID, callID string, tokens int64) (Batch, error) {
	return budget.reserve(ctx, owner, taskID, callID, tokens, RelayConcurrencyLimit, "")
}

func (budget *Budget) reserve(ctx context.Context, owner uint, taskID, callID string, tokens int64, concurrency int, purpose string) (Batch, error) {
	if !validID(taskID) || !validID(callID) || tokens <= 0 || tokens > VerificationTokenLimit {
		return Batch{}, ErrInvalidAdmission
	}
	if _, err := budget.Open(ctx, owner); err != nil {
		return Batch{}, err
	}
	for attempt := 0; attempt < 8; attempt++ {
		state, err := budget.read(ctx, owner)
		if err != nil {
			return Batch{}, err
		}
		if state.UnknownUsage {
			return state.Batch, ErrUsageUnknown
		}
		if previous, exists := state.calls[callID]; exists {
			if previous.owner != owner || previous.taskID != taskID || previous.reserved != tokens {
				return state.Batch, ErrInvalidAdmission
			}
			return state.Batch, ErrCallReserved
		}
		if state.closedTasks[taskKey(owner, taskID)] {
			return state.Batch, ErrTaskClosed
		}
		if state.TaskID != "" && (state.TaskID != taskID || state.TaskOwnerID != owner) {
			return state.Batch, ErrTaskBusy
		}
		if state.outstanding() >= concurrency {
			return state.Batch, ErrRelayBusy
		}
		if tokens > state.LimitTokens-state.UsedTokens-state.ReservedTokens {
			return state.Batch, ErrBudgetExhausted
		}
		_, err = budget.ledger.Append(ctx, verificationLedgerID, state.seq, "admission/call_reserved", budgetFact{OwnerID: owner, TaskID: taskID, CallID: callID, Tokens: tokens, Purpose: purpose})
		if errors.Is(err, session.ErrSequenceConflict) {
			continue
		}
		if err != nil {
			return state.Batch, err
		}
		updated, err := budget.read(ctx, owner)
		return updated.Batch, err
	}
	return Batch{}, session.ErrSequenceConflict
}

func (budget *Budget) Settle(ctx context.Context, owner uint, callID string, tokens int64) (Batch, error) {
	return budget.settle(ctx, owner, callID, tokens, budgetFact{})
}

func (budget *Budget) settle(ctx context.Context, owner uint, callID string, tokens int64, fact budgetFact) (Batch, error) {
	for attempt := 0; attempt < 8; attempt++ {
		state, err := budget.read(ctx, owner)
		if err != nil {
			return Batch{}, err
		}
		previous, exists := state.calls[callID]
		if !exists || previous.owner != owner || tokens < 0 || tokens > previous.reserved || (previous.settled && previous.used != tokens) {
			if exists && previous.owner == owner && !previous.settled && tokens > previous.reserved {
				batch, err := budget.markUnknown(ctx, owner, callID, tokens, true)
				return batch, errors.Join(ErrUsageUnknown, err)
			}
			return state.Batch, ErrInvalidAdmission
		}
		if previous.boundViolated {
			return state.Batch, ErrUsageUnknown
		}
		if previous.settled {
			return state.Batch, nil
		}
		fact.OwnerID, fact.CallID, fact.Tokens = owner, callID, tokens
		_, err = budget.ledger.Append(ctx, verificationLedgerID, state.seq, "admission/call_settled", fact)
		if errors.Is(err, session.ErrSequenceConflict) {
			continue
		}
		if err != nil {
			return state.Batch, err
		}
		updated, err := budget.read(ctx, owner)
		return updated.Batch, err
	}
	return Batch{}, session.ErrSequenceConflict
}

func (budget *Budget) MarkUnknown(ctx context.Context, owner uint, callID string) (Batch, error) {
	return budget.markUnknown(ctx, owner, callID, 0, false)
}

func (budget *Budget) markUnknown(ctx context.Context, owner uint, callID string, observedTokens int64, boundViolated bool) (Batch, error) {
	for attempt := 0; attempt < 8; attempt++ {
		state, err := budget.read(ctx, owner)
		if err != nil {
			return Batch{}, err
		}
		previous, exists := state.calls[callID]
		if !exists || previous.owner != owner || previous.settled {
			return state.Batch, ErrInvalidAdmission
		}
		if previous.unknown && ((!boundViolated && observedTokens <= previous.reserved) || previous.boundViolated) {
			return state.Batch, nil
		}
		_, err = budget.ledger.Append(ctx, verificationLedgerID, state.seq, "admission/call_unknown", budgetFact{OwnerID: owner, CallID: callID, Tokens: observedTokens, BoundViolated: boundViolated})
		if errors.Is(err, session.ErrSequenceConflict) {
			continue
		}
		if err != nil {
			return state.Batch, err
		}
		updated, err := budget.read(ctx, owner)
		return updated.Batch, err
	}
	return Batch{}, session.ErrSequenceConflict
}

// FinishTask releases the task slot only after every foreground, retry and
// auxiliary call has been reconciled. It never resets the batch allowance.
func (budget *Budget) FinishTask(ctx context.Context, owner uint, taskID string) (Batch, error) {
	if !validID(taskID) {
		return Batch{}, ErrInvalidAdmission
	}
	for attempt := 0; attempt < 8; attempt++ {
		state, err := budget.read(ctx, owner)
		if err != nil {
			return Batch{}, err
		}
		if state.closedTasks[taskKey(owner, taskID)] {
			return state.Batch, nil
		}
		if state.TaskID != taskID || state.TaskOwnerID != owner {
			return state.Batch, ErrInvalidAdmission
		}
		if state.outstanding() != 0 {
			return state.Batch, ErrUsageUnknown
		}
		_, err = budget.ledger.Append(ctx, verificationLedgerID, state.seq, "admission/task_finished", budgetFact{OwnerID: owner, TaskID: taskID})
		if errors.Is(err, session.ErrSequenceConflict) {
			continue
		}
		if err != nil {
			return state.Batch, err
		}
		updated, err := budget.read(ctx, owner)
		return updated.Batch, err
	}
	return Batch{}, session.ErrSequenceConflict
}
