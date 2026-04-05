// Package store provides all SQLite database operations for sentinel.
// Must-NOT make network calls or access filesystem beyond the DB file.
package store

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGo)
)

// DB wraps *sql.DB and exposes all store implementations.
type DB struct {
	sql *sql.DB

	PRs            *PRStore
	Findings       *FindingStore
	Resolutions    *ResolutionStore
	ApprovedValues *ApprovedValueStore
	SyncRuns       *SyncRunStore
	Tasks          *TaskStore
	Actions        *ActionStore
	Confirmations  *ConfirmationStore
	Reviews        *ReviewStore
	WebhookEvents  *WebhookEventStore
	DocState       *DocStateStore
}

// Open opens the SQLite database at dsn, applies WAL pragmas, runs schema
// migrations, and returns a fully initialised *DB.
//
// Critical constraint: the DB file MUST reside on a Longhorn RWO PVC.
// SQLite requires exclusive file-level access; RWX PVCs will corrupt the DB.
func Open(dsn string) (*DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dsn, err)
	}

	// Single writer; Go's sql pool manages connections internally.
	// WAL allows multiple concurrent readers and one writer.
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %q: %w", p, err)
		}
	}

	if err := runMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	s := &DB{sql: db}
	s.PRs = &PRStore{db: db}
	s.Findings = &FindingStore{db: db}
	s.Resolutions = &ResolutionStore{db: db}
	s.ApprovedValues = &ApprovedValueStore{db: db}
	s.SyncRuns = &SyncRunStore{db: db}
	s.Tasks = &TaskStore{db: db}
	s.Actions = &ActionStore{db: db}
	s.Confirmations = &ConfirmationStore{db: db}
	s.Reviews = &ReviewStore{db: db}
	s.WebhookEvents = &WebhookEventStore{db: db}
	s.DocState = &DocStateStore{db: db}

	return s, nil
}

// Close closes the underlying database, flushing the WAL checkpoint.
func (d *DB) Close() error {
	// WAL checkpoint is flushed automatically on close.
	return d.sql.Close()
}

// runMigrations applies the schema DDL. All CREATE statements use IF NOT EXISTS
// so this is safe to call on every startup.
func runMigrations(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

// SQL is the underlying *sql.DB, exposed for stores that need direct access.
func (d *DB) SQL() *sql.DB { return d.sql }
