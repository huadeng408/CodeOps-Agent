package handler

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"code-agent/internal/middleware"
	"code-agent/internal/serverconfig"
	"code-agent/internal/service"

	"github.com/gin-gonic/gin"
)

type knowledgeIngestUploadCall struct {
	fileMD5   string
	fileName  string
	totalSize int64
	index     int
	chunkMD5  string
	userID    uint
	orgTag    string
	isPublic  bool
	content   []byte
}

type recordingKnowledgeIngestService struct {
	uploadCalls []knowledgeIngestUploadCall
	mergeCalls  int
	mergeMD5    string
	mergeName   string
	mergeUserID uint
	uploadErr   error
	mergeErr    error
	objectURL   string
}

func (s *recordingKnowledgeIngestService) UploadChunk(
	_ context.Context,
	fileMD5, fileName string,
	totalSize int64,
	chunkIndex int,
	file multipart.File,
	chunkMD5 string,
	userID uint,
	orgTag string,
	isPublic bool,
) ([]int, int, error) {
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, err
	}
	s.uploadCalls = append(s.uploadCalls, knowledgeIngestUploadCall{
		fileMD5:   fileMD5,
		fileName:  fileName,
		totalSize: totalSize,
		index:     chunkIndex,
		chunkMD5:  chunkMD5,
		userID:    userID,
		orgTag:    orgTag,
		isPublic:  isPublic,
		content:   append([]byte(nil), content...),
	})
	if s.uploadErr != nil {
		return nil, 0, s.uploadErr
	}
	return []int{chunkIndex}, chunkIndex + 1, nil
}

func (s *recordingKnowledgeIngestService) MergeChunks(_ context.Context, fileMD5, fileName string, userID uint) (string, error) {
	s.mergeCalls++
	s.mergeMD5 = fileMD5
	s.mergeName = fileName
	s.mergeUserID = userID
	if s.mergeErr != nil {
		return "", s.mergeErr
	}
	return s.objectURL, nil
}

func TestKnowledgeIngestRejectsInvalidRequestsWithoutServiceCalls(t *testing.T) {
	tests := []struct {
		name        string
		fields      map[string]string
		fileName    string
		file        []byte
		includeFile bool
	}{
		{name: "missing user id", fields: map[string]string{}, fileName: "doc.pdf", file: []byte("x"), includeFile: true},
		{name: "zero user id", fields: map[string]string{"userId": "0"}, fileName: "doc.pdf", file: []byte("x"), includeFile: true},
		{name: "non numeric user id", fields: map[string]string{"userId": "abc"}, fileName: "doc.pdf", file: []byte("x"), includeFile: true},
		{name: "missing file", fields: map[string]string{"userId": "7"}},
		{name: "empty file", fields: map[string]string{"userId": "7"}, fileName: "doc.pdf", includeFile: true},
		{name: "invalid is public", fields: map[string]string{"userId": "7", "isPublic": "sometimes"}, fileName: "doc.pdf", file: []byte("x"), includeFile: true},
		{name: "numeric true is public", fields: map[string]string{"userId": "7", "isPublic": "1"}, fileName: "doc.pdf", file: []byte("x"), includeFile: true},
		{name: "numeric false is public", fields: map[string]string{"userId": "7", "isPublic": "0"}, fileName: "doc.pdf", file: []byte("x"), includeFile: true},
		{name: "uppercase true is public", fields: map[string]string{"userId": "7", "isPublic": "TRUE"}, fileName: "doc.pdf", file: []byte("x"), includeFile: true},
		{name: "mixed case false is public", fields: map[string]string{"userId": "7", "isPublic": "False"}, fileName: "doc.pdf", file: []byte("x"), includeFile: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &recordingKnowledgeIngestService{}
			response := serveKnowledgeIngest(t, svc, tt.fields, tt.fileName, tt.file, tt.includeFile)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if len(svc.uploadCalls) != 0 || svc.mergeCalls != 0 {
				t.Fatalf("service calls = uploads:%d merges:%d, want none", len(svc.uploadCalls), svc.mergeCalls)
			}
		})
	}
}

func TestKnowledgeIngestUploadsSmallFileAndQueuesIngestion(t *testing.T) {
	content := []byte("small multimodal pdf")
	svc := &recordingKnowledgeIngestService{objectURL: "http://minio.test/object"}
	response := serveKnowledgeIngest(t, svc, map[string]string{
		"userId":   "42",
		"orgTag":   "research",
		"isPublic": "true",
	}, "../../safe.pdf", content, true)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if len(svc.uploadCalls) != 1 {
		t.Fatalf("upload calls = %d, want 1", len(svc.uploadCalls))
	}
	wantMD5 := md5Hex(content)
	call := svc.uploadCalls[0]
	if call.fileMD5 != wantMD5 || call.fileName != "safe.pdf" || call.totalSize != int64(len(content)) || call.index != 0 || call.chunkMD5 != wantMD5 || call.userID != 42 || call.orgTag != "research" || !call.isPublic || !bytes.Equal(call.content, content) {
		t.Fatalf("unexpected upload call: %+v", call)
	}
	if svc.mergeCalls != 1 {
		t.Fatalf("merge calls = %d, want 1", svc.mergeCalls)
	}
	if svc.mergeMD5 != wantMD5 || svc.mergeName != "safe.pdf" || svc.mergeUserID != 42 {
		t.Fatalf("unexpected merge call: md5=%s name=%s user=%d", svc.mergeMD5, svc.mergeName, svc.mergeUserID)
	}

	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			FileMD5   string `json:"fileMd5"`
			FileName  string `json:"fileName"`
			ObjectURL string `json:"objectUrl"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != http.StatusAccepted || envelope.Message != "ingestion queued" || envelope.Data.FileMD5 != wantMD5 || envelope.Data.FileName != "safe.pdf" || envelope.Data.ObjectURL != svc.objectURL {
		t.Fatalf("unexpected response: %+v", envelope)
	}
}

func TestKnowledgeIngestRejectsFileAboveConfiguredLimit(t *testing.T) {
	svc := &recordingKnowledgeIngestService{}
	response := serveKnowledgeIngestWithHandler(
		t,
		newKnowledgeIngestHandler(svc, 1024),
		map[string]string{"userId": "42"},
		"too-large.pdf",
		make([]byte, 1025),
		true,
	)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
	if len(svc.uploadCalls) != 0 || svc.mergeCalls != 0 {
		t.Fatalf("service calls = uploads:%d merges:%d, want none", len(svc.uploadCalls), svc.mergeCalls)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("1024")) {
		t.Fatalf("response leaks configured limit: %s", response.Body.String())
	}
}

func TestKnowledgeIngestNormalizesWindowsStyleFileName(t *testing.T) {
	fileName, ok := safeKnowledgeIngestFileName(`C:\fakepath\evil.pdf`)
	if !ok {
		t.Fatal("Windows-style browser path was rejected")
	}
	if fileName != "evil.pdf" {
		t.Fatalf("file name = %q, want %q", fileName, "evil.pdf")
	}

	svc := &recordingKnowledgeIngestService{objectURL: "http://minio.test/object"}
	response := serveKnowledgeIngest(t, svc, map[string]string{"userId": "42"}, `C:\fakepath\evil.pdf`, []byte("content"), true)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if len(svc.uploadCalls) != 1 || svc.uploadCalls[0].fileName != "evil.pdf" {
		t.Fatalf("unexpected upload calls: %+v", svc.uploadCalls)
	}
}

func TestKnowledgeIngestRejectsUnsafeFileNames(t *testing.T) {
	for _, fileName := range []string{"", ".", "..", "bad\x00name.pdf", "line\nbreak.pdf"} {
		t.Run(strconv.Quote(fileName), func(t *testing.T) {
			if normalized, ok := safeKnowledgeIngestFileName(fileName); ok {
				t.Fatalf("unsafe file name normalized to %q", normalized)
			}
		})
	}
}

func TestKnowledgeIngestFileNameUTF8ByteLimit(t *testing.T) {
	accepted := strings.Repeat("a", 255)
	if normalized, ok := safeKnowledgeIngestFileName(accepted); !ok || normalized != accepted {
		t.Fatalf("255-byte file name rejected or changed: ok=%v len=%d", ok, len([]byte(normalized)))
	}

	for _, fileName := range []string{
		strings.Repeat("a", 256),
		strings.Repeat("文", 86),
	} {
		if normalized, ok := safeKnowledgeIngestFileName(fileName); ok {
			t.Fatalf("%d-byte file name accepted as %q", len([]byte(fileName)), normalized)
		}
	}
}

func TestKnowledgeIngestRejectsFileNameAboveDatabaseLimitWithoutServiceCalls(t *testing.T) {
	svc := &recordingKnowledgeIngestService{}
	response := serveKnowledgeIngest(
		t,
		svc,
		map[string]string{"userId": "42"},
		strings.Repeat("a", 252)+".pdf",
		[]byte("content"),
		true,
	)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if len(svc.uploadCalls) != 0 || svc.mergeCalls != 0 {
		t.Fatalf("service calls = uploads:%d merges:%d, want none", len(svc.uploadCalls), svc.mergeCalls)
	}
}

func TestKnowledgeIngestUploadsLargeFileInOrderedBoundedChunks(t *testing.T) {
	content := make([]byte, service.DefaultChunkSize+17)
	for i := range content {
		content[i] = byte(i % 251)
	}
	svc := &recordingKnowledgeIngestService{objectURL: "http://minio.test/large"}
	response := serveKnowledgeIngest(t, svc, map[string]string{"userId": "9"}, "large.pdf", content, true)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if len(svc.uploadCalls) != 2 {
		t.Fatalf("upload calls = %d, want 2", len(svc.uploadCalls))
	}
	var reassembled []byte
	for index, call := range svc.uploadCalls {
		if call.index != index {
			t.Errorf("call %d chunk index = %d", index, call.index)
		}
		if len(call.content) == 0 || len(call.content) > service.DefaultChunkSize {
			t.Errorf("call %d chunk size = %d", index, len(call.content))
		}
		if call.chunkMD5 != md5Hex(call.content) {
			t.Errorf("call %d chunk MD5 = %s, want %s", index, call.chunkMD5, md5Hex(call.content))
		}
		if call.fileMD5 != md5Hex(content) || call.totalSize != int64(len(content)) {
			t.Errorf("call %d file metadata = md5:%s size:%d", index, call.fileMD5, call.totalSize)
		}
		reassembled = append(reassembled, call.content...)
	}
	if !bytes.Equal(reassembled, content) {
		t.Fatal("uploaded chunks do not reconstruct the original file")
	}
	if svc.mergeCalls != 1 {
		t.Fatalf("merge calls = %d, want 1", svc.mergeCalls)
	}
}

func TestKnowledgeIngestUploadsExactChunkBoundaryAsOneChunk(t *testing.T) {
	content := make([]byte, service.DefaultChunkSize)
	for i := range content {
		content[i] = byte(i % 251)
	}
	svc := &recordingKnowledgeIngestService{objectURL: "http://minio.test/exact"}
	response := serveKnowledgeIngest(t, svc, map[string]string{"userId": "9"}, "exact.pdf", content, true)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if len(svc.uploadCalls) != 1 {
		t.Fatalf("upload calls = %d, want 1", len(svc.uploadCalls))
	}
	if svc.uploadCalls[0].index != 0 || len(svc.uploadCalls[0].content) != service.DefaultChunkSize || svc.uploadCalls[0].chunkMD5 != md5Hex(content) {
		t.Fatalf("unexpected boundary chunk: %+v", svc.uploadCalls[0])
	}
	if svc.mergeCalls != 1 {
		t.Fatalf("merge calls = %d, want 1", svc.mergeCalls)
	}
}

func TestKnowledgeIngestStopsOnUploadError(t *testing.T) {
	svc := &recordingKnowledgeIngestService{uploadErr: errors.New("storage credentials leaked")}
	response := serveKnowledgeIngest(t, svc, map[string]string{"userId": "3"}, "doc.pdf", []byte("content"), true)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if svc.mergeCalls != 0 {
		t.Fatalf("merge calls = %d, want 0", svc.mergeCalls)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("credentials")) {
		t.Fatalf("response leaks internal error: %s", response.Body.String())
	}
}

func TestKnowledgeIngestReturnsInternalErrorWhenMergeFails(t *testing.T) {
	svc := &recordingKnowledgeIngestService{mergeErr: errors.New("private minio detail")}
	response := serveKnowledgeIngest(t, svc, map[string]string{"userId": "3"}, "doc.pdf", []byte("content"), true)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	if len(svc.uploadCalls) != 1 || svc.mergeCalls != 1 {
		t.Fatalf("service calls = uploads:%d merges:%d, want 1 each", len(svc.uploadCalls), svc.mergeCalls)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("minio")) {
		t.Fatalf("response leaks internal error: %s", response.Body.String())
	}
}

func TestKnowledgeIngestInternalRouteRequiresToken(t *testing.T) {
	previousSecret := serverconfig.Conf.AI.Orchestrator.SharedSecret
	serverconfig.Conf.AI.Orchestrator.SharedSecret = "correct-secret"
	t.Cleanup(func() { serverconfig.Conf.AI.Orchestrator.SharedSecret = previousSecret })

	for _, tt := range []struct {
		name       string
		token      string
		wantStatus int
		wantCalls  int
	}{
		{name: "missing token", wantStatus: http.StatusUnauthorized},
		{name: "wrong token", token: "wrong-secret", wantStatus: http.StatusUnauthorized},
		{name: "correct token", token: "correct-secret", wantStatus: http.StatusAccepted, wantCalls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := &recordingKnowledgeIngestService{objectURL: "http://minio.test/auth"}
			response := serveAuthenticatedKnowledgeIngest(t, svc, map[string]string{"userId": "8"}, "doc.pdf", []byte("content"), tt.token)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
			if len(svc.uploadCalls) != tt.wantCalls {
				t.Fatalf("upload calls = %d, want %d", len(svc.uploadCalls), tt.wantCalls)
			}
			if svc.mergeCalls != tt.wantCalls {
				t.Fatalf("merge calls = %d, want %d", svc.mergeCalls, tt.wantCalls)
			}
		})
	}
}

func serveKnowledgeIngest(t *testing.T, svc KnowledgeIngestService, fields map[string]string, fileName string, file []byte, includeFile bool) *httptest.ResponseRecorder {
	t.Helper()
	return serveKnowledgeIngestWithHandler(t, NewKnowledgeIngestHandler(svc), fields, fileName, file, includeFile)
}

func serveKnowledgeIngestWithHandler(t *testing.T, handler *KnowledgeIngestHandler, fields map[string]string, fileName string, file []byte, includeFile bool) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	request := newKnowledgeIngestRequest(t, fields, fileName, file, includeFile)
	recorder := httptest.NewRecorder()
	router := gin.New()
	router.POST("/internal/orchestrator/knowledge-ingest", handler.Ingest)
	router.ServeHTTP(recorder, request)
	return recorder
}

func serveAuthenticatedKnowledgeIngest(t *testing.T, svc KnowledgeIngestService, fields map[string]string, fileName string, file []byte, token string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	request := newKnowledgeIngestRequest(t, fields, fileName, file, true)
	if token != "" {
		request.Header.Set("X-Internal-Token", token)
	}
	recorder := httptest.NewRecorder()
	router := gin.New()
	group := router.Group("/internal")
	group.Use(middleware.InternalAuthMiddleware())
	group.POST("/orchestrator/knowledge-ingest", NewKnowledgeIngestHandler(svc).Ingest)
	router.ServeHTTP(recorder, request)
	return recorder
}

func newKnowledgeIngestRequest(t *testing.T, fields map[string]string, fileName string, file []byte, includeFile bool) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field %s: %v", key, err)
		}
	}
	if includeFile {
		part, err := writer.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatalf("create file field: %v", err)
		}
		if _, err := part.Write(file); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/internal/orchestrator/knowledge-ingest", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func md5Hex(content []byte) string {
	sum := md5.Sum(content)
	return hex.EncodeToString(sum[:])
}
