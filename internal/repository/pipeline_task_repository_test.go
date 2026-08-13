package repository

import (
	"testing"

	"code-agent/internal/model"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newPipelineTaskDB opens an in-memory sqlite database (pure-Go via modernc)
// and auto-migrates the pipeline_task table. The test suite is hermetic: it
// never touches MySQL, so it runs in every environment without integration
// flags. This lets the run-aware dedup contract be exercised in CI/RED phase.
func newPipelineTaskDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.PipelineTask{}); err != nil {
		t.Fatalf("auto-migrate pipeline_task: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

// TestMarkProcessingRunCreatesRunWithRunKey asserts the run-keyed create
// writes the exact run-prefixed idempotency_key and stores the RunID column.
func TestMarkProcessingRunCreatesRunWithRunKey(t *testing.T) {
	db := newPipelineTaskDB(t)
	repo := NewPipelineTaskRepository(db)

	task, err := repo.MarkProcessingRun("r1", "md5", "chunk", -1)
	if err != nil {
		t.Fatalf("MarkProcessingRun: %v", err)
	}
	if task.IdempotencyKey != "run:r1:md5:chunk:-1" {
		t.Fatalf("idempotency_key=%q want run:r1:md5:chunk:-1", task.IdempotencyKey)
	}
	if task.RunID != "r1" {
		t.Fatalf("RunID=%q want r1", task.RunID)
	}
	if task.Status != model.PipelineStatusProcessing {
		t.Fatalf("status=%q want PROCESSING", task.Status)
	}
	if task.FileMD5 != "md5" || task.Stage != "chunk" || task.ChunkID != -1 {
		t.Fatalf("row fields mismatch: %+v", task)
	}
}

// TestMarkProcessingRunIsIdempotent asserts that re-marking the same run key
// returns the same row (upsert by idempotency_key) and never raises a unique
// conflict — the load-bearing guarantee that controlled replays dedup within
// a run without exploding the unique index.
func TestMarkProcessingRunIsIdempotent(t *testing.T) {
	db := newPipelineTaskDB(t)
	repo := NewPipelineTaskRepository(db)

	first, err := repo.MarkProcessingRun("r1", "md5", "chunk", -1)
	if err != nil {
		t.Fatalf("first MarkProcessingRun: %v", err)
	}
	second, err := repo.MarkProcessingRun("r1", "md5", "chunk", -1)
	if err != nil {
		t.Fatalf("second MarkProcessingRun (must not conflict): %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected same row, got first.ID=%d second.ID=%d", first.ID, second.ID)
	}
	if second.Status != model.PipelineStatusProcessing {
		t.Fatalf("status=%q want PROCESSING", second.Status)
	}

	var count int64
	if err := db.Model(&model.PipelineTask{}).Where("idempotency_key = ?", "run:r1:md5:chunk:-1").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("row count for run key = %d, want exactly 1 (no unique conflict, no duplicate)", count)
	}
}

// TestMarkSuccessRunUpdatesStatus asserts success flips status on the run row.
func TestMarkSuccessRunUpdatesStatus(t *testing.T) {
	db := newPipelineTaskDB(t)
	repo := NewPipelineTaskRepository(db)

	if _, err := repo.MarkProcessingRun("r1", "md5", "chunk", -1); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkSuccessRun("r1", "md5", "chunk", -1); err != nil {
		t.Fatalf("MarkSuccessRun: %v", err)
	}
	row, err := repo.GetByRunKey("r1", "md5", "chunk", -1)
	if err != nil {
		t.Fatalf("GetByRunKey: %v", err)
	}
	if row.Status != model.PipelineStatusSuccess {
		t.Fatalf("status=%q want SUCCESS", row.Status)
	}
	if row.LastError != "" {
		t.Fatalf("LastError=%q want empty on success", row.LastError)
	}
}

// TestMarkRetryRunIncrementsRetryCount asserts retry bumps the counter and
// records the sanitized error on the run row.
func TestMarkRetryRunIncrementsRetryCount(t *testing.T) {
	db := newPipelineTaskDB(t)
	repo := NewPipelineTaskRepository(db)

	if _, err := repo.MarkProcessingRun("r1", "md5", "chunk", -1); err != nil {
		t.Fatal(err)
	}
	n, err := repo.MarkRetryRun("r1", "md5", "chunk", -1, "boom")
	if err != nil {
		t.Fatalf("MarkRetryRun: %v", err)
	}
	if n != 1 {
		t.Fatalf("retry count=%d want 1", n)
	}
	n2, err := repo.MarkRetryRun("r1", "md5", "chunk", -1, "boom2")
	if err != nil {
		t.Fatalf("second MarkRetryRun: %v", err)
	}
	if n2 != 2 {
		t.Fatalf("retry count=%d want 2", n2)
	}
	row, err := repo.GetByRunKey("r1", "md5", "chunk", -1)
	if err != nil {
		t.Fatal(err)
	}
	if row.RetryCount != 2 {
		t.Fatalf("persisted RetryCount=%d want 2", row.RetryCount)
	}
	if row.LastError != "boom2" {
		t.Fatalf("LastError=%q want boom2", row.LastError)
	}
}

// TestMarkFailedRunSetsFailed asserts failed status + error on the run row.
func TestMarkFailedRunSetsFailed(t *testing.T) {
	db := newPipelineTaskDB(t)
	repo := NewPipelineTaskRepository(db)

	if _, err := repo.MarkProcessingRun("r1", "md5", "chunk", -1); err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkFailedRun("r1", "md5", "chunk", -1, "explode"); err != nil {
		t.Fatalf("MarkFailedRun: %v", err)
	}
	row, err := repo.GetByRunKey("r1", "md5", "chunk", -1)
	if err != nil {
		t.Fatal(err)
	}
	if row.Status != model.PipelineStatusFailed {
		t.Fatalf("status=%q want FAILED", row.Status)
	}
	if row.LastError != "explode" {
		t.Fatalf("LastError=%q want explode", row.LastError)
	}
}

// TestGetByRunKeyMissing asserts a missing run key returns gorm.ErrRecordNotFound.
func TestGetByRunKeyMissing(t *testing.T) {
	db := newPipelineTaskDB(t)
	repo := NewPipelineTaskRepository(db)

	if _, err := repo.GetByRunKey("r1", "md5", "chunk", -1); err == nil {
		t.Fatal("expected ErrRecordNotFound for missing run key, got nil")
	}
}

func TestListByRunAndFileReturnsOnlyCurrentRunOrderedByStage(t *testing.T) {
	db := newPipelineTaskDB(t)
	repo := NewPipelineTaskRepository(db)

	for _, stage := range []string{"index", "parse", "embed", "chunk"} {
		if _, err := repo.MarkProcessingRun("current-run", "current-md5", stage, -1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.MarkProcessingRun("historical-run", "current-md5", "parse", -1); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.MarkProcessingRun("current-run", "other-md5", "parse", -1); err != nil {
		t.Fatal(err)
	}

	tasks, err := repo.ListByRunAndFile("current-run", "current-md5")
	if err != nil {
		t.Fatalf("ListByRunAndFile: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("task count = %d, want 4: %+v", len(tasks), tasks)
	}
	want := []string{"parse", "chunk", "embed", "index"}
	for i := range want {
		if tasks[i].Stage != want[i] || tasks[i].RunID != "current-run" || tasks[i].FileMD5 != "current-md5" {
			t.Fatalf("task[%d] = %+v, want stage %q scoped to current run/file", i, tasks[i], want[i])
		}
	}
}

// TestLegacyMarkProcessingCoexistsWithRunRows is the R1 load-bearing
// compatibility test: a legacy message (no RunID) and a controlled run
// message for the SAME file_md5/stage/chunk_id must coexist as two distinct
// rows with distinct idempotency keys, neither shadowing the other. Legacy
// MarkProcessing semantics are completely unchanged.
func TestLegacyMarkProcessingCoexistsWithRunRows(t *testing.T) {
	db := newPipelineTaskDB(t)
	repo := NewPipelineTaskRepository(db)

	// Legacy path: old key file_md5:stage:chunk_id, RunID empty.
	legacy, err := repo.MarkProcessing("md5", "chunk", -1)
	if err != nil {
		t.Fatalf("legacy MarkProcessing: %v", err)
	}
	if legacy.IdempotencyKey != "md5:chunk:-1" {
		t.Fatalf("legacy idempotency_key=%q want md5:chunk:-1", legacy.IdempotencyKey)
	}
	if legacy.RunID != "" {
		t.Fatalf("legacy RunID=%q want empty", legacy.RunID)
	}

	// Mark the legacy row SUCCESS to prove the run row is NOT shadowed by it.
	if err := repo.MarkSuccess("md5", "chunk", -1); err != nil {
		t.Fatal(err)
	}

	// Run path: same file_md5/stage/chunk_id, different (run-prefixed) key.
	run, err := repo.MarkProcessingRun("r1", "md5", "chunk", -1)
	if err != nil {
		t.Fatalf("MarkProcessingRun coexisting with legacy: %v", err)
	}
	if run.ID == legacy.ID {
		t.Fatalf("run row must be distinct from legacy row, both ID=%d", run.ID)
	}
	if run.RunID != "r1" {
		t.Fatalf("run RunID=%q want r1", run.RunID)
	}

	// Two rows coexist for the same file_md5.
	var count int64
	if err := db.Model(&model.PipelineTask{}).Where("file_md5 = ?", "md5").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("row count for file_md5=md5 = %d, want 2 (legacy + run coexist)", count)
	}

	// Legacy lookup unchanged: still sees its SUCCESS row.
	legacyRow, err := repo.GetByKey("md5", "chunk", -1)
	if err != nil {
		t.Fatalf("legacy GetByKey: %v", err)
	}
	if legacyRow.Status != model.PipelineStatusSuccess {
		t.Fatalf("legacy status=%q want SUCCESS (legacy semantics unchanged)", legacyRow.Status)
	}
	if legacyRow.RunID != "" {
		t.Fatalf("legacy RunID=%q want empty", legacyRow.RunID)
	}

	// Run lookup sees its own PROCESSING row, distinct from legacy SUCCESS.
	runRow, err := repo.GetByRunKey("r1", "md5", "chunk", -1)
	if err != nil {
		t.Fatalf("GetByRunKey: %v", err)
	}
	if runRow.Status != model.PipelineStatusProcessing {
		t.Fatalf("run status=%q want PROCESSING", runRow.Status)
	}
	if runRow.ID == legacyRow.ID {
		t.Fatalf("run row ID equals legacy row ID; run row was shadowed by legacy SUCCESS")
	}
}
