// Package handler contains HTTP and WebSocket endpoint handlers.
package handler

import (
	"net/http"
	"strconv"

	"code-agent/internal/service"
	"code-agent/pkg/log"
	"code-agent/pkg/token"

	"github.com/gin-gonic/gin"
)

// calculateProgress calculates progress.
func calculateProgress(uploadedChunks []int, totalChunks int) float64 {
	if totalChunks == 0 {
		return 0
	}
	return (float64(len(uploadedChunks)) / float64(totalChunks)) * 100
}

// UploadHandler handles upload requests.
type UploadHandler struct {
	uploadService service.UploadService
}

// NewUploadHandler creates an upload handler.
func NewUploadHandler(uploadService service.UploadService) *UploadHandler {
	return &UploadHandler{uploadService: uploadService}
}

// CheckFileRequest describes the check file request payload.
type CheckFileRequest struct {
	MD5 string `json:"md5" binding:"required"`
}

// CheckFile checks file.
func (h *UploadHandler) CheckFile(c *gin.Context) {
	var req CheckFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request payload"})
		return
	}

	claimsValue, _ := c.Get("claims")
	userClaims := claimsValue.(*token.CustomClaims)

	completed, uploadedChunks, err := h.uploadService.CheckFile(c.Request.Context(), req.MD5, userClaims.UserID)
	if err != nil {
		log.Error("CheckFile: failed to check file", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"completed":      completed,
		"uploadedChunks": uploadedChunks,
	})
}

// UploadChunk uploads chunk.
func (h *UploadHandler) UploadChunk(c *gin.Context) {
	fileMD5 := c.PostForm("fileMd5")
	fileName := c.PostForm("fileName")
	totalSizeStr := c.PostForm("totalSize")
	chunkIndexStr := c.PostForm("chunkIndex")
	chunkMD5 := c.PostForm("chunkMd5")
	orgTag := c.PostForm("orgTag")
	isPublicStr := c.PostForm("isPublic")

	if fileMD5 == "" || fileName == "" || totalSizeStr == "" || chunkIndexStr == "" || chunkMD5 == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing required parameters"})
		return
	}

	totalSize, err := strconv.ParseInt(totalSizeStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid totalSize"})
		return
	}
	if totalSize <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty file is not allowed"})
		return
	}

	chunkIndex, err := strconv.Atoi(chunkIndexStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid chunkIndex"})
		return
	}
	isPublic, _ := strconv.ParseBool(isPublicStr)

	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read chunk file"})
		return
	}
	defer file.Close()

	claims, _ := c.Get("claims")
	userClaims := claims.(*token.CustomClaims)

	uploadedChunks, totalChunks, err := h.uploadService.UploadChunk(
		c.Request.Context(),
		fileMD5,
		fileName,
		totalSize,
		chunkIndex,
		file,
		chunkMD5,
		userClaims.UserID,
		orgTag,
		isPublic,
	)
	if err != nil {
		log.Error("UploadChunk: failed to upload chunk", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    http.StatusInternalServerError,
			"message": "chunk upload failed: " + err.Error(),
		})
		return
	}

	progress := calculateProgress(uploadedChunks, totalChunks)
	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"message": "chunk upload success",
		"data": gin.H{
			"uploaded":    uploadedChunks,
			"progress":    progress,
			"totalChunks": totalChunks,
		},
	})
}

// MergeChunksRequest describes the merge chunks request payload.
type MergeChunksRequest struct {
	MD5      string `json:"fileMd5" binding:"required"`
	FileName string `json:"fileName" binding:"required"`
}

// MergeChunks merges chunks.
func (h *UploadHandler) MergeChunks(c *gin.Context) {
	var req MergeChunksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request payload"})
		return
	}

	claimsValue, _ := c.Get("claims")
	userClaims := claimsValue.(*token.CustomClaims)

	objectURL, err := h.uploadService.MergeChunks(c.Request.Context(), req.MD5, req.FileName, userClaims.UserID)
	if err != nil {
		log.Error("MergeChunks: failed to merge chunks", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "merge failed: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"message": "merge success, task sent to Kafka",
		"data":    gin.H{"object_url": objectURL},
	})
}

// GetUploadStatus returns upload status.
func (h *UploadHandler) GetUploadStatus(c *gin.Context) {
	fileMD5 := c.Query("file_md5")
	if fileMD5 == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing file_md5 parameter"})
		return
	}

	claims, _ := c.Get("claims")
	userClaims := claims.(*token.CustomClaims)

	fileName, fileType, uploadedChunks, totalChunks, err := h.uploadService.GetUploadStatus(c.Request.Context(), fileMD5, userClaims.UserID)
	if err != nil {
		if err.Error() == "record not found" {
			c.JSON(http.StatusNotFound, gin.H{
				"code":    http.StatusNotFound,
				"message": "upload record not found",
			})
			return
		}
		log.Error("GetUploadStatus: failed", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get upload status"})
		return
	}

	progress := calculateProgress(uploadedChunks, totalChunks)
	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"message": "success",
		"data": gin.H{
			"fileName":    fileName,
			"fileType":    fileType,
			"uploaded":    uploadedChunks,
			"progress":    progress,
			"totalChunks": totalChunks,
		},
	})
}

// GetSupportedFileTypes returns supported file types.
func (h *UploadHandler) GetSupportedFileTypes(c *gin.Context) {
	types, err := h.uploadService.GetSupportedFileTypes()
	if err != nil {
		log.Error("GetSupportedFileTypes: failed", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get supported file types"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"message": "success",
		"data":    types,
	})
}

// FastUpload handles fast upload.
func (h *UploadHandler) FastUpload(c *gin.Context) {
	var req struct {
		MD5 string `json:"md5"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	claims := c.MustGet("claims").(*token.CustomClaims)
	isUploaded, err := h.uploadService.FastUpload(c.Request.Context(), req.MD5, claims.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check file status"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"uploaded": isUploaded})
}
