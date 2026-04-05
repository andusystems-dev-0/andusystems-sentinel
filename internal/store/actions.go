package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// ActionStore records all sentinel-originated actions for audit.
// Implements types.ActionLogger.
type ActionStore struct {
	db *sql.DB
}

// Log inserts a sentinel_actions row. detail should be JSON (no secrets).
func (s *ActionStore) Log(ctx context.Context, actionType, repo, entityID, detail string) error {
	const q = `
		INSERT INTO sentinel_actions (id, action_type, repo, entity_id, detail, actor, created_at)
		VALUES (?,?,?,?,?,?,?)`

	_, err := s.db.ExecContext(ctx, q,
		newID(), actionType, repo, nullStr(entityID), nullStr(detail), "sentinel", time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("log action %s: %w", actionType, err)
	}
	return nil
}

// LatestMentionAt returns the time of the most recent 'discord_mention_here'
// action for the given repo, or zero Time if none.
func (s *ActionStore) LatestMentionAt(ctx context.Context, repo string) (time.Time, error) {
	const q = `
		SELECT created_at FROM sentinel_actions
		WHERE action_type='discord_mention_here' AND repo=?
		ORDER BY created_at DESC LIMIT 1`

	var t time.Time
	err := s.db.QueryRowContext(ctx, q, repo).Scan(&t)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	return t, err
}
