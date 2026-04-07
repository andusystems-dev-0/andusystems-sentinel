package executor

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/types"
)

// TaskExecutor implements types.TaskExecutor by invoking the [AI_ASSISTANT] Code CLI.
type TaskExecutor struct {
	cfg          *config.Config
	worktreePath string // base path for Forgejo worktrees
}

// NewTaskExecutor creates a TaskExecutor.
func NewTaskExecutor(cfg *config.Config, worktreePath string) *TaskExecutor {
	return &TaskExecutor{cfg: cfg, worktreePath: worktreePath}
}

// Execute invokes [AI_ASSISTANT] Code CLI with the task specification piped via stdin.
//
// Concurrency: acquires global [AI_ASSISTANT] Code semaphore (max 1 concurrent).
// The caller (pipeline/Mode 2) MUST hold ForgejoWorktreeLock.Lock(repo) before calling.
func (e *TaskExecutor) Execute(ctx context.Context, spec types.TaskSpec, branch, repo string) (*types.TaskResult, error) {
	acquireClaudeCode()
	defer releaseClaudeCode()

	// Resolve base branch — default to "main".
	baseBranch := "main"

	prTitle := buildPRTitle(spec)

	taskText, err := RenderTaskSpec(spec, repo, branch, baseBranch, prTitle, e.cfg)
	if err != nil {
		return &types.TaskResult{Error: err.Error()}, err
	}

	// Construct [AI_ASSISTANT] CLI invocation.
	binary := e.cfg.ClaudeCode.BinaryPath
	args := append(e.cfg.ClaudeCode.Flags, "--print") // "--print" reads from stdin

	// Use existing context deadline if set (e.g. from pipeline budget system),
	// otherwise fall back to the static config timeout.
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		timeout := time.Duration(e.cfg.ClaudeCode.TaskTimeoutMinutes) * time.Minute
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(taskText)
	cmd.Dir = filepath.Join(e.worktreePath, repo)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Info("executing [AI_ASSISTANT] Code task",
		"spec_id", spec.ID,
		"repo", repo,
		"branch", branch,
		"binary", binary,
	)

	if err := cmd.Run(); err != nil {
		errMsg := fmt.Sprintf("[AI_ASSISTANT] code exit: %v\nstderr: %s", err, stderr.String())
		slog.Error("[AI_ASSISTANT] Code execution failed", "spec", spec.ID, "err", errMsg)
		return &types.TaskResult{
			Success: false,
			Output:  stdout.String(),
			Error:   errMsg,
		}, fmt.Errorf("[AI_ASSISTANT] code: %w", err)
	}

	slog.Info("[AI_ASSISTANT] Code task completed", "spec_id", spec.ID, "output_len", stdout.Len())

	return &types.TaskResult{
		Success: true,
		Output:  stdout.String(),
	}, nil
}

// ExecuteDocGen invokes [AI_ASSISTANT] Code to generate or update documentation files.
// Concurrency: acquires the global [AI_ASSISTANT] Code semaphore (max 1 concurrent).
// Caller MUST hold ForgejoWorktreeLock.Lock(repo).
func (e *TaskExecutor) ExecuteDocGen(ctx context.Context, id, repo, branch string, docTargets []string, sourceContext, obsidianContext string) (*types.TaskResult, error) {
	acquireClaudeCode()
	defer releaseClaudeCode()

	taskText, err := RenderDocGenSpec(id, repo, branch, "main", docTargets, sourceContext, obsidianContext, e.cfg)
	if err != nil {
		return &types.TaskResult{Error: err.Error()}, err
	}

	binary := e.cfg.ClaudeCode.BinaryPath
	args := append(e.cfg.ClaudeCode.Flags, "--print")

	timeout := time.Duration(e.cfg.ClaudeCode.TaskTimeoutMinutes) * time.Minute
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Stdin = strings.NewReader(taskText)
	cmd.Dir = filepath.Join(e.worktreePath, repo)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Info("executing doc-gen task", "id", id, "repo", repo, "branch", branch, "targets", len(docTargets))

	if err := cmd.Run(); err != nil {
		errMsg := fmt.Sprintf("[AI_ASSISTANT] code exit: %v\nstderr: %s", err, stderr.String())
		slog.Error("doc-gen execution failed", "id", id, "err", errMsg)
		return &types.TaskResult{Success: false, Output: stdout.String(), Error: errMsg},
			fmt.Errorf("doc-gen [AI_ASSISTANT] code: %w", err)
	}

	slog.Info("doc-gen task completed", "id", id, "output_len", stdout.Len())
	return &types.TaskResult{Success: true, Output: stdout.String()}, nil
}

// buildPRTitle formats a PR title from a TaskSpec per the PLAN.md template.
func buildPRTitle(spec types.TaskSpec) string {
	switch spec.Type {
	case "vulnerability":
		return fmt.Sprintf("fix(security): %s", spec.Title)
	case "fix":
		return fmt.Sprintf("fix: %s", spec.Title)
	case "feat", "feature":
		return fmt.Sprintf("feat: %s", spec.Title)
	case "docs", "readme", "doc-gen":
		return fmt.Sprintf("docs: %s", spec.Title)
	case "chore", "sdlc-housekeeping":
		return fmt.Sprintf("chore: %s", spec.Title)
	case "dependency-update":
		return fmt.Sprintf("chore(deps): %s", spec.Title)
	default:
		return spec.Title
	}
}
