package database

import (
	"os"
	"testing"

	"code-agent/internal/model"
	"code-agent/pkg/log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestStructuredChunkColumnsExistInMySQL(t *testing.T) {
	if os.Getenv("CODE_AGENT_RUN_DB_INTEGRATION") != "1" {
		t.Skip("set CODE_AGENT_RUN_DB_INTEGRATION=1 to run the MySQL integration test")
	}
	log.Init("error", "console", "")
	dsn := os.Getenv("CODE_AGENT_TEST_MYSQL_DSN")
	if dsn == "" {
		dsn = "codeagent:codeagent@tcp(127.0.0.1:3306)/codeagent?charset=utf8mb4&parseTime=True&loc=Local"
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	previous := DB
	DB = db
	t.Cleanup(func() { DB = previous })

	if err := db.AutoMigrate(&model.DocumentVector{}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureRuntimeSchema(); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"EmbeddingText", "TokenCount", "SheetName", "CellRange"} {
		if !db.Migrator().HasColumn(&model.DocumentVector{}, field) {
			t.Fatalf("document_vectors is missing %s", field)
		}
	}
}
