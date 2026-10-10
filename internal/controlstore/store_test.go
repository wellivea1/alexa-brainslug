package controlstore

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/wellivea1/alexa-brainslug/internal/codex"
)

func TestDatabasesRejectOtherServicesAndNewerMigrations(t *testing.T) {
	for _, kind := range []string{"voice", "controller", "worker", "future-controller", "future-worker"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "isolated.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			statements := map[string][]string{"voice": {`CREATE TABLE interactions(id INTEGER)`}, "controller": {`CREATE TABLE control_jobs(id TEXT)`}, "worker": {`CREATE TABLE worker_attempts(id TEXT)`}, "future-controller": {`CREATE TABLE control_migrations(version INTEGER PRIMARY KEY)`, `INSERT INTO control_migrations VALUES(99)`}, "future-worker": {`CREATE TABLE worker_migrations(version INTEGER PRIMARY KEY)`, `INSERT INTO worker_migrations VALUES(99)`}}
			for _, statement := range statements[kind] {
				if _, err := db.ExecContext(t.Context(), statement); err != nil {
					_ = db.Close()
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			if kind == "voice" || kind == "worker" || kind == "future-controller" {
				if store, err := Open(t.Context(), path); err == nil {
					_ = store.Close()
					t.Fatal("controller accepted incompatible operational storage")
				}
			}
			if kind == "voice" || kind == "controller" || kind == "future-worker" {
				if ledger, err := codex.OpenLedger(t.Context(), path); err == nil {
					_ = ledger.Close()
					t.Fatal("worker accepted incompatible operational storage")
				}
			}
		})
	}
}
