package handler

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"code-agent/internal/model"
	"code-agent/internal/serverconfig"
	"code-agent/internal/service"
	"code-agent/pkg/objectpath"
	"code-agent/pkg/storage"

	"github.com/gin-gonic/gin"
	"github.com/minio/minio-go/v7"
)

const (
	defaultKnowledgeIngestMaxFileBytes = int64(256 * 1024 * 1024)
	knowledgeIngestMultipartOverhead   = int64(64 * 1024)
)

// KnowledgeIngestHandler accepts complete documents from trusted internal
// clients and routes them through the dedicated corpus entry. Unlike the
// public /api/v1/upload/* path, this entry writes the merged object directly
// (via storage.MinioClient) and hands the presigned URL plus the full corpus
// provenance to CorpusIngestService, which is the authority for the corpus
// contract and lifecycle persistence. The handler never calls the legacy
// UploadService.MergeChunks, because that path enqueues a parse task without
// CorpusGeneration and would double-write into the legacy index.
type KnowledgeIngestHandler struct {
	corpusService service.CorpusIngestService
	minioCfg      serverconfig.MinIOConfig
	maxFileBytes  int64
	// putMerged and presign are the storage seam: production wiring uses the
	// package-level MinIO client, tests inject a recording fake. Keeping them
	// as function-typed fields avoids mutating package globals and lets each
	// test run hermetically.
	putMerged func(ctx context.Context, bucket, objectName string, reader io.Reader, size int64) error
	presign   func(bucket, objectName string) (string, error)
}

// NewKnowledgeIngestHandler creates an internal knowledge ingestion handler
// backed by the dedicated corpus service. The MinIO config supplies the bucket
// name used for the merged-object write and presigned URL.
func NewKnowledgeIngestHandler(corpusService service.CorpusIngestService, minioCfg serverconfig.MinIOConfig) *KnowledgeIngestHandler {
	return newKnowledgeIngestHandler(corpusService, minioCfg, defaultKnowledgeIngestMaxFileBytes)
}

func newKnowledgeIngestHandler(corpusService service.CorpusIngestService, minioCfg serverconfig.MinIOConfig, maxFileBytes int64) *KnowledgeIngestHandler {
	h := &KnowledgeIngestHandler{
		corpusService: corpusService,
		minioCfg:      minioCfg,
		maxFileBytes:  maxFileBytes,
	}
	h.putMerged = h.defaultPutMerged
	h.presign = h.defaultPresign
	return h
}

// defaultPutMerged writes the full file to the canonical merged-object path via
// the package-level MinIO client. It is only used in production wiring; tests
// override the putMerged field.
func (h *KnowledgeIngestHandler) defaultPutMerged(ctx context.Context, bucket, objectName string, reader io.Reader, size int64) error {
	if _, err := storage.MinioClient.PutObject(ctx, bucket, objectName, reader, size, minio.PutObjectOptions{}); err != nil {
		return err
	}
	return nil
}

// defaultPresign returns a one-hour presigned GET URL for the merged object.
func (h *KnowledgeIngestHandler) defaultPresign(bucket, objectName string) (string, error) {
	return storage.GetPresignedURL(bucket, objectName, time.Hour)
}

// Ingest reads a complete document plus its corpus provenance from a trusted
// internal multipart form, writes the merged object, and enqueues the corpus
// parse task through CorpusIngestService. Validation of generation / target
// index / loader user lives in the service; the handler is responsible for
// field presence, the anti-forgery raw-bytes sha256 check, storage, and status
// code mapping (400 = bad request / validation, 500 = storage or enqueue
// failure). Error bodies are fixed sanitized strings and never echo err.Error().
func (h *KnowledgeIngestHandler) Ingest(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.maxFileBytes+knowledgeIngestMultipartOverhead)
	if err := c.Request.ParseMultipartForm(service.DefaultChunkSize); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			knowledgeIngestTooLarge(c)
		} else {
			knowledgeIngestBadRequest(c)
		}
		return
	}
	if c.Request.MultipartForm != nil {
		defer c.Request.MultipartForm.RemoveAll()
	}

	userIDValue, err := strconv.ParseUint(strings.TrimSpace(c.Request.PostForm.Get("userId")), 10, strconv.IntSize)
	if err != nil || userIDValue == 0 {
		knowledgeIngestBadRequest(c)
		return
	}

	orgTag := strings.TrimSpace(c.Request.PostForm.Get("orgTag"))
	isPublic := false
	switch c.Request.PostForm.Get("isPublic") {
	case "", "false":
	case "true":
		isPublic = true
	default:
		knowledgeIngestBadRequest(c)
		return
	}

	// Provenance presence/format is checked here so any missing routing field
	// fails fast with a 400 and zero service calls. Generation / target index /
	// loader user equality is the service's authority (it owns the config).
	provenance := model.CorpusProvenance{
		SourceID:         strings.TrimSpace(c.Request.PostForm.Get("sourceId")),
		SourcePath:       strings.TrimSpace(c.Request.PostForm.Get("sourcePath")),
		SourceURL:        strings.TrimSpace(c.Request.PostForm.Get("sourceUrl")),
		SourceCommit:     strings.TrimSpace(c.Request.PostForm.Get("sourceCommit")),
		SourceSHA256:     strings.TrimSpace(c.Request.PostForm.Get("sourceSha256")),
		TargetIndex:      strings.TrimSpace(c.Request.PostForm.Get("targetIndex")),
		CorpusGeneration: strings.TrimSpace(c.Request.PostForm.Get("corpusGeneration")),
	}
	if err := provenance.Validate(); err != nil {
		knowledgeIngestBadRequest(c)
		return
	}

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		knowledgeIngestBadRequest(c)
		return
	}
	defer file.Close()

	fileName, validFileName := safeKnowledgeIngestFileName(header.Filename)
	if !validFileName {
		knowledgeIngestBadRequest(c)
		return
	}
	if header.Size > h.maxFileBytes {
		knowledgeIngestTooLarge(c)
		return
	}

	// Stream the upload once, accumulating md5 + sha256 and buffering the bytes
	// so the merged object can be written afterward. The LimitReader + size
	// check bounds memory to maxFileBytes and rejects oversized uploads.
	md5Hash := md5.New()
	sha256Hash := sha256.New()
	buffer := &bytes.Buffer{}
	multi := io.MultiWriter(md5Hash, sha256Hash, buffer)
	totalSize, err := io.Copy(multi, io.LimitReader(file, h.maxFileBytes+1))
	if err != nil {
		knowledgeIngestInternalError(c)
		return
	}
	if totalSize > h.maxFileBytes {
		knowledgeIngestTooLarge(c)
		return
	}
	if totalSize == 0 {
		knowledgeIngestBadRequest(c)
		return
	}

	fileMD5 := hex.EncodeToString(md5Hash.Sum(nil))
	contentSHA256 := hex.EncodeToString(sha256Hash.Sum(nil))

	// Anti-forgery guard: the declared source hash must equal the hash of the
	// raw bytes we actually received. Without this a trusted caller could pin a
	// benign hash and route unrelated content into the corpus index. Checked
	// before any storage write or service call.
	if contentSHA256 != provenance.SourceSHA256 {
		knowledgeIngestBadRequest(c)
		return
	}

	objectName := objectpath.MergedObjectName(fileMD5, fileName)
	if err := h.putMerged(c.Request.Context(), h.minioCfg.BucketName, objectName, bytes.NewReader(buffer.Bytes()), totalSize); err != nil {
		knowledgeIngestInternalError(c)
		return
	}

	objectURL, err := h.presign(h.minioCfg.BucketName, objectName)
	if err != nil {
		knowledgeIngestInternalError(c)
		return
	}

	req := service.CorpusIngestRequest{
		UserID:        uint(userIDValue),
		OrgTag:        orgTag,
		IsPublic:      isPublic,
		FileMD5:       fileMD5,
		FileName:      fileName,
		TotalSize:     totalSize,
		ObjectURL:     objectURL,
		ContentSHA256: contentSHA256,
		Provenance:    provenance,
		// runId scopes the enqueued parse task to a controlled run so the
		// consumer's run-aware dedup bypasses any stale historical SUCCESS for
		// this file_md5 and re-runs parse/chunk/embed/index. Optional on the
		// wire: absent -> empty -> legacy dedup semantics (backward compatible).
		RunID: strings.TrimSpace(c.Request.PostForm.Get("runId")),
	}
	result, err := h.corpusService.Ingest(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrCorpusValidation) {
			knowledgeIngestBadRequest(c)
			return
		}
		knowledgeIngestInternalError(c)
		return
	}

	documentID := ""
	if result != nil {
		documentID = result.DocumentID
	}
	c.JSON(http.StatusAccepted, gin.H{
		"code":    http.StatusAccepted,
		"message": "ingestion queued",
		"data": gin.H{
			"fileMd5":    fileMD5,
			"fileName":   fileName,
			"objectUrl":  objectURL,
			"documentId": documentID,
		},
	})
}

func knowledgeIngestBadRequest(c *gin.Context) {
	c.JSON(http.StatusBadRequest, gin.H{"code": http.StatusBadRequest, "message": "invalid ingestion request", "data": nil})
}

func knowledgeIngestInternalError(c *gin.Context) {
	c.JSON(http.StatusInternalServerError, gin.H{"code": http.StatusInternalServerError, "message": "knowledge ingestion failed", "data": nil})
}

func knowledgeIngestTooLarge(c *gin.Context) {
	c.JSON(http.StatusRequestEntityTooLarge, gin.H{"code": http.StatusRequestEntityTooLarge, "message": "ingestion file is too large", "data": nil})
}

func safeKnowledgeIngestFileName(value string) (string, bool) {
	normalized := strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	for _, char := range normalized {
		if unicode.IsControl(char) {
			return "", false
		}
	}
	fileName := path.Base(normalized)
	if fileName == "" || fileName == "." || fileName == ".." || fileName == "/" || len([]byte(fileName)) > 255 {
		return "", false
	}
	return fileName, true
}
