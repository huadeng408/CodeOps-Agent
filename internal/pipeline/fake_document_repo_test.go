package pipeline

import (
	"code-agent/internal/model"

	"gorm.io/gorm"
)

// fakeDocumentRepo records MarkDocumentStatus calls so pipeline lifecycle
// tests can assert the knowledge_document status transitions driven by the
// processor. It mirrors the in-memory fake already used by the service tests.
type fakeDocumentRepo struct {
	markCalls []fakeDocMarkCall
	err       error
}

type fakeDocMarkCall struct {
	documentID string
	status     model.KnowledgeDocumentStatus
	lastError  string
}

func (f *fakeDocumentRepo) CreateOrGetDocument(document *model.KnowledgeDocument) (*model.KnowledgeDocument, error) {
	if f.err != nil {
		return nil, f.err
	}
	return document, nil
}

func (f *fakeDocumentRepo) MarkDocumentStatus(documentID string, status model.KnowledgeDocumentStatus, lastError string) error {
	f.markCalls = append(f.markCalls, fakeDocMarkCall{documentID: documentID, status: status, lastError: lastError})
	return nil
}

func (f *fakeDocumentRepo) ListDocumentsByGeneration(string) ([]model.KnowledgeDocument, error) {
	return nil, nil
}

func (f *fakeDocumentRepo) ListDocumentsByGenerationAndStatus(string, []string) ([]model.KnowledgeDocument, error) {
	return nil, nil
}

func (f *fakeDocumentRepo) CountActiveDocuments(string) (int64, error) {
	return 0, nil
}

func (f *fakeDocumentRepo) GetDocumentByFileMD5(string) (*model.KnowledgeDocument, error) {
	return nil, gorm.ErrRecordNotFound
}
