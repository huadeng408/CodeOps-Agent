package session

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
)

// ShadowLedgerForRevisionTest only constructs a TEMP schema fault in an owned
// test connection; it is absent from production builds.
func ShadowLedgerForRevisionTest(ledger *SQLiteEventLog) error {
	_, err := ledger.db.Exec(`CREATE TEMP VIEW session_events AS SELECT session_id, seq, event_id, version, type, created_at, '{}' AS payload, surface_op, prev_checksum, checksum FROM main.session_events`)
	return err
}

func TestLedgerReadRevisionTracksSQLAndConnectionChanges(t *testing.T) {
	for _, mode := range []string{"same_connection_payload", "other_connection_payload", "same_connection_schema", "same_connection_temp_schema", "connection_replaced", "closed_connection"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			ledger := openWorkbenchTestLedger(t)
			if _, err := ledger.Append(ctx, "revision", 0, "fixture/fact", map[string]bool{"original": true}); err != nil {
				t.Fatal(err)
			}
			before, err := ledger.ReadRevision(ctx)
			if err != nil || !before.Matches(before) {
				t.Fatalf("initial native proof: %v", err)
			}
			unchanged, err := ledger.ReadRevision(ctx)
			if err != nil || !before.Matches(unchanged) {
				t.Fatalf("unchanged source proof: %v", err)
			}
			switch mode {
			case "same_connection_payload":
				_, err = ledger.db.ExecContext(ctx, `UPDATE session_events SET payload = '{"changed":true}' WHERE session_id = 'revision'`)
			case "other_connection_payload":
				var other *sql.DB
				other, err = sql.Open("sqlite", ledger.Path)
				if err == nil {
					defer other.Close()
					_, err = other.ExecContext(ctx, `UPDATE session_events SET payload = '{"changed":true}' WHERE session_id = 'revision'`)
				}
			case "same_connection_schema":
				_, err = ledger.db.ExecContext(ctx, `CREATE VIEW revision_schema_fixture AS SELECT 1 AS marker`)
			case "same_connection_temp_schema":
				_, err = ledger.db.ExecContext(ctx, `CREATE TEMP TABLE session_events AS SELECT * FROM main.session_events`)
			case "connection_replaced":
				var connection *sql.Conn
				connection, err = ledger.db.Conn(ctx)
				if err == nil {
					_ = connection.Raw(func(any) error { return driver.ErrBadConn })
					_ = connection.Close()
				}
			case "closed_connection":
				err = ledger.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			after, err := ledger.ReadRevision(ctx)
			if mode == "closed_connection" {
				if err == nil || before.Matches(after) {
					t.Fatal("closed reader retained a usable proof")
				}
				return
			}
			if err != nil || before.Matches(after) {
				t.Fatalf("SQL/connection change reused proof: %v", err)
			}
			_, readErr := ReadVerifiedSnapshot(ctx, ledger, "revision")
			if mode == "same_connection_payload" || mode == "other_connection_payload" {
				if !errors.Is(readErr, ErrEventIntegrity) {
					t.Fatalf("mutated old payload not rejected: %v", readErr)
				}
			} else if readErr != nil {
				t.Fatalf("legitimate source failed after connection/schema change: %v", readErr)
			}
		})
	}
}
