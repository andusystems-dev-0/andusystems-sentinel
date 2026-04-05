package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Task mirrors the tasks table row.
type Task struct {
	ID            string
	Repo          string
	PipelineRunID string
	TaskType      string
	Complexity    string
	Title         string
	Description   string
	AffectedFiles []string // stored as JSON
	Acceptance    []string // stored as JSON
	Branch        string
	Executor      string // "claude_code" | "llm" | "go"
	Status        string // "pending" | "running" | "pr_opened" | "complete" | "failed"
	CreatedAt     time.Time
	StartedAt     *time.Time
	CompletedAt   *time.Time
	PRNumber      int
}

// TaskStore manages task CRUD.
type TaskStore struct {
	db *sql.DB
}

func (s *TaskStore) Create(ctx context.Context, t Task) error {
	filesJSON, err := json.Marshal(t.AffectedFiles)
	if err != nil {
		return err
	}
	acceptJSON, err := json.Marshal(t.Acceptance)
	if err != nil {
		return err
	}

	const q = `
		INSERT INTO tasks
			(id, repo, pipeline_run_id, task_type, complexity, title, description,
			 affected_files, acceptance, branch, executor, status, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`

	_, err = s.db.ExecContext(ctx, q,
		t.ID, t.Repo, nullStr(t.PipelineRunID), t.TaskType, t.Complexity,
		t.Title, t.Description, string(filesJSON), string(acceptJSON),
		nullStr(t.Branch), t.Executor, t.Status, t.CreatedAt.UTC(),
	)
	return err
}

func (s *TaskStore) GetByID(ctx context.Context, id string) (*Task, error) {
	const q = `SELECT id, repo, pipeline_run_id, task_type, complexity, title, description,
		affected_files, acceptance, branch, executor, status, created_at, started_at, completed_at, pr_number
		FROM tasks WHERE id=? LIMIT 1`

	return scanTask(s.db.QueryRowContext(ctx, q, id))
}

func (s *TaskStore) GetByBranch(ctx context.Context, branch string) (*Task, error) {
	const q = `SELECT id, repo, pipeline_run_id, task_type, complexity, title, description,
		affected_files, acceptance, branch, executor, status, created_at, started_at, completed_at, pr_number
		FROM tasks WHERE branch=? LIMIT 1`

	return scanTask(s.db.QueryRowContext(ctx, q, branch))
}

func (s *TaskStore) SetStatus(ctx context.Context, id, status string) error {
	now := time.Now().UTC()
	switch status {
	case "running":
		_, err := s.db.ExecContext(ctx, `UPDATE tasks SET status=?, started_at=? WHERE id=?`, status, now, id)
		return err
	case "complete", "failed":
		_, err := s.db.ExecContext(ctx, `UPDATE tasks SET status=?, completed_at=? WHERE id=?`, status, now, id)
		return err
	default:
		_, err := s.db.ExecContext(ctx, `UPDATE tasks SET status=? WHERE id=?`, status, id)
		return err
	}
}

func (s *TaskStore) SetBranch(ctx context.Context, id, branch string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET branch=? WHERE id=?`, branch, id)
	return err
}

func (s *TaskStore) SetPRNumber(ctx context.Context, id string, prNumber int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE tasks SET pr_number=?, status='pr_opened' WHERE id=?`, prNumber, id)
	return err
}

// ---- helpers ----------------------------------------------------------------

func scanTask(row *sql.Row) (*Task, error) {
	var t Task
	var pipelineRunID, branch sql.NullString
	var startedAt, completedAt sql.NullTime
	var prNumber sql.NullInt64
	var filesJSON, acceptJSON string

	err := row.Scan(
		&t.ID, &t.Repo, &pipelineRunID, &t.TaskType, &t.Complexity,
		&t.Title, &t.Description, &filesJSON, &acceptJSON,
		&branch, &t.Executor, &t.Status, &t.CreatedAt,
		&startedAt, &completedAt, &prNumber,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan task: %w", err)
	}

	t.PipelineRunID = pipelineRunID.String
	t.Branch = branch.String
	if startedAt.Valid {
		tt := startedAt.Time
		t.StartedAt = &tt
	}
	if completedAt.Valid {
		tt := completedAt.Time
		t.CompletedAt = &tt
	}
	if prNumber.Valid {
		t.PRNumber = int(prNumber.Int64)
	}

	if err := json.Unmarshal([]byte(filesJSON), &t.AffectedFiles); err != nil {
		t.AffectedFiles = nil
	}
	if err := json.Unmarshal([]byte(acceptJSON), &t.Acceptance); err != nil {
		t.Acceptance = nil
	}

	return &t, nil
}
