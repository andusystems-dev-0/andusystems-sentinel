package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// DocState records when and from which SHA a documentation file was last generated.
type DocState struct {
	Repo             string
	DocFile          string
	GeneratedFromSHA string
	GeneratedAt      time.Time
	PRBranch         string
	Status           string // "current" | "generating" | "failed"
}

// ChangelogEntry is an append-only record of a changelog update for a repo.
type ChangelogEntry struct {
	ID        string
	Repo      string
	SHA       string
	Entry     string
	PRTitle   string
	CreatedAt time.Time
}

// DocStateStore manages doc_state and changelog_entries CRUD.
type DocStateStore struct {
	db *sql.DB
}

// Upsert inserts or replaces the doc state for (repo, doc_file).
func (s *DocStateStore) Upsert(ctx context.Context, d DocState) error {
	const q = `
		INSERT INTO doc_state (repo, doc_file, generated_from_sha, generated_at, pr_branch, status)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(repo, doc_file) DO UPDATE SET
			generated_from_sha=excluded.generated_from_sha,
			generated_at=excluded.generated_at,
			pr_branch=excluded.pr_branch,
			status=excluded.status`
	_, err := s.db.ExecContext(ctx, q,
		d.Repo, d.DocFile, d.GeneratedFromSHA, d.GeneratedAt.UTC(),
		nullStr(d.PRBranch), d.Status,
	)
	return err
}

// Get returns the doc state for (repo, docFile), or nil if not found.
func (s *DocStateStore) Get(ctx context.Context, repo, docFile string) (*DocState, error) {
	const q = `SELECT repo, doc_file, generated_from_sha, generated_at, pr_branch, status
		FROM doc_state WHERE repo=? AND doc_file=? LIMIT 1`

	var d DocState
	var prBranch sql.NullString
	err := s.db.QueryRowContext(ctx, q, repo, docFile).Scan(
		&d.Repo, &d.DocFile, &d.GeneratedFromSHA, &d.GeneratedAt, &prBranch, &d.Status,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan doc_state: %w", err)
	}
	d.PRBranch = prBranch.String
	return &d, nil
}

// StaleTargets returns the subset of docFiles whose generated_from_sha differs
// from currentSHA, or which have never been generated.
func (s *DocStateStore) StaleTargets(ctx context.Context, repo, currentSHA string, docFiles []string) ([]string, error) {
	var stale []string
	for _, f := range docFiles {
		d, err := s.Get(ctx, repo, f)
		if err != nil {
			return nil, err
		}
		if d == nil || d.GeneratedFromSHA != currentSHA {
			stale = append(stale, f)
		}
	}
	return stale, nil
}

// SetStatus updates the status field for (repo, docFile).
func (s *DocStateStore) SetStatus(ctx context.Context, repo, docFile, status string) error {
	const q = `UPDATE doc_state SET status=? WHERE repo=? AND doc_file=?`
	_, err := s.db.ExecContext(ctx, q, status, repo, docFile)
	return err
}

// AppendChangelog inserts a changelog entry record.
func (s *DocStateStore) AppendChangelog(ctx context.Context, e ChangelogEntry) error {
	const q = `INSERT INTO changelog_entries (id, repo, sha, entry, pr_title, created_at)
		VALUES (?,?,?,?,?,?)`
	_, err := s.db.ExecContext(ctx, q,
		e.ID, e.Repo, e.SHA, e.Entry, nullStr(e.PRTitle), e.CreatedAt.UTC(),
	)
	return err
}

// ListChangelog returns all changelog entries for a repo ordered by creation time.
func (s *DocStateStore) ListChangelog(ctx context.Context, repo string) ([]ChangelogEntry, error) {
	const q = `SELECT id, repo, sha, entry, pr_title, created_at
		FROM changelog_entries WHERE repo=? ORDER BY created_at ASC`

	rows, err := s.db.QueryContext(ctx, q, repo)
	if err != nil {
		return nil, fmt.Errorf("list changelog: %w", err)
	}
	defer rows.Close()

	var out []ChangelogEntry
	for rows.Next() {
		var e ChangelogEntry
		var prTitle sql.NullString
		if err := rows.Scan(&e.ID, &e.Repo, &e.SHA, &e.Entry, &prTitle, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan changelog entry: %w", err)
		}
		e.PRTitle = prTitle.String
		out = append(out, e)
	}
	return out, rows.Err()
}
