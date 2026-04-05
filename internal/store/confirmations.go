package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// PendingConfirmation tracks operator confirmations for allowlist additions
// and --force migration operations.
type PendingConfirmation struct {
	ID               string
	Kind             string // "allowlist" | "force_migration"
	Repo             string
	Value            string
	DiscordMessageID string
	DiscordChannelID string
	RequestedBy      string
	CreatedAt        time.Time
	ExpiresAt        time.Time
	Status           string // "pending" | "confirmed" | "rejected" | "expired"
}

// ConfirmationStore manages pending_confirmations CRUD.
type ConfirmationStore struct {
	db *sql.DB
}

func (s *ConfirmationStore) Create(ctx context.Context, c PendingConfirmation) error {
	const q = `
		INSERT INTO pending_confirmations
			(id, kind, repo, value, discord_message_id, discord_channel_id,
			 requested_by, created_at, expires_at, status)
		VALUES (?,?,?,?,?,?,?,?,?,?)`

	_, err := s.db.ExecContext(ctx, q,
		c.ID, c.Kind, c.Repo, nullStr(c.Value),
		c.DiscordMessageID, c.DiscordChannelID,
		c.RequestedBy, c.CreatedAt.UTC(), c.ExpiresAt.UTC(), c.Status,
	)
	return err
}

func (s *ConfirmationStore) GetByMessageID(ctx context.Context, messageID string) (*PendingConfirmation, error) {
	const q = `SELECT id, kind, repo, value, discord_message_id, discord_channel_id,
		requested_by, created_at, expires_at, status
		FROM pending_confirmations WHERE discord_message_id=? LIMIT 1`

	var c PendingConfirmation
	var value sql.NullString
	err := s.db.QueryRowContext(ctx, q, messageID).Scan(
		&c.ID, &c.Kind, &c.Repo, &value,
		&c.DiscordMessageID, &c.DiscordChannelID,
		&c.RequestedBy, &c.CreatedAt, &c.ExpiresAt, &c.Status,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan confirmation: %w", err)
	}
	c.Value = value.String
	return &c, nil
}

// SetStatus updates the confirmation status.
func (s *ConfirmationStore) SetStatus(ctx context.Context, id, status string) error {
	const q = `UPDATE pending_confirmations SET status=? WHERE id=?`
	_, err := s.db.ExecContext(ctx, q, status, id)
	return err
}

// SetStatusByMessageID updates confirmation status by Discord message ID.
func (s *ConfirmationStore) SetStatusByMessageID(ctx context.Context, messageID, status string) error {
	const q = `UPDATE pending_confirmations SET status=? WHERE discord_message_id=? AND status='pending'`
	_, err := s.db.ExecContext(ctx, q, status, messageID)
	return err
}

// SetDiscordMessageID stores the Discord message ID on a confirmation.
func (s *ConfirmationStore) SetDiscordMessageID(ctx context.Context, id, messageID string) error {
	const q = `UPDATE pending_confirmations SET discord_message_id=? WHERE id=?`
	_, err := s.db.ExecContext(ctx, q, messageID, id)
	return err
}

// IsExpired returns true if the confirmation's expires_at is in the past
// and the status is still pending.
func (c *PendingConfirmation) IsExpired() bool {
	return c.Status == "pending" && time.Now().After(c.ExpiresAt)
}
