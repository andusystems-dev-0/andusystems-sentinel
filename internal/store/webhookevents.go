package store

import (
	"context"
	"database/sql"
	"time"
)

// WebhookEventStore records all incoming webhook events for audit.
type WebhookEventStore struct {
	db *sql.DB
}

// Record inserts a new webhook event row immediately on receipt.
func (s *WebhookEventStore) Record(ctx context.Context, id, eventType, repo string, payload []byte, hmacValid bool) error {
	const q = `
		INSERT INTO webhook_events (id, event_type, repo, payload, hmac_valid, received_at)
		VALUES (?,?,?,?,?,?)`
	_, err := s.db.ExecContext(ctx, q, id, eventType, repo, payload, hmacValid, time.Now().UTC())
	return err
}

// MarkProcessed updates the processed_at timestamp and any error message.
func (s *WebhookEventStore) MarkProcessed(ctx context.Context, id, errMsg string) error {
	const q = `UPDATE webhook_events SET processed_at=?, error=? WHERE id=?`
	_, err := s.db.ExecContext(ctx, q, time.Now().UTC(), nullStr(errMsg), id)
	return err
}

// GetByID returns a webhook event by ID.
func (s *WebhookEventStore) GetByID(ctx context.Context, id string) (*WebhookEvent, error) {
	const q = `SELECT id, event_type, repo, payload, hmac_valid, received_at, processed_at, error
		FROM webhook_events WHERE id=? LIMIT 1`

	var e WebhookEvent
	var processedAt sql.NullTime
	var errMsg sql.NullString

	err := s.db.QueryRowContext(ctx, q, id).Scan(
		&e.ID, &e.EventType, &e.Repo, &e.Payload, &e.HMACValid,
		&e.ReceivedAt, &processedAt, &errMsg,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if processedAt.Valid {
		t := processedAt.Time
		e.ProcessedAt = &t
	}
	e.Error = errMsg.String
	return &e, nil
}

// WebhookEvent is a row from the webhook_events table.
type WebhookEvent struct {
	ID          string
	EventType   string
	Repo        string
	Payload     []byte
	HMACValid   bool
	ReceivedAt  time.Time
	ProcessedAt *time.Time
	Error       string
}
