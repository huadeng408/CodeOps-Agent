// Package service contains application business logic.
package service

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"code-agent/internal/serverconfig"
	"code-agent/internal/model"
	"code-agent/internal/repository"
	"code-agent/pkg/database"
	"code-agent/pkg/es"
	"code-agent/pkg/objectpath"
	"code-agent/pkg/storage"
	"code-agent/pkg/tika"

	"github.com/minio/minio-go/v7"
)

// FileUploadDTO is returned to the frontend with resolved organization tag names.
type FileUploadDTO struct {
	model.FileUpload
	OrgTagName string `json:"orgTagName"`
}

// DownloadInfoDTO contains temporary download information for a file.
type DownloadInfoDTO struct {
	FileName    string `json:"fileName"`
	DownloadURL string `json:"downloadUrl"`
	FileSize    int64  `json:"fileSize"`
}

// PreviewInfoDTO contains text preview information for a file.
type PreviewInfoDTO struct {
	FileName string `json:"fileName"`
	Content  string `json:"content"`
	FileSize int64  `json:"fileSize"`
}

// DocumentService defines document management operations.
type DocumentService interface {
	ListAccessibleFiles(user *model.User) ([]model.FileUpload, error)
	ListUploadedFiles(userID uint) ([]FileUploadDTO, error)
	DeleteDocument(fileMD5 string, user *model.User) error
	GenerateDownloadURL(fileName string, user *model.User) (*DownloadInfoDTO, error)
	GetFilePreviewContent(fileName string, user *model.User) (*PreviewInfoDTO, error)
}

// documentService implements document operations.
type documentService struct {
	uploadRepo       repository.UploadRepository
	userRepo         repository.UserRepository
	orgTagRepo       repository.OrgTagRepository
	docVectorRepo    repository.DocumentVectorRepository
	pipelineTaskRepo repository.PipelineTaskRepository
	minioCfg         serverconfig.MinIOConfig
	esIndexName      string
	tikaClient       *tika.Client
}

// NewDocumentService creates a DocumentService.
func NewDocumentService(
	uploadRepo repository.UploadRepository,
	userRepo repository.UserRepository,
	orgTagRepo repository.OrgTagRepository,
	docVectorRepo repository.DocumentVectorRepository,
	pipelineTaskRepo repository.PipelineTaskRepository,
	minioCfg serverconfig.MinIOConfig,
	esIndexName string,
	tikaClient *tika.Client,
) DocumentService {
	return &documentService{
		uploadRepo:       uploadRepo,
		userRepo:         userRepo,
		orgTagRepo:       orgTagRepo,
		docVectorRepo:    docVectorRepo,
		pipelineTaskRepo: pipelineTaskRepo,
		minioCfg:         minioCfg,
		esIndexName:      esIndexName,
		tikaClient:       tikaClient,
	}
}

// ListAccessibleFiles returns files the user can access.
func (s *documentService) ListAccessibleFiles(user *model.User) ([]model.FileUpload, error) {
	orgTags := strings.Split(user.OrgTags, ",")
	return s.uploadRepo.FindAccessibleFiles(user.ID, orgTags)
}

// ListUploadedFiles returns files uploaded by one user.
func (s *documentService) ListUploadedFiles(userID uint) ([]FileUploadDTO, error) {
	files, err := s.uploadRepo.FindFilesByUserID(userID)
	if err != nil {
		return nil, err
	}

	dtos, err := s.mapFileUploadsToDTOs(files)
	if err != nil {
		return nil, err
	}

	return dtos, nil
}

// DeleteDocument deletes a document and its derived search artifacts.
func (s *documentService) DeleteDocument(fileMD5 string, user *model.User) error {
	record, err := s.uploadRepo.GetFileUploadRecordByMD5(fileMD5)
	if err != nil {
		return errors.New("file not found or not accessible")
	}

	if record.UserID != user.ID && user.Role != "ADMIN" {
		return errors.New("permission denied to delete this file")
	}

	ctx := context.Background()
	if err := es.DeleteDocumentsByFileMD5(ctx, s.esIndexName, fileMD5); err != nil {
		return err
	}
	if err := s.docVectorRepo.DeleteByFileMD5(fileMD5); err != nil {
		return err
	}
	if err := s.uploadRepo.DeleteChunkInfoByFileMD5(fileMD5); err != nil {
		return err
	}
	if err := s.pipelineTaskRepo.DeleteByFileMD5(fileMD5); err != nil {
		return err
	}

	_ = s.uploadRepo.DeleteUploadMark(ctx, fileMD5, record.UserID)
	_ = database.RDB.Del(ctx, "pipeline:embeddings:"+fileMD5).Err()

	objectName := objectpath.MergedObjectName(record.FileMD5, record.FileName)
	_ = storage.MinioClient.RemoveObject(ctx, s.minioCfg.BucketName, objectName, minio.RemoveObjectOptions{})
	_ = storage.MinioClient.RemoveObject(ctx, s.minioCfg.BucketName, objectpath.ParsedObjectName(fileMD5), minio.RemoveObjectOptions{})

	if user.Role == "ADMIN" {
		return s.uploadRepo.DeleteFileUploadRecordByMD5(fileMD5)
	}
	return s.uploadRepo.DeleteFileUploadRecord(fileMD5, record.UserID)
}

// GenerateDownloadURL creates a temporary download URL for a file.
func (s *documentService) GenerateDownloadURL(fileName string, user *model.User) (*DownloadInfoDTO, error) {
	files, err := s.ListAccessibleFiles(user)
	if err != nil {
		return nil, err
	}

	var targetFile *model.FileUpload
	for i := range files {
		if files[i].FileName == fileName {
			targetFile = &files[i]
			break
		}
	}

	if targetFile == nil {
		return nil, errors.New("file not found or not accessible")
	}

	expiry := time.Hour
	objectName := objectpath.MergedObjectName(targetFile.FileMD5, targetFile.FileName)
	presignedURL, err := storage.MinioClient.PresignedGetObject(context.Background(), s.minioCfg.BucketName, objectName, expiry, url.Values{})
	if err != nil {
		return nil, err
	}

	return &DownloadInfoDTO{
		FileName:    targetFile.FileName,
		DownloadURL: presignedURL.String(),
		FileSize:    targetFile.TotalSize,
	}, nil
}

// GetFilePreviewContent extracts text preview content for a file.
func (s *documentService) GetFilePreviewContent(fileName string, user *model.User) (*PreviewInfoDTO, error) {
	files, err := s.ListAccessibleFiles(user)
	if err != nil {
		return nil, err
	}

	var targetFile *model.FileUpload
	for i := range files {
		if files[i].FileName == fileName {
			targetFile = &files[i]
			break
		}
	}

	if targetFile == nil {
		return nil, errors.New("file not found or not accessible")
	}

	objectName := objectpath.MergedObjectName(targetFile.FileMD5, targetFile.FileName)
	object, err := storage.MinioClient.GetObject(context.Background(), s.minioCfg.BucketName, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer object.Close()

	content, err := s.tikaClient.ExtractText(object, fileName)
	if err != nil {
		return nil, err
	}

	return &PreviewInfoDTO{
		FileName: targetFile.FileName,
		Content:  content,
		FileSize: targetFile.TotalSize,
	}, nil
}

// mapFileUploadsToDTOs resolves organization tag names for file uploads.
func (s *documentService) mapFileUploadsToDTOs(files []model.FileUpload) ([]FileUploadDTO, error) {
	if len(files) == 0 {
		return []FileUploadDTO{}, nil
	}

	tagIDs := make(map[string]struct{})
	for _, file := range files {
		if file.OrgTag != "" {
			tagIDs[file.OrgTag] = struct{}{}
		}
	}

	tagIDList := make([]string, 0, len(tagIDs))
	for id := range tagIDs {
		tagIDList = append(tagIDList, id)
	}

	tags, err := s.orgTagRepo.FindBatchByIDs(tagIDList)
	if err != nil {
		return nil, err
	}

	tagMap := make(map[string]string)
	for _, tag := range tags {
		tagMap[tag.TagID] = tag.Name
	}

	dtos := make([]FileUploadDTO, len(files))
	for i, file := range files {
		dtos[i] = FileUploadDTO{
			FileUpload: file,
			OrgTagName: tagMap[file.OrgTag],
		}
	}

	return dtos, nil
}
