package repository

import (
	"os"
	"testing"

	"code-agent/internal/model"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestKnowledgeRepository(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_DB_INTEGRATION") != "1" {
		t.Skip("set CODE_AGENT_RUN_DB_INTEGRATION=1 to run the MySQL integration test")
	}
	dsn := os.Getenv("CODE_AGENT_TEST_MYSQL_DSN")
	if dsn == "" {
		dsn = "codeagent:codeagent@tcp(127.0.0.1:3306)/codeagent?charset=utf8mb4&parseTime=True&loc=Local"
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.KnowledgeSource{}, &model.KnowledgeDocument{}); err != nil {
		t.Fatal(err)
	}

	generation := "integration-test-generation"
	sourceVersionID := model.SourceVersionID("integration-test", "0123456789abcdef0123456789abcdef01234567", generation)
	documentID := model.DocumentID("integration-test", "0123456789abcdef0123456789abcdef01234567", "docs/test.md")
	t.Cleanup(func() {
		db.Where("document_id = ?", documentID).Delete(&model.KnowledgeDocument{})
		db.Where("source_version_id = ?", sourceVersionID).Delete(&model.KnowledgeSource{})
	})

	sourceRepo := NewKnowledgeSourceRepository(db)
	documentRepo := NewKnowledgeDocumentRepository(db)
	source := &model.KnowledgeSource{
		SourceVersionID: sourceVersionID, SourceID: "integration-test", SourceCommit: "0123456789abcdef0123456789abcdef01234567",
		CorpusGeneration: generation, Status: model.SourceStaged,
	}
	firstSource, err := sourceRepo.CreateOrGetSource(source)
	if err != nil {
		t.Fatal(err)
	}
	secondSource, err := sourceRepo.CreateOrGetSource(source)
	if err != nil {
		t.Fatal(err)
	}
	if firstSource.SourceVersionID != secondSource.SourceVersionID || secondSource.Status != model.SourceStaged {
		t.Fatalf("source is not idempotent: first=%+v second=%+v", firstSource, secondSource)
	}
	var sourceCount int64
	if err := db.Model(&model.KnowledgeSource{}).Where("source_version_id = ?", sourceVersionID).Count(&sourceCount).Error; err != nil {
		t.Fatal(err)
	}
	if sourceCount != 1 {
		t.Fatalf("source row count = %d, want 1", sourceCount)
	}

	document := &model.KnowledgeDocument{
		DocumentID: documentID, SourceID: "integration-test", SourcePath: "docs/test.md",
		SourceCommit: "0123456789abcdef0123456789abcdef01234567", DocumentLanguage: "en",
		ContentSHA256:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceVersionID: sourceVersionID, CorpusGeneration: generation,
		TargetIndex: "knowledge_base_v2_bge_m3", Status: model.DocumentStaged,
	}
	firstDocument, err := documentRepo.CreateOrGetDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	secondDocument, err := documentRepo.CreateOrGetDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	if firstDocument.ContentSHA256 != secondDocument.ContentSHA256 || secondDocument.Status != model.DocumentStaged {
		t.Fatalf("document is not idempotent: first=%+v second=%+v", firstDocument, secondDocument)
	}
	var documentCount int64
	if err := db.Model(&model.KnowledgeDocument{}).Where("document_id = ?", documentID).Count(&documentCount).Error; err != nil {
		t.Fatal(err)
	}
	if documentCount != 1 {
		t.Fatalf("document row count = %d, want 1", documentCount)
	}
}
