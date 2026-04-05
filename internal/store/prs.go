package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/andusystems/sentinel/internal/types"
)

// PRStore implements types.SentinelPRStore against SQLite.
type PRStore struct {
	db *sql.DB
}

func (s *PRStore) Create(ctx context.Context, pr types.SentinelPR) error {
	const q = `
		INSERT INTO sentinel_prs
			(id, repo, pr_number, pr_url, branch, base_branch, title, pr_type,
			 priority_tier, related_pr_number, discord_message_id, discord_channel_id,
			 discord_thread_id, status, opened_at, task_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

	_, err := s.db.ExecContext(ctx, q,
		pr.ID, pr.Repo, pr.PRNumber, pr.PRUrl, pr.Branch, pr.BaseBranch,
		pr.Title, pr.PRType, string(pr.PriorityTier), pr.RelatedPRNumber,
		pr.DiscordMessageID, pr.DiscordChannelID, nullStr(pr.DiscordThreadID),
		string(pr.Status), pr.OpenedAt.UTC(), nullStr(pr.TaskID),
	)
	if err != nil {
		return fmt.Errorf("create sentinel_pr %s: %w", pr.ID, err)
	}
	return nil
}

func (s *PRStore) GetByMessageID(ctx context.Context, messageID string) (*types.SentinelPR, error) {
	const q = `SELECT * FROM sentinel_prs WHERE discord_message_id = ? LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, messageID)
	return scanPR(row)
}

func (s *PRStore) GetByPRNumber(ctx context.Context, repo string, prNumber int) (*types.SentinelPR, error) {
	const q = `SELECT * FROM sentinel_prs WHERE repo = ? AND pr_number = ? LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, repo, prNumber)
	return scanPR(row)
}

func (s *PRStore) GetOpenPRs(ctx context.Context) ([]types.SentinelPR, error) {
	const q = `SELECT * FROM sentinel_prs WHERE status = 'open' ORDER BY opened_at DESC`
	return queryPRs(ctx, s.db, q)
}

func (s *PRStore) GetOpenPRsForRepo(ctx context.Context, repo string) ([]types.SentinelPR, error) {
	const q = `SELECT * FROM sentinel_prs WHERE repo = ? AND status = 'open' ORDER BY opened_at DESC`
	return queryPRs(ctx, s.db, q, repo)
}

func (s *PRStore) MarkMerged(ctx context.Context, id, resolvedBy string) error {
	return s.resolve(ctx, id, "merged", resolvedBy)
}

func (s *PRStore) MarkClosed(ctx context.Context, id, resolvedBy string) error {
	return s.resolve(ctx, id, "closed", resolvedBy)
}

func (s *PRStore) SetThread(ctx context.Context, id, threadID string) error {
	const q = `UPDATE sentinel_prs SET discord_thread_id = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, q, threadID, id)
	return err
}

func (s *PRStore) resolve(ctx context.Context, id, status, resolvedBy string) error {
	const q = `UPDATE sentinel_prs SET status = ?, resolved_at = ?, resolved_by = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, q, status, time.Now().UTC(), resolvedBy, id)
	if err != nil {
		return fmt.Errorf("resolve sentinel_pr %s → %s: %w", id, status, err)
	}
	return nil
}

// ---- helpers ----------------------------------------------------------------

func scanPR(row *sql.Row) (*types.SentinelPR, error) {
	var pr types.SentinelPR
	var priorityTier, status string
	var resolvedAt sql.NullTime
	var resolvedBy, threadID, taskID sql.NullString
	var relatedPRNumber sql.NullInt64

	err := row.Scan(
		&pr.ID, &pr.Repo, &pr.PRNumber, &pr.PRUrl, &pr.Branch, &pr.BaseBranch,
		&pr.Title, &pr.PRType, &priorityTier, &relatedPRNumber,
		&pr.DiscordMessageID, &pr.DiscordChannelID, &threadID,
		&status, &pr.OpenedAt, &resolvedAt, &resolvedBy, &taskID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan sentinel_pr: %w", err)
	}

	pr.PriorityTier = types.PRPriorityTier(priorityTier)
	pr.Status = types.PRStatus(status)
	if resolvedAt.Valid {
		t := resolvedAt.Time
		pr.ResolvedAt = &t
	}
	pr.ResolvedBy = resolvedBy.String
	pr.DiscordThreadID = threadID.String
	pr.TaskID = taskID.String
	if relatedPRNumber.Valid {
		pr.RelatedPRNumber = int(relatedPRNumber.Int64)
	}

	return &pr, nil
}

func queryPRs(ctx context.Context, db *sql.DB, q string, args ...any) ([]types.SentinelPR, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var prs []types.SentinelPR
	for rows.Next() {
		var pr types.SentinelPR
		var priorityTier, status string
		var resolvedAt sql.NullTime
		var resolvedBy, threadID, taskID sql.NullString
		var relatedPRNumber sql.NullInt64

		if err := rows.Scan(
			&pr.ID, &pr.Repo, &pr.PRNumber, &pr.PRUrl, &pr.Branch, &pr.BaseBranch,
			&pr.Title, &pr.PRType, &priorityTier, &relatedPRNumber,
			&pr.DiscordMessageID, &pr.DiscordChannelID, &threadID,
			&status, &pr.OpenedAt, &resolvedAt, &resolvedBy, &taskID,
		); err != nil {
			return nil, fmt.Errorf("scan sentinel_pr row: %w", err)
		}

		pr.PriorityTier = types.PRPriorityTier(priorityTier)
		pr.Status = types.PRStatus(status)
		if resolvedAt.Valid {
			t := resolvedAt.Time
			pr.ResolvedAt = &t
		}
		pr.ResolvedBy = resolvedBy.String
		pr.DiscordThreadID = threadID.String
		pr.TaskID = taskID.String
		if relatedPRNumber.Valid {
			pr.RelatedPRNumber = int(relatedPRNumber.Int64)
		}

		prs = append(prs, pr)
	}
	return prs, rows.Err()
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
