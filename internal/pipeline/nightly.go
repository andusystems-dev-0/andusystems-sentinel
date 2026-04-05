package pipeline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/docs"
	"github.com/andusystems/sentinel/internal/executor"
	"github.com/andusystems/sentinel/internal/llm"
	"github.com/andusystems/sentinel/internal/prnotify"
	"github.com/andusystems/sentinel/internal/store"
	"github.com/andusystems/sentinel/internal/types"
	"github.com/andusystems/sentinel/internal/worktree"
)

// NightlyRunner orchestrates Mode 1 nightly SDLC for all watched repos.
type NightlyRunner struct {
	cfg       *config.Config
	db        *store.DB
	wt        *worktree.Manager
	wtLock    types.ForgejoWorktreeLock
	batcher   *llm.Batcher
	executor  *executor.TaskExecutor
	forge     types.ForgejoProvider
	notifier  *prnotify.Notifier
	docGen    *docs.Generator
	changelog *docs.ChangelogManager
}

// NewNightlyRunner creates a NightlyRunner.
func NewNightlyRunner(
	cfg *config.Config,
	db *store.DB,
	wt *worktree.Manager,
	wtLock types.ForgejoWorktreeLock,
	batcher *llm.Batcher,
	exec *executor.TaskExecutor,
	forge types.ForgejoProvider,
	notifier *prnotify.Notifier,
	docGen *docs.Generator,
	changelog *docs.ChangelogManager,
) *NightlyRunner {
	return &NightlyRunner{
		cfg:       cfg,
		db:        db,
		wt:        wt,
		wtLock:    wtLock,
		batcher:   batcher,
		executor:  exec,
		forge:     forge,
		notifier:  notifier,
		docGen:    docGen,
		changelog: changelog,
	}
}

// RunAll runs the nightly pipeline for all non-excluded repos sequentially.
func (r *NightlyRunner) RunAll(ctx context.Context) error {
	for _, repo := range r.cfg.Repos {
		if repo.Excluded || !repo.SyncEnabled {
			continue
		}

		if err := r.Run(ctx, repo.Name); err != nil {
			slog.Error("nightly run failed", "repo", repo.Name, "err", err)
			// Continue to next repo — non-blocking.
		}
	}
	return nil
}

// Run executes the nightly pipeline for a single repo.
func (r *NightlyRunner) Run(ctx context.Context, repoName string) error {
	slog.Info("nightly pipeline start", "repo", repoName)

	// 1. Pre-flight checks.
	r.wtLock.Lock(repoName)
	if err := r.wt.EnsureForgejoWorktree(ctx, repoName); err != nil {
		r.wtLock.Unlock(repoName)
		return fmt.Errorf("ensure worktree: %w", err)
	}
	r.wtLock.Unlock(repoName)

	openPRs, err := r.db.PRs.GetOpenPRsForRepo(ctx, repoName)
	if err != nil {
		return err
	}

	repoConfig := r.repoConfig(repoName)
	if repoConfig == nil {
		return fmt.Errorf("repo %q not found in config", repoName)
	}

	skip, reason, err := PreflightCheck(
		ctx,
		repoName,
		r.wt.ForgejoDir(repoName),
		openPRs,
		r.cfg.Nightly.FloodThreshold,
		r.cfg.Nightly.SkipIfActiveDevWithinHours,
		r.cfg.Sentinel.GitEmail,
	)
	if err != nil {
		return err
	}
	if skip {
		slog.Info("nightly pipeline skipped", "repo", repoName, "reason", reason)
		return nil
	}

	// 2. Get diff since last synced SHA.
	lastSHA, err := r.db.SyncRuns.GetRepoSyncSHA(ctx, repoName)
	if err != nil {
		return err
	}

	diffs, currentSHA, err := r.getFileDiffs(ctx, repoName, lastSHA)
	if err != nil {
		return fmt.Errorf("get diffs for %s: %w", repoName, err)
	}

	if len(diffs) == 0 {
		slog.Info("nightly pipeline: no changes since last run", "repo", repoName)
		return nil
	}

	// 3. Multi-call LLM analysis.
	r.wtLock.RLock(repoName)
	specs, err := r.batcher.AnalyzeAll(ctx, repoName, diffs, repoConfig.FocusAreas, repoConfig.MaxTasksPerRun)
	r.wtLock.RUnlock(repoName)
	if err != nil {
		return fmt.Errorf("LLM analysis: %w", err)
	}

	slog.Info("nightly: analysis complete", "repo", repoName, "tasks", len(specs))

	// 4. For each task, create branch + PR (or invoke [AI_ASSISTANT] Code).
	pipelineRunID := newID()
	for _, spec := range specs {
		if err := r.processTask(ctx, repoName, spec, pipelineRunID, repoConfig); err != nil {
			slog.Error("nightly task failed", "repo", repoName, "task", spec.Title, "err", err)
			// Continue to next task.
		}
	}

	// 5. Update sync SHA baseline.
	if currentSHA != "" {
		r.db.SyncRuns.SetRepoSyncSHA(ctx, repoName, currentSHA)
	}

	r.db.Actions.Log(ctx, "mode1_nightly_complete", repoName, pipelineRunID,
		fmt.Sprintf(`{"tasks":%d}`, len(specs)))

	// 6. Update stale documentation (non-blocking: errors are logged, not returned).
	if r.docGen != nil {
		if err := r.docGen.UpdateStale(ctx, repoName, currentSHA); err != nil {
			slog.Warn("nightly: doc-gen update failed", "repo", repoName, "err", err)
		}
	}

	// 7. Update changelog from new commits since last run.
	if r.changelog != nil {
		if err := r.changelog.UpdateChangelog(ctx, repoName, lastSHA, currentSHA); err != nil {
			slog.Warn("nightly: changelog update failed", "repo", repoName, "err", err)
		}
	}

	slog.Info("nightly pipeline complete", "repo", repoName, "tasks_processed", len(specs))
	return nil
}

func (r *NightlyRunner) processTask(ctx context.Context, repoName string, spec types.TaskSpec, pipelineRunID string, repoCfg *config.RepoConfig) error {
	executor := AssignExecutor(spec)
	branch := BranchName(TaskTypeToPRType(spec.Type), repoName, spec.Title)
	prTitle := PRTitleFor(spec)

	// Save task record.
	task := &store.Task{
		ID:            newID(),
		Repo:          repoName,
		PipelineRunID: pipelineRunID,
		TaskType:      spec.Type,
		Complexity:    spec.Complexity,
		Title:         spec.Title,
		Description:   spec.Description,
		AffectedFiles: spec.AffectedFiles,
		Acceptance:    spec.AcceptanceCriteria,
		Branch:        branch,
		Executor:      executor,
		Status:        "pending",
		CreatedAt:     time.Now(),
	}
	if err := r.db.Tasks.Create(ctx, *task); err != nil {
		return err
	}

	if executor != "claude_code" {
		// LLM or Go executor — handled separately.
		slog.Info("nightly: skipping non-[AI_ASSISTANT]-Code task for now", "type", spec.Type, "title", spec.Title)
		return nil
	}

	// Acquire write lock and invoke [AI_ASSISTANT] Code.
	r.wtLock.Lock(repoName)
	defer r.wtLock.Unlock(repoName)

	// Create branch on Forgejo.
	headSHA, err := r.forge.GetHeadSHA(ctx, repoName, "main")
	if err != nil {
		return fmt.Errorf("get head SHA: %w", err)
	}
	if err := r.forge.CreateBranch(ctx, repoName, branch, headSHA); err != nil {
		return fmt.Errorf("create branch %s: %w", branch, err)
	}

	r.db.Tasks.SetStatus(ctx, task.ID, "running")

	spec.ID = task.ID
	result, err := r.executor.Execute(ctx, spec, branch, repoName)
	if err != nil || !result.Success {
		r.db.Tasks.SetStatus(ctx, task.ID, "failed")
		return fmt.Errorf("[AI_ASSISTANT] code task %s: %v", task.ID, err)
	}

	_ = prTitle // PR is opened by the webhook push handler (Branch B flow).
	r.db.Tasks.SetStatus(ctx, task.ID, "complete")
	return nil
}

// getFileDiffs returns per-file diffs between lastSHA and current HEAD.
// If lastSHA is empty, diffs all files against the empty tree.
func (r *NightlyRunner) getFileDiffs(_ context.Context, repoName, lastSHA string) ([]types.FileDiff, string, error) {
	dir := r.wt.ForgejoDir(repoName)

	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return nil, "", err
	}

	head, err := repo.Head()
	if err != nil {
		return nil, "", err
	}
	currentSHA := head.Hash().String()

	if lastSHA == currentSHA {
		return nil, currentSHA, nil
	}

	headCommit, err := repo.CommitObject(head.Hash())
	if err != nil {
		return nil, "", err
	}
	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, "", err
	}

	var baseTree *object.Tree
	if lastSHA != "" {
		baseHash := plumbing.NewHash(lastSHA)
		baseCommit, err := repo.CommitObject(baseHash)
		if err == nil {
			baseTree, _ = baseCommit.Tree()
		}
	}

	var diffs []types.FileDiff

	if baseTree == nil {
		// First run: walk all files in the tree and treat every line as added.
		err = headTree.Files().ForEach(func(f *object.File) error {
			content, ferr := f.Contents()
			if ferr != nil {
				return nil // skip unreadable files
			}
			lines := 0
			for _, c := range content {
				if c == '\n' {
					lines++
				}
			}
			diffs = append(diffs, types.FileDiff{
				Filename:     f.Name,
				Diff:         content,
				LinesAdded:   lines,
				LinesRemoved: 0,
				EstTokens:    len(content) / 4,
			})
			return nil
		})
		if err != nil {
			return nil, currentSHA, fmt.Errorf("walk head tree: %w", err)
		}
		return diffs, currentSHA, nil
	}

	changes, err := headTree.Diff(baseTree)
	if err != nil {
		return nil, currentSHA, fmt.Errorf("diff trees: %w", err)
	}

	for _, change := range changes {
		patch, err := change.Patch()
		if err != nil {
			continue
		}
		diffStr := patch.String()
		name := change.To.Name
		if name == "" {
			name = change.From.Name
		}

		stats := patch.Stats()
		var added, removed int
		for _, s := range stats {
			added += s.Addition
			removed += s.Deletion
		}

		diffs = append(diffs, types.FileDiff{
			Filename:     name,
			Diff:         diffStr,
			LinesAdded:   added,
			LinesRemoved: removed,
			EstTokens:    len(diffStr) / 4,
		})
	}

	return diffs, currentSHA, nil
}

func (r *NightlyRunner) repoConfig(name string) *config.RepoConfig {
	for i := range r.cfg.Repos {
		if r.cfg.Repos[i].Name == name {
			return &r.cfg.Repos[i]
		}
	}
	return nil
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
