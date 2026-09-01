package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/internal/serverconfig"
	"code-agent/pkg/database"
	"code-agent/pkg/es"
	"code-agent/pkg/kafka"
	"code-agent/pkg/storage"
	"code-agent/pkg/tasks"

	"github.com/minio/minio-go/v7"
)

// legacyObjectStore adapts the existing MinIO package globals to the pipeline
// object-store seam. It is used only by the compatibility constructor.
type legacyObjectStore struct {
	cfg serverconfig.MinIOConfig
}

func (a legacyObjectStore) Read(ctx context.Context, bucket, object string) (io.ReadCloser, error) {
	if storage.MinioClient == nil {
		return nil, errors.New("minio client is unavailable")
	}
	return storage.MinioClient.GetObject(ctx, bucket, object, minio.GetObjectOptions{})
}

func (a legacyObjectStore) Write(ctx context.Context, bucket, object string, reader io.Reader, size int64, contentType string) error {
	if storage.MinioClient == nil {
		return errors.New("minio client is unavailable")
	}
	_, err := storage.MinioClient.PutObject(ctx, bucket, object, reader, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (a legacyObjectStore) Presign(bucket, object string, expiry time.Duration) (string, error) {
	if storage.MinioClient == nil {
		return "", errors.New("minio client is unavailable")
	}
	return storage.GetPresignedURL(bucket, object, expiry)
}

func (a legacyObjectStore) Delete(ctx context.Context, bucket, object string) error {
	if storage.MinioClient == nil {
		return nil
	}
	return storage.MinioClient.RemoveObject(ctx, bucket, object, minio.RemoveObjectOptions{})
}

type legacyTaskQueue struct{}

func (legacyTaskQueue) Publish(task tasks.FileProcessingTask) error {
	return kafka.ProduceTask(task)
}

// cachedEmbedding preserves the legacy string-cache representation used by
// older workers while the current writer uses a Redis hash.
type cachedEmbedding struct {
	ChunkID int       `json:"chunkId"`
	Vector  []float32 `json:"vector"`
}

type legacyEmbeddingCache struct{}

func (legacyEmbeddingCache) EnsureHash(ctx context.Context, key string) error {
	if database.RDB == nil {
		return errors.New("redis client is unavailable")
	}
	cacheType, err := database.RDB.Type(ctx, key).Result()
	if err != nil {
		return err
	}
	if cacheType == "none" || cacheType == "hash" {
		return nil
	}
	return database.RDB.Del(ctx, key).Err()
}
func (legacyEmbeddingCache) Clear(ctx context.Context, key string) error {
	if database.RDB == nil {
		return nil
	}
	return database.RDB.Del(ctx, key).Err()
}

func (legacyEmbeddingCache) Put(ctx context.Context, key string, vectors map[int][]float32) error {
	if database.RDB == nil {
		return errors.New("redis client is unavailable")
	}
	values := make(map[string]interface{}, len(vectors))
	for chunkID, vector := range vectors {
		encoded, err := json.Marshal(vector)
		if err != nil {
			return fmt.Errorf("marshal vector chunk=%d: %w", chunkID, err)
		}
		values[strconv.Itoa(chunkID)] = string(encoded)
	}
	if len(values) == 0 {
		return nil
	}
	return database.RDB.HSet(ctx, key, values).Err()
}

func (legacyEmbeddingCache) SetTTL(ctx context.Context, key string, ttl time.Duration) error {
	if database.RDB == nil {
		return errors.New("redis client is unavailable")
	}
	return database.RDB.Expire(ctx, key, ttl).Err()
}

func (legacyEmbeddingCache) Load(ctx context.Context, key string) (map[int][]float32, error) {
	if database.RDB == nil {
		return nil, errors.New("redis client is unavailable")
	}
	cacheType, err := database.RDB.Type(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	switch cacheType {
	case "hash":
		rawMap, err := database.RDB.HGetAll(ctx, key).Result()
		if err != nil {
			return nil, err
		}
		vectors := make(map[int][]float32, len(rawMap))
		for field, value := range rawMap {
			chunkID, err := strconv.Atoi(field)
			if err != nil {
				return nil, fmt.Errorf("invalid chunk id in cache field=%s: %w", field, err)
			}
			var vector []float32
			if err := json.Unmarshal([]byte(value), &vector); err != nil {
				return nil, fmt.Errorf("invalid vector for chunk=%d: %w", chunkID, err)
			}
			vectors[chunkID] = vector
		}
		return vectors, nil
	case "string":
		cacheBytes, err := database.RDB.Get(ctx, key).Bytes()
		if err != nil {
			return nil, err
		}
		var cache []cachedEmbedding
		if err := json.Unmarshal(cacheBytes, &cache); err != nil {
			return nil, err
		}
		vectors := make(map[int][]float32, len(cache))
		for _, item := range cache {
			vectors[item.ChunkID] = item.Vector
		}
		return vectors, nil
	case "none":
		return nil, errors.New("embedding cache key not found")
	default:
		return nil, fmt.Errorf("unsupported embedding cache type: %s", cacheType)
	}
}

type legacyIndexWriter struct{}

func (legacyIndexWriter) Write(ctx context.Context, index string, docs []model.EsDocument) error {
	if es.ESClient == nil {
		return errors.New("elasticsearch client is unavailable")
	}
	return es.BulkIndexDocuments(ctx, index, docs)
}

type legacyDocumentLifecycle struct {
	repo repository.KnowledgeDocumentRepository
}

func (a legacyDocumentLifecycle) Active(_ context.Context, task tasks.FileProcessingTask) error {
	if a.repo == nil || task.DocumentID == "" {
		return nil
	}
	return a.repo.MarkDocumentStatus(task.DocumentID, model.DocumentActive, "")
}

func (a legacyDocumentLifecycle) Skipped(_ context.Context, task tasks.FileProcessingTask, reason string) error {
	if a.repo == nil || task.DocumentID == "" {
		return nil
	}
	return a.repo.MarkDocumentStatus(task.DocumentID, model.DocumentSkipped, reason)
}

func (a legacyDocumentLifecycle) Failed(_ context.Context, task tasks.FileProcessingTask, stage string, cause error) error {
	if a.repo == nil || task.DocumentID == "" {
		return nil
	}
	return a.repo.MarkDocumentStatus(task.DocumentID, model.DocumentFailed, sanitizeErrorSummary(stage, cause))
}

type indexWriterFunc func(context.Context, string, []model.EsDocument) error

func (f indexWriterFunc) Write(ctx context.Context, index string, docs []model.EsDocument) error {
	return f(ctx, index, docs)
}

type embeddingCacheCompat struct {
	loader  func(context.Context, string) (map[int][]float32, error)
	backend EmbeddingCache
}

func (c embeddingCacheCompat) Clear(ctx context.Context, key string) error {
	return c.backend.Clear(ctx, key)
}
func (c embeddingCacheCompat) Put(ctx context.Context, key string, vectors map[int][]float32) error {
	return c.backend.Put(ctx, key, vectors)
}
func (c embeddingCacheCompat) SetTTL(ctx context.Context, key string, ttl time.Duration) error {
	return c.backend.SetTTL(ctx, key, ttl)
}
func (c embeddingCacheCompat) EnsureHash(ctx context.Context, key string) error {
	preparer, ok := c.backend.(hashCachePreparer)
	if !ok {
		return nil
	}
	return preparer.EnsureHash(ctx, key)
}
func (c embeddingCacheCompat) Load(ctx context.Context, key string) (map[int][]float32, error) {
	return c.loader(ctx, key)
}

func (p *Processor) objectStorePort() ObjectStore {
	if p.objectStore != nil {
		return p.objectStore
	}
	return legacyObjectStore{cfg: p.minioCfg}
}

func (p *Processor) taskQueuePort() TaskQueue {
	if p.taskQueue != nil {
		return p.taskQueue
	}
	return legacyTaskQueue{}
}

func (p *Processor) embeddingCachePort() EmbeddingCache {
	if p.embeddingCache != nil {
		return p.embeddingCache
	}
	backend := EmbeddingCache(legacyEmbeddingCache{})
	if p.vectorCache != nil {
		return embeddingCacheCompat{loader: p.vectorCache, backend: backend}
	}
	return backend
}

func (p *Processor) indexWriterPort() IndexWriter {
	if p.indexWriter != nil {
		return p.indexWriter
	}
	if p.esWriter != nil {
		return indexWriterFunc(p.esWriter)
	}
	return legacyIndexWriter{}
}

func (p *Processor) lifecyclePort() DocumentLifecycle {
	if p.lifecycle != nil {
		return p.lifecycle
	}
	return legacyDocumentLifecycle{repo: p.documentRepo}
}
