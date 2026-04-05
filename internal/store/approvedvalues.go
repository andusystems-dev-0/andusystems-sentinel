package store

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"

	"github.com/andusystems/sentinel/internal/types"
)

// ApprovedValueStore implements types.ApprovedValuesStore against SQLite.
type ApprovedValueStore struct {
	db *sql.DB
}

func (s *ApprovedValueStore) Add(ctx context.Context, repo, value, category, approvedBy string) error {
	if len(value) > 256 {
		return fmt.Errorf("approved value exceeds 256-char limit")
	}
	const q = `
		INSERT INTO approved_values (id, repo, value, category, approved_by, approved_at)
		VALUES (?, ?, ?, ?, ?, datetime('now'))
		ON CONFLICT(repo, value) DO UPDATE SET
			category=excluded.category,
			approved_by=excluded.approved_by,
			approved_at=excluded.approved_at`

	_, err := s.db.ExecContext(ctx, q, newID(), repo, value, category, approvedBy)
	return err
}

func (s *ApprovedValueStore) Contains(ctx context.Context, repo, value string) (bool, error) {
	const q = `SELECT COUNT(*) FROM approved_values WHERE repo=? AND value=?`
	var n int
	err := s.db.QueryRowContext(ctx, q, repo, value).Scan(&n)
	return n > 0, err
}

// GetSkipZones scans content for all approved_values for the repo and returns
// their byte ranges as SkipZones. The sanitization pipeline uses these zones
// to skip flagging pre-approved values.
func (s *ApprovedValueStore) GetSkipZones(ctx context.Context, repo string, content []byte) ([]types.SkipZone, error) {
	const q = `SELECT value FROM approved_values WHERE repo=?`
	rows, err := s.db.QueryContext(ctx, q, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var zones []types.SkipZone
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}

		// Find all occurrences of this value in content.
		needle := []byte(value)
		start := 0
		for {
			idx := bytes.Index(content[start:], needle)
			if idx < 0 {
				break
			}
			abs := start + idx
			zones = append(zones, types.SkipZone{
				Start: abs,
				End:   abs + len(needle),
			})
			start = abs + len(needle)
		}
	}
	return zones, rows.Err()
}

func (s *ApprovedValueStore) List(ctx context.Context, repo string) ([]types.ApprovedValue, error) {
	const q = `SELECT id, repo, value, category, approved_by, approved_at FROM approved_values WHERE repo=? ORDER BY approved_at DESC`
	rows, err := s.db.QueryContext(ctx, q, repo)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []types.ApprovedValue
	for rows.Next() {
		var av types.ApprovedValue
		if err := rows.Scan(&av.ID, &av.Repo, &av.Value, &av.Category, &av.ApprovedBy, &av.ApprovedAt); err != nil {
			return nil, err
		}
		out = append(out, av)
	}
	return out, rows.Err()
}
