package store

import (
	"context"
	"database/sql"
	"time"
)

// ReviewStore manages Mode 2 PR review dedup and result records.
type ReviewStore struct {
	db *sql.DB
}

// WasReviewedWithin returns true if a review for (repo, prNumber) was recorded
// within cooldownMinutes ago.
func (s *ReviewStore) WasReviewedWithin(ctx context.Context, repo string, prNumber, cooldownMinutes int) (bool, error) {
	const q = `SELECT COUNT(*) FROM pr_dedup WHERE repo=? AND pr_number=? AND reviewed_at > ?`
	cutoff := time.Now().Add(-time.Duration(cooldownMinutes) * time.Minute).UTC()
	var n int
	err := s.db.QueryRowContext(ctx, q, repo, prNumber, cutoff).Scan(&n)
	return n > 0, err
}

// MarkReviewed upserts a pr_dedup record.
func (s *ReviewStore) MarkReviewed(ctx context.Context, repo string, prNumber int) error {
	const q = `
		INSERT INTO pr_dedup (id, repo, pr_number, reviewed_at) VALUES (?,?,?,?)
		ON CONFLICT(repo, pr_number) DO UPDATE SET reviewed_at=excluded.reviewed_at`
	_, err := s.db.ExecContext(ctx, q, newID(), repo, prNumber, time.Now().UTC())
	return err
}

// SaveReview persists a completed review record.
func (s *ReviewStore) SaveReview(ctx context.Context, r Review) error {
	const q = `
		INSERT INTO reviews (id, repo, pr_number, verdict, comment_posted, housekeeping_pr, fix_pr, reviewed_at)
		VALUES (?,?,?,?,?,?,?,?)`
	_, err := s.db.ExecContext(ctx, q,
		r.ID, r.Repo, r.PRNumber, r.Verdict, r.CommentPosted,
		nullInt(r.HousekeepingPR), nullInt(r.FixPR), time.Now().UTC(),
	)
	return err
}

// Review is a record of a completed Mode 2 review.
type Review struct {
	ID            string
	Repo          string
	PRNumber      int
	Verdict       string // "APPROVE" | "REQUEST_CHANGES" | "COMMENT"
	CommentPosted bool
	HousekeepingPR int
	FixPR         int
}

// GetMigrationStatus returns the current migration state for a repo.
func (s *ReviewStore) GetMigrationStatus(ctx context.Context, repo string) (string, error) {
	const q = `SELECT status FROM migration_status WHERE repo=? LIMIT 1`
	var status string
	err := s.db.QueryRowContext(ctx, q, repo).Scan(&status)
	if err == sql.ErrNoRows {
		return "pending", nil
	}
	return status, err
}

// SetMigrationStatus upserts the migration status for a repo.
func (s *ReviewStore) SetMigrationStatus(ctx context.Context, repo, status, forgejosHA, errMsg string) error {
	const q = `
		INSERT INTO migration_status (repo, status, forgejo_sha, error)
		VALUES (?,?,?,?)
		ON CONFLICT(repo) DO UPDATE SET status=excluded.status, forgejo_sha=excluded.forgejo_sha, error=excluded.error`
	_, err := s.db.ExecContext(ctx, q, repo, status, nullStr(forgejosHA), nullStr(errMsg))
	return err
}

// StartMigration sets started_at for a repo migration.
func (s *ReviewStore) StartMigration(ctx context.Context, repo string) error {
	const q = `
		INSERT INTO migration_status (repo, status, started_at) VALUES (?,?,?)
		ON CONFLICT(repo) DO UPDATE SET status='in_progress', started_at=excluded.started_at`
	_, err := s.db.ExecContext(ctx, q, repo, "in_progress", time.Now().UTC())
	return err
}

// CompleteMigration sets completed_at and status for a repo migration.
func (s *ReviewStore) CompleteMigration(ctx context.Context, repo, forgejosHA string) error {
	const q = `UPDATE migration_status SET status='complete', completed_at=?, forgejo_sha=? WHERE repo=?`
	_, err := s.db.ExecContext(ctx, q, time.Now().UTC(), forgejosHA, repo)
	return err
}
