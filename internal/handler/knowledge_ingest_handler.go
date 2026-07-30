package handler

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path"
	"strconv"
	"strings"
	"unicode"

	"code-agent/internal/service"

	"github.com/gin-gonic/gin"
)

const (
	defaultKnowledgeIngestMaxFileBytes = int64(256 * 1024 * 1024)
	knowledgeIngestMultipartOverhead   = int64(64 * 1024)
)

// KnowledgeIngestService is the upload capability needed by internal ingestion.
type KnowledgeIngestService interface {
	UploadChunk(ctx context.Context, fileMD5, fileName string, totalSize int64, chunkIndex int, file multipart.File, chunkMD5 string, userID uint, orgTag string, isPublic bool) ([]int, int, error)
	MergeChunks(ctx context.Context, fileMD5, fileName string, userID uint) (string, error)
}

// KnowledgeIngestHandler accepts complete documents from trusted internal clients.
type KnowledgeIngestHandler struct {
	service      KnowledgeIngestService
	maxFileBytes int64
}

// NewKnowledgeIngestHandler creates an internal knowledge ingestion handler.
func NewKnowledgeIngestHandler(uploadService KnowledgeIngestService) *KnowledgeIngestHandler {
	return newKnowledgeIngestHandler(uploadService, defaultKnowledgeIngestMaxFileBytes)
}

func newKnowledgeIngestHandler(uploadService KnowledgeIngestService, maxFileBytes int64) *KnowledgeIngestHandler {
	return &KnowledgeIngestHandler{service: uploadService, maxFileBytes: maxFileBytes}
}

// Ingest uploads a complete document in bounded chunks and queues its processing pipeline.
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

	isPublic := false
	switch c.Request.PostForm.Get("isPublic") {
	case "", "false":
	case "true":
		isPublic = true
	default:
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

	fullHash := md5.New()
	totalSize, err := io.Copy(fullHash, io.LimitReader(file, h.maxFileBytes+1))
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
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		knowledgeIngestInternalError(c)
		return
	}

	fileMD5 := hex.EncodeToString(fullHash.Sum(nil))
	buffer := make([]byte, service.DefaultChunkSize)
	for chunkIndex := 0; ; chunkIndex++ {
		read, readErr := io.ReadFull(file, buffer)
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
			knowledgeIngestInternalError(c)
			return
		}
		if read == 0 {
			break
		}

		chunk := buffer[:read]
		chunkHash := md5.Sum(chunk)
		chunkFile := &knowledgeIngestChunkFile{Reader: bytes.NewReader(chunk)}
		if _, _, err := h.service.UploadChunk(
			c.Request.Context(),
			fileMD5,
			fileName,
			totalSize,
			chunkIndex,
			chunkFile,
			hex.EncodeToString(chunkHash[:]),
			uint(userIDValue),
			c.Request.PostForm.Get("orgTag"),
			isPublic,
		); err != nil {
			knowledgeIngestInternalError(c)
			return
		}
		if readErr == io.ErrUnexpectedEOF {
			break
		}
	}

	objectURL, err := h.service.MergeChunks(c.Request.Context(), fileMD5, fileName, uint(userIDValue))
	if err != nil {
		knowledgeIngestInternalError(c)
		return
	}

	c.JSON(http.StatusAccepted, gin.H{
		"code":    http.StatusAccepted,
		"message": "ingestion queued",
		"data": gin.H{
			"fileMd5":   fileMD5,
			"fileName":  fileName,
			"objectUrl": objectURL,
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

type knowledgeIngestChunkFile struct {
	Reader *bytes.Reader
}

func (f *knowledgeIngestChunkFile) Read(p []byte) (int, error) {
	return f.Reader.Read(p)
}

func (f *knowledgeIngestChunkFile) ReadAt(p []byte, off int64) (int, error) {
	return f.Reader.ReadAt(p, off)
}

func (f *knowledgeIngestChunkFile) Seek(offset int64, whence int) (int64, error) {
	return f.Reader.Seek(offset, whence)
}

func (f *knowledgeIngestChunkFile) Close() error {
	return nil
}
