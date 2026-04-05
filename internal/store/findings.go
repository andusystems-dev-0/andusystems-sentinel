package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/andusystems/sentinel/internal/types"
)

// FindingStore persists sanitization findings to SQLite.
type FindingStore struct {
	db *sql.DB
}

func (s *FindingStore) Create(ctx context.Context, f types.SanitizationFinding) error {
	const q = `
		INSERT INTO sanitization_findings
			(id, sync_run_id, layer, repo, filename, line_number,
			 byte_offset_start, byte_offset_end, original_value, suggested_replacement,
			 category, confidence, auto_redacted, token_index, pending_resolution_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

	var tokenIndex sql.NullInt64
	if !f.AutoRedacted && f.TokenIndex > 0 {
		tokenIndex = sql.NullInt64{Int64: int64(f.TokenIndex), Valid: true}
	}

	_, err := s.db.ExecContext(ctx, q,
		f.ID, f.SyncRunID, f.Layer, f.Repo, f.Filename, f.LineNumber,
		f.ByteOffsetStart, f.ByteOffsetEnd, f.OriginalValue, f.SuggestedReplacement,
		f.Category, f.Confidence, f.AutoRedacted, tokenIndex, nullStr(f.PendingResolutionID),
	)
	return err
}

func (s *FindingStore) GetByID(ctx context.Context, id string) (*types.SanitizationFinding, error) {
	const q = `SELECT * FROM sanitization_findings WHERE id = ? LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, id)
	return scanFinding(row)
}

func (s *FindingStore) ListByRepoFile(ctx context.Context, repo, filename string) ([]types.SanitizationFinding, error) {
	const q = `SELECT * FROM sanitization_findings WHERE repo = ? AND filename = ? ORDER BY byte_offset_start`
	rows, err := s.db.QueryContext(ctx, q, repo, filename)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFindings(rows)
}

func (s *FindingStore) SetPendingResolutionID(ctx context.Context, findingID, resolutionID string) error {
	const q = `UPDATE sanitization_findings SET pending_resolution_id = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, q, resolutionID, findingID)
	return err
}

// ---- ResolutionStore --------------------------------------------------------

// ResolutionStore implements types.PendingResolutionStore against SQLite.
type ResolutionStore struct {
	db *sql.DB
}

func (s *ResolutionStore) Create(ctx context.Context, r types.PendingResolution) error {
	const q = `
		INSERT INTO pending_resolutions
			(id, repo, filename, finding_id, sync_run_id, forgejo_issue_number,
			 discord_message_id, discord_channel_id, discord_thread_id, has_thread,
			 suggested_replacement, status, superseded_by)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`

	_, err := s.db.ExecContext(ctx, q,
		r.ID, r.Repo, r.Filename, r.FindingID, r.SyncRunID,
		nullInt(r.ForgejoIssueNumber),
		r.DiscordMessageID, r.DiscordChannelID, nullStr(r.DiscordThreadID),
		r.HasThread, r.SuggestedReplacement, string(r.Status), nullStr(r.SupersededBy),
	)
	return err
}

func (s *ResolutionStore) GetByMessageID(ctx context.Context, messageID string) (*types.PendingResolution, error) {
	const q = `SELECT * FROM pending_resolutions WHERE discord_message_id = ? LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, messageID)
	return scanResolution(row)
}

func (s *ResolutionStore) GetPendingForFile(ctx context.Context, repo, filename string) ([]types.PendingResolution, error) {
	const q = `SELECT * FROM pending_resolutions WHERE repo = ? AND filename = ? AND status = 'pending'`
	rows, err := s.db.QueryContext(ctx, q, repo, filename)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []types.PendingResolution
	for rows.Next() {
		r, err := scanResolutionRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// CountResolvedPredecessors counts how many resolutions for (repo, filename)
// have tokenIndex < the given tokenIndex and status in (approved, rejected, custom_replaced).
func (s *ResolutionStore) CountResolvedPredecessors(ctx context.Context, repo, filename string, tokenIndex int) (int, error) {
	const q = `
		SELECT COUNT(*) FROM pending_resolutions pr
		JOIN sanitization_findings sf ON sf.id = pr.finding_id
		WHERE pr.repo = ? AND pr.filename = ?
		  AND sf.token_index < ?
		  AND pr.status IN ('approved','rejected','custom_replaced')`

	var count int
	err := s.db.QueryRowContext(ctx, q, repo, filename, tokenIndex).Scan(&count)
	return count, err
}

func (s *ResolutionStore) Approve(ctx context.Context, id, userID, finalValue string) error {
	const q = `UPDATE pending_resolutions SET status='approved', resolved_at=?, resolved_by=?, final_value=? WHERE id=?`
	_, err := s.db.ExecContext(ctx, q, time.Now().UTC(), userID, finalValue, id)
	return err
}

func (s *ResolutionStore) Reject(ctx context.Context, id, userID string) error {
	const q = `UPDATE pending_resolutions SET status='rejected', resolved_at=?, resolved_by=? WHERE id=?`
	_, err := s.db.ExecContext(ctx, q, time.Now().UTC(), userID, id)
	return err
}

func (s *ResolutionStore) CustomReplace(ctx context.Context, id, userID, token string) error {
	const q = `UPDATE pending_resolutions SET status='custom_replaced', resolved_at=?, resolved_by=?, final_value=? WHERE id=?`
	_, err := s.db.ExecContext(ctx, q, time.Now().UTC(), userID, token, id)
	return err
}

func (s *ResolutionStore) MarkReanalyzing(ctx context.Context, id string) error {
	const q = `UPDATE pending_resolutions SET status='reanalyzing' WHERE id=?`
	_, err := s.db.ExecContext(ctx, q, id)
	return err
}

func (s *ResolutionStore) Supersede(ctx context.Context, oldID, newID string) error {
	const q = `UPDATE pending_resolutions SET status='superseded', superseded_by=? WHERE id=?`
	_, err := s.db.ExecContext(ctx, q, newID, oldID)
	return err
}

func (s *ResolutionStore) SetThread(ctx context.Context, id, threadID string) error {
	const q = `UPDATE pending_resolutions SET discord_thread_id=?, has_thread=TRUE WHERE id=?`
	_, err := s.db.ExecContext(ctx, q, threadID, id)
	return err
}

func (s *ResolutionStore) SetIssueNumber(ctx context.Context, id string, issueNum int) error {
	const q = `UPDATE pending_resolutions SET forgejo_issue_number=? WHERE id=?`
	_, err := s.db.ExecContext(ctx, q, issueNum, id)
	return err
}

// ---- helpers ----------------------------------------------------------------

func scanFinding(row *sql.Row) (*types.SanitizationFinding, error) {
	var f types.SanitizationFinding
	var tokenIndex sql.NullInt64
	var pendingResID sql.NullString

	err := row.Scan(
		&f.ID, &f.SyncRunID, &f.Layer, &f.Repo, &f.Filename, &f.LineNumber,
		&f.ByteOffsetStart, &f.ByteOffsetEnd, &f.OriginalValue, &f.SuggestedReplacement,
		&f.Category, &f.Confidence, &f.AutoRedacted, &tokenIndex, &pendingResID,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan finding: %w", err)
	}

	if tokenIndex.Valid {
		f.TokenIndex = int(tokenIndex.Int64)
	}
	f.PendingResolutionID = pendingResID.String
	return &f, nil
}

func scanFindings(rows *sql.Rows) ([]types.SanitizationFinding, error) {
	var out []types.SanitizationFinding
	for rows.Next() {
		var f types.SanitizationFinding
		var tokenIndex sql.NullInt64
		var pendingResID sql.NullString

		if err := rows.Scan(
			&f.ID, &f.SyncRunID, &f.Layer, &f.Repo, &f.Filename, &f.LineNumber,
			&f.ByteOffsetStart, &f.ByteOffsetEnd, &f.OriginalValue, &f.SuggestedReplacement,
			&f.Category, &f.Confidence, &f.AutoRedacted, &tokenIndex, &pendingResID,
		); err != nil {
			return nil, err
		}
		if tokenIndex.Valid {
			f.TokenIndex = int(tokenIndex.Int64)
		}
		f.PendingResolutionID = pendingResID.String
		out = append(out, f)
	}
	return out, rows.Err()
}

func scanResolution(row *sql.Row) (*types.PendingResolution, error) {
	var r types.PendingResolution
	var status, supersededBy, threadID, resolvedBy, finalValue sql.NullString
	var issueNum sql.NullInt64
	var resolvedAt sql.NullTime

	err := row.Scan(
		&r.ID, &r.Repo, &r.Filename, &r.FindingID, &r.SyncRunID,
		&issueNum, &r.DiscordMessageID, &r.DiscordChannelID, &threadID,
		&r.HasThread, &r.SuggestedReplacement, &status, &supersededBy,
		&resolvedAt, &resolvedBy, &finalValue,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan resolution: %w", err)
	}

	r.Status = types.ResolutionStatus(status.String)
	r.SupersededBy = supersededBy.String
	r.DiscordThreadID = threadID.String
	r.ResolvedBy = resolvedBy.String
	r.FinalValue = finalValue.String
	if issueNum.Valid {
		r.ForgejoIssueNumber = int(issueNum.Int64)
	}
	if resolvedAt.Valid {
		t := resolvedAt.Time
		r.ResolvedAt = &t
	}
	return &r, nil
}

func scanResolutionRow(rows *sql.Rows) (*types.PendingResolution, error) {
	var r types.PendingResolution
	var status, supersededBy, threadID, resolvedBy, finalValue sql.NullString
	var issueNum sql.NullInt64
	var resolvedAt sql.NullTime

	err := rows.Scan(
		&r.ID, &r.Repo, &r.Filename, &r.FindingID, &r.SyncRunID,
		&issueNum, &r.DiscordMessageID, &r.DiscordChannelID, &threadID,
		&r.HasThread, &r.SuggestedReplacement, &status, &supersededBy,
		&resolvedAt, &resolvedBy, &finalValue,
	)
	if err != nil {
		return nil, fmt.Errorf("scan resolution row: %w", err)
	}

	r.Status = types.ResolutionStatus(status.String)
	r.SupersededBy = supersededBy.String
	r.DiscordThreadID = threadID.String
	r.ResolvedBy = resolvedBy.String
	r.FinalValue = finalValue.String
	if issueNum.Valid {
		r.ForgejoIssueNumber = int(issueNum.Int64)
	}
	if resolvedAt.Valid {
		t := resolvedAt.Time
		r.ResolvedAt = &t
	}
	return &r, nil
}

func nullInt(i int) sql.NullInt64 {
	if i == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(i), Valid: true}
}
