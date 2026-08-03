package handler

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
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

const (
	testLoaderUser       = 42
	testCorpusGeneration = "techdocs-test-v1"
	testTargetIndex      = "knowledge_base_test_idx"
	// 40 lowercase hex git commit.
	testCommitHex = "abcdef0123456789abcdef0123456789abcdef01"
	// defaultTestObjectURL is the presigned URL the fake storage hands back.
	defaultTestObjectURL = "http://minio.test/merged/object"
	// defaultTestDocumentID is the document ID the recording service returns.
	defaultTestDocumentID = "go@abcdef0123456789abcdef0123456789abcdef01:techdocs-test-v1:doc/asm.html"
)

// recordingCorpusIngestService implements service.CorpusIngestService and records
// the last Ingest call so handler tests can assert on the forwarded payload. A
// non-nil ingestErr is returned verbatim; otherwise result (or a default) is
// returned so the handler can echo a document_id.
type recordingCorpusIngestService struct {
	ingestCalls []service.CorpusIngestRequest
	ingestErr   error
	result      *service.CorpusIngestResult
}

func (s *recordingCorpusIngestService) Ingest(_ context.Context, req service.CorpusIngestRequest) (*service.CorpusIngestResult, error) {
	s.ingestCalls = append(s.ingestCalls, req)
	if s.ingestErr != nil {
		return nil, s.ingestErr
	}
	if s.result != nil {
		return s.result, nil
	}
	return &service.CorpusIngestResult{
		FileMD5:    req.FileMD5,
		FileName:   req.FileName,
		ObjectURL:  req.ObjectURL,
		DocumentID: defaultTestDocumentID,
	}, nil
}

// fakeKnowledgeStorage records PutObject/presign interactions so the handler
// test exercises the merged-object write without a live MinIO client.
type fakeKnowledgeStorage struct {
	putCalls     []fakeStoragePutCall
	presignCalls []fakeStoragePresignCall
	presignedURL string
	putErr       error
	presignErr   error
}

type fakeStoragePutCall struct {
	bucket  string
	object  string
	size    int64
	content []byte
}

type fakeStoragePresignCall struct {
	bucket string
	object string
}

func (s *fakeKnowledgeStorage) put(_ context.Context, bucket, object string, reader io.Reader, size int64) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	s.putCalls = append(s.putCalls, fakeStoragePutCall{
		bucket:  bucket,
		object:  object,
		size:    size,
		content: append([]byte(nil), data...),
	})
	if s.putErr != nil {
		return s.putErr
	}
	return nil
}

func (s *fakeKnowledgeStorage) presign(bucket, object string) (string, error) {
	s.presignCalls = append(s.presignCalls, fakeStoragePresignCall{bucket: bucket, object: object})
	if s.presignErr != nil {
		return "", s.presignErr
	}
	return s.presignedURL, nil
}

func (s *fakeKnowledgeStorage) apply(h *KnowledgeIngestHandler) {
	h.putMerged = s.put
	h.presign = s.presign
}

func testMinIOConfig() serverconfig.MinIOConfig {
	return serverconfig.MinIOConfig{BucketName: "test-bucket"}
}

// newValidCorpusFields builds the full multipart form for a legal corpus request
// whose sourceSha256 matches the handler-computed SHA-256 of file.
func newValidCorpusFields(file []byte) map[string]string {
	return map[string]string{
		"userId":           strconv.Itoa(testLoaderUser),
		"orgTag":           "corpus",
		"isPublic":         "true",
		"sourceId":         "go",
		"sourcePath":       "doc/asm.html",
		"sourceUrl":        "https://example.test/go/blob/abc/doc/asm.html",
		"sourceCommit":     testCommitHex,
		"corpusGeneration": testCorpusGeneration,
		"targetIndex":      testTargetIndex,
		"sourceSha256":     sha256Hex(file),
		"runId":            "import-123",
	}
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func TestKnowledgeIngestQueuesCorpusDocument(t *testing.T) {
	content := []byte("<html>real asm payload</html>")
	svc := &recordingCorpusIngestService{}
	storage := &fakeKnowledgeStorage{presignedURL: defaultTestObjectURL}

	response := serveKnowledgeIngestWithStorage(
		t, svc, storage,
		newValidCorpusFields(content),
		"doc/asm.html",
		content,
		true,
	)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if len(svc.ingestCalls) != 1 {
		t.Fatalf("service ingest calls = %d, want 1", len(svc.ingestCalls))
	}

	call := svc.ingestCalls[0]
	wantSHA := sha256Hex(content)
	wantMD5 := md5Hex(content)
	if call.ContentSHA256 != wantSHA {
		t.Errorf("ContentSHA256 = %q, want raw-bytes sha256 %q", call.ContentSHA256, wantSHA)
	}
	if call.FileMD5 != wantMD5 {
		t.Errorf("FileMD5 = %q, want %q", call.FileMD5, wantMD5)
	}
	if call.UserID != testLoaderUser {
		t.Errorf("UserID = %d, want %d", call.UserID, testLoaderUser)
	}
	if call.FileName != "asm.html" {
		t.Errorf("FileName = %q, want %q", call.FileName, "asm.html")
	}
	if call.TotalSize != int64(len(content)) {
		t.Errorf("TotalSize = %d, want %d", call.TotalSize, len(content))
	}
	if call.OrgTag != "corpus" || !call.IsPublic {
		t.Errorf("OrgTag=%q IsPublic=%v, want corpus/true", call.OrgTag, call.IsPublic)
	}
	if call.ObjectURL == "" || call.ObjectURL != defaultTestObjectURL {
		t.Errorf("ObjectURL = %q, want non-empty %q", call.ObjectURL, defaultTestObjectURL)
	}

	prov := call.Provenance
	if prov.SourceID != "go" ||
		prov.SourcePath != "doc/asm.html" ||
		prov.SourceCommit != testCommitHex ||
		prov.CorpusGeneration != testCorpusGeneration ||
		prov.TargetIndex != testTargetIndex ||
		prov.SourceSHA256 != wantSHA ||
		prov.SourceURL != "https://example.test/go/blob/abc/doc/asm.html" {
		t.Errorf("unexpected provenance: %+v", prov)
	}

	// The handler must forward the form's runId into CorpusIngestRequest.RunID
	// so the enqueued task is run-scoped and the consumer's run-aware dedup
	// bypasses any stale historical SUCCESS for this file_md5.
	if call.RunID != "import-123" {
		t.Errorf("RunID = %q, want %q (handler must forward form runId)", call.RunID, "import-123")
	}

	if len(storage.putCalls) != 1 {
		t.Fatalf("storage put calls = %d, want 1", len(storage.putCalls))
	}
	put := storage.putCalls[0]
	if put.bucket != "test-bucket" {
		t.Errorf("put bucket = %q, want test-bucket", put.bucket)
	}
	if !bytes.Equal(put.content, content) {
		t.Errorf("put content does not match uploaded bytes (size=%d want=%d)", len(put.content), len(content))
	}
	if put.size != int64(len(content)) {
		t.Errorf("put size = %d, want %d", put.size, len(content))
	}
	if len(storage.presignCalls) != 1 || storage.presignCalls[0].object != put.object {
		t.Errorf("presign must be called once on the merged object: %+v", storage.presignCalls)
	}

	var envelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			FileMD5    string `json:"fileMd5"`
			FileName   string `json:"fileName"`
			ObjectURL  string `json:"objectUrl"`
			DocumentID string `json:"documentId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != http.StatusAccepted || envelope.Message != "ingestion queued" {
		t.Fatalf("unexpected envelope: %+v", envelope)
	}
	if envelope.Data.FileMD5 != wantMD5 || envelope.Data.FileName != "asm.html" || envelope.Data.ObjectURL != defaultTestObjectURL {
		t.Errorf("unexpected response data: %+v", envelope.Data)
	}
	if envelope.Data.DocumentID == "" || envelope.Data.DocumentID != defaultTestDocumentID {
		t.Errorf("documentId = %q, want non-empty %q", envelope.Data.DocumentID, defaultTestDocumentID)
	}
}

// TestKnowledgeIngestOmitsRunIDWhenAbsent is the backward-compatibility guard:
// runId is optional on the wire. A request that omits it must still be accepted
// (202) and reach the service with an empty RunID — legacy callers that predate
// the run-scoped dedup are not forced to send it.
func TestKnowledgeIngestOmitsRunIDWhenAbsent(t *testing.T) {
	content := []byte("<html>real asm payload</html>")
	svc := &recordingCorpusIngestService{}
	storage := &fakeKnowledgeStorage{presignedURL: defaultTestObjectURL}

	fields := newValidCorpusFields(content)
	delete(fields, "runId")

	response := serveKnowledgeIngestWithStorage(t, svc, storage, fields, "doc/asm.html", content, true)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if len(svc.ingestCalls) != 1 {
		t.Fatalf("service ingest calls = %d, want 1", len(svc.ingestCalls))
	}
	if svc.ingestCalls[0].RunID != "" {
		t.Errorf("RunID = %q, want empty when form omits runId", svc.ingestCalls[0].RunID)
	}
}

func TestKnowledgeIngestRejectsInvalidRequestsWithoutServiceCalls(t *testing.T) {
	cases := []struct {
		name        string
		base        map[string]string
		omit        string
		fileName    string
		file        []byte
		includeFile bool
	}{
		{name: "missing user id", base: newValidCorpusFields([]byte("x")), omit: "userId", fileName: "asm.html", file: []byte("x"), includeFile: true},
		{name: "zero user id", base: withField(newValidCorpusFields([]byte("x")), "userId", "0"), fileName: "asm.html", file: []byte("x"), includeFile: true},
		{name: "non numeric user id", base: withField(newValidCorpusFields([]byte("x")), "userId", "abc"), fileName: "asm.html", file: []byte("x"), includeFile: true},
		{name: "missing file", base: newValidCorpusFields(nil), fileName: "asm.html", includeFile: false},
		{name: "empty file", base: newValidCorpusFields([]byte("")), fileName: "asm.html", file: []byte(""), includeFile: true},
		{name: "invalid is public", base: withField(newValidCorpusFields([]byte("x")), "isPublic", "sometimes"), fileName: "asm.html", file: []byte("x"), includeFile: true},
		{name: "missing sourceId", base: newValidCorpusFields([]byte("x")), omit: "sourceId", fileName: "asm.html", file: []byte("x"), includeFile: true},
		{name: "missing sourcePath", base: newValidCorpusFields([]byte("x")), omit: "sourcePath", fileName: "asm.html", file: []byte("x"), includeFile: true},
		{name: "missing sourceCommit", base: newValidCorpusFields([]byte("x")), omit: "sourceCommit", fileName: "asm.html", file: []byte("x"), includeFile: true},
		{name: "missing corpusGeneration", base: newValidCorpusFields([]byte("x")), omit: "corpusGeneration", fileName: "asm.html", file: []byte("x"), includeFile: true},
		{name: "missing sourceSha256", base: newValidCorpusFields([]byte("x")), omit: "sourceSha256", fileName: "asm.html", file: []byte("x"), includeFile: true},
		{name: "missing targetIndex", base: newValidCorpusFields([]byte("x")), omit: "targetIndex", fileName: "asm.html", file: []byte("x"), includeFile: true},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			fields := tt.base
			if tt.omit != "" {
				delete(fields, tt.omit)
			}
			svc := &recordingCorpusIngestService{}
			response := serveKnowledgeIngest(t, svc, fields, tt.fileName, tt.file, tt.includeFile)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			if len(svc.ingestCalls) != 0 {
				t.Fatalf("service ingest calls = %d, want 0 (validation must reject before service)", len(svc.ingestCalls))
			}
		})
	}
}

// TestKnowledgeIngestRejectsForgedSourceSHA256 is the anti-forgery guard: a
// caller cannot route unrelated content into the corpus index by declaring a
// mismatched source hash. The handler computes the hash over the raw bytes it
// received and must 400 before touching storage or the service.
func TestKnowledgeIngestRejectsForgedSourceSHA256(t *testing.T) {
	content := []byte("actual bytes on the wire")
	declaredSHA := sha256Hex([]byte("completely different content"))
	if declaredSHA == sha256Hex(content) {
		t.Fatal("test setup invariant broken: declared hash must differ from actual")
	}
	fields := newValidCorpusFields(content)
	fields["sourceSha256"] = declaredSHA

	svc := &recordingCorpusIngestService{}
	storage := &fakeKnowledgeStorage{presignedURL: defaultTestObjectURL}
	response := serveKnowledgeIngestWithStorage(t, svc, storage, fields, "asm.html", content, true)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if len(svc.ingestCalls) != 0 {
		t.Fatalf("service ingest calls = %d, want 0", len(svc.ingestCalls))
	}
	if len(storage.putCalls) != 0 {
		t.Fatalf("storage put calls = %d, want 0 (no object write on hash mismatch)", len(storage.putCalls))
	}
}

func TestKnowledgeIngestMapsValidationErrorsToBadRequest(t *testing.T) {
	content := []byte("payload")
	svc := &recordingCorpusIngestService{ingestErr: service.ErrCorpusValidation}
	response := serveKnowledgeIngest(t, svc, newValidCorpusFields(content), "asm.html", content, true)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if len(svc.ingestCalls) != 1 {
		t.Fatalf("service should be invoked once for validation mapping, got %d", len(svc.ingestCalls))
	}
	if bytes.Contains(response.Body.Bytes(), []byte("validation")) || bytes.Contains(response.Body.Bytes(), []byte("mismatch")) {
		t.Fatalf("response leaks internal validation detail: %s", response.Body.String())
	}
}

// TestKnowledgeIngestMapsLoaderUserMismatchToBadRequest mirrors the (f)
// scenario: the handler passes userId through; the service is the authority
// and rejects a non-loader user with ErrCorpusValidation, which the handler
// must surface as 400.
func TestKnowledgeIngestMapsLoaderUserMismatchToBadRequest(t *testing.T) {
	content := []byte("payload")
	fields := newValidCorpusFields(content)
	fields["userId"] = strconv.Itoa(testLoaderUser + 7)
	svc := &recordingCorpusIngestService{ingestErr: service.ErrCorpusValidation}
	response := serveKnowledgeIngest(t, svc, fields, "asm.html", content, true)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadRequest)
	}
	if len(svc.ingestCalls) != 1 || svc.ingestCalls[0].UserID != testLoaderUser+7 {
		t.Fatalf("service should observe the forwarded user id, got %+v", svc.ingestCalls)
	}
}

func TestKnowledgeIngestMapsServiceErrorsToInternalError(t *testing.T) {
	content := []byte("payload")
	svc := &recordingCorpusIngestService{ingestErr: errors.New("kafka broker credentials exploded")}
	response := serveKnowledgeIngest(t, svc, newValidCorpusFields(content), "asm.html", content, true)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if len(svc.ingestCalls) != 1 {
		t.Fatalf("service should be invoked once, got %d", len(svc.ingestCalls))
	}
	for _, leak := range []string{"kafka", "credentials", "exploded", "broker"} {
		if bytes.Contains(response.Body.Bytes(), []byte(leak)) {
			t.Fatalf("response leaks internal error %q: %s", leak, response.Body.String())
		}
	}
}

func TestKnowledgeIngestReturnsInternalErrorWhenStoragePutFails(t *testing.T) {
	content := []byte("payload")
	svc := &recordingCorpusIngestService{}
	storage := &fakeKnowledgeStorage{
		presignedURL: defaultTestObjectURL,
		putErr:       errors.New("minio putobject 503 slow down"),
	}
	response := serveKnowledgeIngestWithStorage(t, svc, storage, newValidCorpusFields(content), "asm.html", content, true)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if len(svc.ingestCalls) != 0 {
		t.Fatalf("service ingest calls = %d, want 0 on storage failure", len(svc.ingestCalls))
	}
	if bytes.Contains(response.Body.Bytes(), []byte("minio")) {
		t.Fatalf("response leaks storage detail: %s", response.Body.String())
	}
}

func TestKnowledgeIngestReturnsInternalErrorWhenPresignFails(t *testing.T) {
	content := []byte("payload")
	svc := &recordingCorpusIngestService{}
	storage := &fakeKnowledgeStorage{
		presignErr: errors.New("minio presign endpoint unreachable"),
	}
	response := serveKnowledgeIngestWithStorage(t, svc, storage, newValidCorpusFields(content), "asm.html", content, true)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusInternalServerError, response.Body.String())
	}
	if len(svc.ingestCalls) != 0 {
		t.Fatalf("service ingest calls = %d, want 0 on presign failure", len(svc.ingestCalls))
	}
}

func TestKnowledgeIngestRejectsFileAboveConfiguredLimit(t *testing.T) {
	content := make([]byte, 1025)
	svc := &recordingCorpusIngestService{}
	storage := &fakeKnowledgeStorage{presignedURL: defaultTestObjectURL}

	handler := newKnowledgeIngestHandler(svc, testMinIOConfig(), 1024)
	storage.apply(handler)
	response := serveKnowledgeIngestWithHandler(t, handler, newValidCorpusFields(content), "too-large.html", content, true)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
	if len(svc.ingestCalls) != 0 {
		t.Fatalf("service ingest calls = %d, want 0", len(svc.ingestCalls))
	}
	if bytes.Contains(response.Body.Bytes(), []byte("1024")) {
		t.Fatalf("response leaks configured limit: %s", response.Body.String())
	}
}

func TestKnowledgeIngestNormalizesWindowsStyleFileName(t *testing.T) {
	fileName, ok := safeKnowledgeIngestFileName(`C:\fakepath\asm.html`)
	if !ok {
		t.Fatal("Windows-style browser path was rejected")
	}
	if fileName != "asm.html" {
		t.Fatalf("file name = %q, want %q", fileName, "asm.html")
	}

	content := []byte("content")
	svc := &recordingCorpusIngestService{}
	response := serveKnowledgeIngest(t, svc, newValidCorpusFields(content), `C:\fakepath\asm.html`, content, true)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusAccepted, response.Body.String())
	}
	if len(svc.ingestCalls) != 1 || svc.ingestCalls[0].FileName != "asm.html" {
		t.Fatalf("unexpected ingest call: %+v", svc.ingestCalls)
	}
}

func TestKnowledgeIngestRejectsUnsafeFileNames(t *testing.T) {
	for _, fileName := range []string{"", ".", "..", "bad\x00name.html", "line\nbreak.html"} {
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

func TestKnowledgeIngestInternalRouteRequiresToken(t *testing.T) {
	previousSecret := serverconfig.Conf.AI.Orchestrator.SharedSecret
	serverconfig.Conf.AI.Orchestrator.SharedSecret = "correct-secret"
	t.Cleanup(func() { serverconfig.Conf.AI.Orchestrator.SharedSecret = previousSecret })

	content := []byte("payload")
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
			svc := &recordingCorpusIngestService{}
			response := serveAuthenticatedKnowledgeIngest(t, svc, newValidCorpusFields(content), "asm.html", content, tt.token)
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, response.Body.String())
			}
			if len(svc.ingestCalls) != tt.wantCalls {
				t.Fatalf("service ingest calls = %d, want %d", len(svc.ingestCalls), tt.wantCalls)
			}
		})
	}
}

// withField returns a shallow copy of fields with key set to value.
func withField(fields map[string]string, key, value string) map[string]string {
	out := make(map[string]string, len(fields)+1)
	for k, v := range fields {
		out[k] = v
	}
	out[key] = value
	return out
}

func serveKnowledgeIngest(t *testing.T, svc service.CorpusIngestService, fields map[string]string, fileName string, file []byte, includeFile bool) *httptest.ResponseRecorder {
	t.Helper()
	return serveKnowledgeIngestWithStorage(t, svc, nil, fields, fileName, file, includeFile)
}

func serveKnowledgeIngestWithStorage(t *testing.T, svc service.CorpusIngestService, storage *fakeKnowledgeStorage, fields map[string]string, fileName string, file []byte, includeFile bool) *httptest.ResponseRecorder {
	t.Helper()
	h := NewKnowledgeIngestHandler(svc, testMinIOConfig())
	if storage == nil {
		storage = &fakeKnowledgeStorage{presignedURL: defaultTestObjectURL}
	}
	storage.apply(h)
	return serveKnowledgeIngestWithHandler(t, h, fields, fileName, file, includeFile)
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

func serveAuthenticatedKnowledgeIngest(t *testing.T, svc service.CorpusIngestService, fields map[string]string, fileName string, file []byte, token string) *httptest.ResponseRecorder {
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
	handler := NewKnowledgeIngestHandler(svc, testMinIOConfig())
	(&fakeKnowledgeStorage{presignedURL: defaultTestObjectURL}).apply(handler)
	group.POST("/orchestrator/knowledge-ingest", handler.Ingest)
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
