// Package database contains shared database clients.
package database

import (
	"code-agent/pkg/log"
	"fmt"
	"strings"
)

// EnsureRuntimeSchema applies lightweight runtime fixes for columns that must
// stay compatible with newer local model names and runtime configuration.
// It is best-effort: a failing statement is logged but does not abort startup,
// because AutoMigrate (called before this in cmd/server) may have already
// created the column in the desired shape on a fresh database.
func EnsureRuntimeSchema() error {
	if DB == nil {
		return fmt.Errorf("database is not initialized")
	}

	statements := []string{
		"ALTER TABLE document_vectors MODIFY COLUMN model_version VARCHAR(128) NULL",
		"ALTER TABLE document_vectors ADD COLUMN document_id VARCHAR(512) NULL",
		"ALTER TABLE document_vectors ADD COLUMN page_id VARCHAR(255) NULL",
		"ALTER TABLE document_vectors ADD COLUMN parent_chunk_id VARCHAR(255) NULL",
		"ALTER TABLE document_vectors ADD COLUMN section_path JSON NULL",
		"ALTER TABLE document_vectors ADD COLUMN page_span JSON NULL",
		"ALTER TABLE document_vectors ADD COLUMN element_ids JSON NULL",
		"ALTER TABLE document_vectors ADD COLUMN element_types JSON NULL",
		"ALTER TABLE document_vectors ADD COLUMN bbox_refs JSON NULL",
		"ALTER TABLE document_vectors ADD COLUMN asset_refs JSON NULL",
		"ALTER TABLE document_vectors ADD COLUMN tokenizer_id VARCHAR(255) NULL",
		"ALTER TABLE document_vectors ADD COLUMN parser_name VARCHAR(128) NULL",
		"ALTER TABLE document_vectors ADD COLUMN parser_version VARCHAR(128) NULL",
		"ALTER TABLE document_vectors ADD COLUMN corpus_generation VARCHAR(128) NULL",
		"ALTER TABLE document_vectors ADD COLUMN target_index VARCHAR(255) NULL",
	}

	for _, stmt := range statements {
		if err := DB.Exec(stmt).Error; err != nil {
			// Tolerate "table doesn't exist" / "column already matches" —
			// these are expected on a fresh or already-migrated schema.
			msg := strings.ToLower(err.Error())
			if strings.Contains(msg, "unknown column") || strings.Contains(msg, "doesn't exist") || strings.Contains(msg, "duplicate") {
				log.Warnf("[migration] non-fatal schema statement skipped: %s err=%v", stmt, err)
				continue
			}
			return err
		}
	}

	return nil
}
