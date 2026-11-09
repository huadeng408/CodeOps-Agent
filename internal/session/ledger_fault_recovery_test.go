package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type taskStageEvent struct {
	TaskID    string `json:"task_id"`
	WorkerID  int    `json:"worker_id"`
	LeaseID   string `json:"lease_id"`
	Stage     int    `json:"stage"`
	Status    string `json:"status"`
	Timestamp int64  `json:"timestamp"`
}

func TestEightWorker200Tasks30FaultsLedgerRecovery(t *testing.T) {
	const (
		workerCount = 8
		taskCount   = 200
		faultCount  = 30
		totalStages = 3
	)

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "ledger-wal-recovery.db")
	ledger, err := OpenSQLiteEventLog(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLiteEventLog: %v", err)
	}
	defer ledger.Close()

	taskQueue := make(chan string, taskCount*2)
	for i := 1; i <= taskCount; i++ {
		taskQueue <- fmt.Sprintf("task-%04d", i)
	}

	var (
		injectedFaults atomic.Int32
		completedCount atomic.Int32
		casRetries     atomic.Int32
		completedTasks sync.Map
	)

	appendWithCAS := func(sessionID, eventType string, payload any, surfaceOp *SurfaceOperation) (Event, error) {
		for attempt := 0; attempt < 32; attempt++ {
			events, err := ledger.Events(ctx, sessionID)
			if err != nil {
				return Event{}, err
			}
			expectedSeq := int64(len(events))
			var ev Event
			if surfaceOp != nil {
				ev, err = ledger.AppendSurface(ctx, sessionID, expectedSeq, eventType, payload, *surfaceOp)
			} else {
				ev, err = ledger.Append(ctx, sessionID, expectedSeq, eventType, payload)
			}
			if err == nil {
				return ev, nil
			}
			if errors.Is(err, ErrSequenceConflict) {
				casRetries.Add(1)
				continue
			}
			return Event{}, err
		}
		return Event{}, fmt.Errorf("CAS retries exhausted for %s", sessionID)
	}

	recoverCommittedStage := func(sessionID string) (int, bool, error) {
		events, err := ledger.Events(ctx, sessionID)
		if err != nil {
			return 0, false, err
		}
		maxStage := 0
		completed := false
		for _, ev := range events {
			if ev.Type != "tool/result" && ev.Type != "assistant/message" {
				continue
			}
			var rec taskStageEvent
			if err := json.Unmarshal(ev.Payload, &rec); err != nil {
				return 0, false, err
			}
			if rec.Stage > maxStage {
				maxStage = rec.Stage
			}
			if rec.Status == "completed" {
				completed = true
			}
		}
		return maxStage, completed, nil
	}

	var wg sync.WaitGroup
	for workerID := 1; workerID <= workerCount; workerID++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for completedCount.Load() < taskCount {
				select {
				case taskID := <-taskQueue:
					sessionID := "sess-" + taskID
					lastStage, done, err := recoverCommittedStage(sessionID)
					if err != nil {
						t.Errorf("recoverCommittedStage(%s): %v", sessionID, err)
						return
					}
					if done {
						continue
					}

					leaseID := fmt.Sprintf("lease-w%d-%d", wid, time.Now().UnixNano())
					if _, err := appendWithCAS(sessionID, "lease/heartbeat", taskStageEvent{
						TaskID:    taskID,
						WorkerID:  wid,
						LeaseID:   leaseID,
						Stage:     lastStage,
						Status:    "leased",
						Timestamp: time.Now().UnixNano(),
					}, nil); err != nil {
						t.Errorf("append lease heartbeat(%s): %v", sessionID, err)
						return
					}

					faulted := false
					for stage := lastStage + 1; stage <= totalStages; stage++ {
						// Deterministically inject 30 mid-task worker faults after stage 1
						if stage == 2 {
							for {
								cur := injectedFaults.Load()
								if cur >= faultCount {
									break
								}
								if injectedFaults.CompareAndSwap(cur, cur+1) {
									_, _ = appendWithCAS(sessionID, "lease/fault_injected", taskStageEvent{
										TaskID:    taskID,
										WorkerID:  wid,
										LeaseID:   leaseID,
										Stage:     stage - 1,
										Status:    "fault_abort",
										Timestamp: time.Now().UnixNano(),
									}, nil)
									// Re-queue task for breakpoint resumption from committed stage 1
									taskQueue <- taskID
									faulted = true
									break
								}
							}
						}
						if faulted {
							break
						}

						if _, err := appendWithCAS(sessionID, "tool/result", taskStageEvent{
							TaskID:    taskID,
							WorkerID:  wid,
							LeaseID:   leaseID,
							Stage:     stage,
							Status:    "stage_done",
							Timestamp: time.Now().UnixNano(),
						}, &SurfaceOperation{Op: "append"}); err != nil {
							t.Errorf("append stage(%s, %d): %v", sessionID, stage, err)
							return
						}
					}

					if faulted {
						continue
					}

					// Compact intermediate stage events via context interval projection (SurfaceOperation replace)
					events, err := ledger.Events(ctx, sessionID)
					if err != nil || len(events) == 0 {
						t.Errorf("read events for surface projection(%s): %v", sessionID, err)
						return
					}
					op := &SurfaceOperation{
						Op:    "replace",
						Start: 0,
						End:   events[len(events)-1].Seq,
					}
					if _, err := appendWithCAS(sessionID, "assistant/message", taskStageEvent{
						TaskID:    taskID,
						WorkerID:  wid,
						LeaseID:   leaseID,
						Stage:     totalStages,
						Status:    "completed",
						Timestamp: time.Now().UnixNano(),
					}, op); err != nil {
						t.Errorf("append completed surface(%s): %v", sessionID, err)
						return
					}

					if _, loaded := completedTasks.LoadOrStore(taskID, true); !loaded {
						completedCount.Add(1)
					}
				default:
					if completedCount.Load() >= taskCount {
						return
					}
					time.Sleep(time.Millisecond)
				}
			}
		}(workerID)
	}

	wg.Wait()

	if got := completedCount.Load(); got != taskCount {
		t.Fatalf("expected %d completed tasks, got %d", taskCount, got)
	}
	if got := injectedFaults.Load(); got != faultCount {
		t.Fatalf("expected %d injected faults, got %d", faultCount, got)
	}

	// Verify SHA-256 hash chain integrity and context interval surface projection across all 200 tasks
	for i := 1; i <= taskCount; i++ {
		sessionID := fmt.Sprintf("sess-task-%04d", i)
		if err := ledger.Verify(ctx, sessionID); err != nil {
			t.Fatalf("SHA-256 hash chain verification failed for %s: %v", sessionID, err)
		}
		surface, err := ledger.Surface(ctx, sessionID)
		if err != nil {
			t.Fatalf("Surface projection failed for %s: %v", sessionID, err)
		}
		if len(surface) != 1 || surface[0].Type != "assistant/message" {
			t.Fatalf("expected compacted surface of length 1 for %s, got %d events", sessionID, len(surface))
		}
	}
}
