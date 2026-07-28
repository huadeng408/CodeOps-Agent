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
