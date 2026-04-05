package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/andusystems/sentinel/internal/types"
)

// SyncRunStore implements types.SyncRunStore against SQLite.
type SyncRunStore struct {
	db *sql.DB
}

func (s *SyncRunStore) Create(ctx context.Context, run types.SyncRun) error {
	const q = `
		INSERT INTO sync_runs
			(id, repo, mode, status, started_at, completed_at,
			 files_synced, files_with_pending, findings_high, findings_medium, findings_low)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`

	_, err := s.db.ExecContext(ctx, q,
		run.ID, run.Repo, run.Mode, run.Status, run.StartedAt.UTC(), sqlTime(run.CompletedAt),
		run.FilesSynced, run.FilesWithPending, run.FindingsHigh, run.FindingsMedium, run.FindingsLow,
	)
	if err != nil {
		return fmt.Errorf("create sync_run %s: %w", run.ID, err)
	}
	return nil
}

func (s *SyncRunStore) Update(ctx context.Context, run types.SyncRun) error {
	const q = `
		UPDATE sync_runs SET
			status=?, completed_at=?, files_synced=?, files_with_pending=?,
			findings_high=?, findings_medium=?, findings_low=?
		WHERE id=?`

	_, err := s.db.ExecContext(ctx, q,
		run.Status, sqlTime(run.CompletedAt),
		run.FilesSynced, run.FilesWithPending,
		run.FindingsHigh, run.FindingsMedium, run.FindingsLow,
		run.ID,
	)
	return err
}

func (s *SyncRunStore) GetByID(ctx context.Context, id string) (*types.SyncRun, error) {
	const q = `SELECT id, repo, mode, status, started_at, completed_at,
		files_synced, files_with_pending, findings_high, findings_medium, findings_low
		FROM sync_runs WHERE id=? LIMIT 1`

	var run types.SyncRun
	var completedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&run.ID, &run.Repo, &run.Mode, &run.Status, &run.StartedAt, &completedAt,
		&run.FilesSynced, &run.FilesWithPending, &run.FindingsHigh, &run.FindingsMedium, &run.FindingsLow,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		t := completedAt.Time
		run.CompletedAt = &t
	}
	return &run, nil
}

// GetRepoSyncSHA returns the last synced Forgejo HEAD SHA for the repo.
// Returns ("", nil) if no sync has been recorded yet.
func (s *SyncRunStore) GetRepoSyncSHA(ctx context.Context, repo string) (string, error) {
	const q = `SELECT forgejo_sha FROM repo_sync_state WHERE repo=? LIMIT 1`
	var sha string
	err := s.db.QueryRowContext(ctx, q, repo).Scan(&sha)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return sha, err
}

// SetRepoSyncSHA upserts the last synced Forgejo HEAD SHA for the repo.
func (s *SyncRunStore) SetRepoSyncSHA(ctx context.Context, repo, sha string) error {
	const q = `
		INSERT INTO repo_sync_state (repo, forgejo_sha, synced_at) VALUES (?,?,?)
		ON CONFLICT(repo) DO UPDATE SET forgejo_sha=excluded.forgejo_sha, synced_at=excluded.synced_at`
	_, err := s.db.ExecContext(ctx, q, repo, sha, time.Now().UTC())
	return err
}

// ---- helpers ----------------------------------------------------------------

func sqlTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t.UTC(), Valid: true}
}
