package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"code-agent/internal/model"
	"code-agent/pkg/tasks"
)

type recordingCacheAdapter struct {
	clears int
	puts   map[int][]float32
	ttl    time.Duration
}

func (r *recordingCacheAdapter) Clear(context.Context, string) error { r.clears++; return nil }
func (r *recordingCacheAdapter) Put(_ context.Context, _ string, vectors map[int][]float32) error {
	r.puts = vectors
	return nil
}
func (r *recordingCacheAdapter) SetTTL(_ context.Context, _ string, ttl time.Duration) error {
	r.ttl = ttl
	return nil
}
func (r *recordingCacheAdapter) Load(context.Context, string) (map[int][]float32, error) {
	return map[int][]float32{2: {1, 2}}, nil
}

func TestLegacyObjectStoreHandlesUnavailableClient(t *testing.T) {
	adapter := legacyObjectStore{}
	if _, err := adapter.Read(context.Background(), "bucket", "object"); err == nil {
		t.Fatal("Read() should report an unavailable MinIO client")
	}
	if err := adapter.Write(context.Background(), "bucket", "object", strings.NewReader("x"), 1, "text/plain"); err == nil {
		t.Fatal("Write() should report an unavailable MinIO client")
	}
	if _, err := adapter.Presign("bucket", "object", time.Minute); err == nil {
		t.Fatal("Presign() should report an unavailable MinIO client")
	}
	if err := adapter.Delete(context.Background(), "bucket", "object"); err != nil {
		t.Fatalf("Delete() should preserve nil-client cleanup no-op: %v", err)
	}
}

func TestEmbeddingCacheCompatForwardsOperations(t *testing.T) {
	backend := &recordingCacheAdapter{}
	loaderCalled := false
	adapter := embeddingCacheCompat{
		backend: backend,
		loader: func(context.Context, string) (map[int][]float32, error) {
			loaderCalled = true
			return map[int][]float32{7: {3, 4}}, nil
		},
	}
	if err := adapter.Clear(context.Background(), "key"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Put(context.Background(), "key", map[int][]float32{1: {1, 2}}); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetTTL(context.Background(), "key", time.Hour); err != nil {
		t.Fatal(err)
	}
	loaded, err := adapter.Load(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	if !loaderCalled || loaded[7][0] != 3 || backend.clears != 1 || backend.puts[1][1] != 2 || backend.ttl != time.Hour {
		t.Fatalf("adapter forwarding mismatch: loader=%v clears=%d puts=%v ttl=%s loaded=%v", loaderCalled, backend.clears, backend.puts, backend.ttl, loaded)
	}
}

func TestIndexWriterFuncForwardsBatch(t *testing.T) {
	called := false
	adapter := indexWriterFunc(func(_ context.Context, index string, docs []model.EsDocument) error {
		called = index == "target" && len(docs) == 1
		return nil
	})
	if err := adapter.Write(context.Background(), "target", []model.EsDocument{{VectorID: "v1"}}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("index writer adapter did not forward the batch")
	}
}

func TestLegacyDocumentLifecycleNoOpForLegacyTask(t *testing.T) {
	repo := &fakeDocumentRepo{}
	adapter := legacyDocumentLifecycle{repo: repo}
	legacy := tasks.FileProcessingTask{}
	if err := adapter.Active(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Skipped(context.Background(), legacy, "empty"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Failed(context.Background(), legacy, "index", errors.New("failed")); err != nil {
		t.Fatal(err)
	}
	if len(repo.markCalls) != 0 {
		t.Fatalf("legacy task touched lifecycle repository: %+v", repo.markCalls)
	}
}
