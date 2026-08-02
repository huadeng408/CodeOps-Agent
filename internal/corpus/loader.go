// Package corpus implements the official technical corpus loader: staged
// checkout, license gate, pilot sampling and idempotent checkpointing.
package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Checkpoint identifies a document that was already processed, keyed by
// source/commit/path/hash so re-runs are idempotent and content changes
// create a new checkpoint instead of overwriting history.
type Checkpoint struct {
	SourceID    string
	SourceCommit string
	Path        string
	ContentHash string
}

// Key returns the stable checkpoint identity.
func (c Checkpoint) Key() string {
	return fmt.Sprintf("%s@%s:%s#%s", c.SourceID, c.SourceCommit, c.Path, c.ContentHash)
}

// SourceGate holds the license and pinning evidence for one source. A source
// only passes the gate when every evidence field is present and non-fake.
type SourceGate struct {
	SourceID      string
	SourceCommit  string // 40-char immutable commit
	LicensePath   string
	LicenseSHA256 string
	LicenseSPDX   string
}

// GateError describes a gate failure that must block import.
type GateError struct {
	SourceID string
	Reason   string
}

func (e *GateError) Error() string {
	return fmt.Sprintf("source %s blocked: %s", e.SourceID, e.Reason)
}

// ValidateSourceGate rejects placeholder/fake evidence (all-zero or floating
// commits, empty license hashes). The caller must have actually verified the
// license file hash against the checked-out commit before calling this — the
// gate only refuses obviously-unpinned evidence.
func ValidateSourceGate(g SourceGate) error {
	if strings.TrimSpace(g.SourceID) == "" {
		return &GateError{SourceID: g.SourceID, Reason: "source_id is empty"}
	}
	if !isCommitSHA(g.SourceCommit) {
		return &GateError{SourceID: g.SourceID, Reason: fmt.Sprintf("source_commit %q is not a 40-char immutable commit", g.SourceCommit)}
	}
	if allZero(g.SourceCommit) {
		return &GateError{SourceID: g.SourceID, Reason: "source_commit is the all-zero placeholder"}
	}
	if strings.TrimSpace(g.LicenseSPDX) == "" {
		return &GateError{SourceID: g.SourceID, Reason: "license_spdx is empty"}
	}
	if !isSHA256Hex(g.LicenseSHA256) {
		return &GateError{SourceID: g.SourceID, Reason: fmt.Sprintf("license_sha256 %q is not a 64-char hex digest", g.LicenseSHA256)}
	}
	if allZero(g.LicenseSHA256) {
		return &GateError{SourceID: g.SourceID, Reason: "license_sha256 is the all-zero placeholder"}
	}
	return nil
}

// HashBytes returns the sha256 hex of data.
func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isCommitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func allZero(s string) bool {
	return s != "" && strings.Trim(s, "0") == ""
}
