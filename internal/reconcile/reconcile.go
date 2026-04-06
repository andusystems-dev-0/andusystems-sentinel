// Package reconcile keeps the GitHub mirror in lock-step with Forgejo.
//
// While Mode 3 sync is normally triggered by Forgejo push webhooks, a webhook
// can be missed (daemon down, network blip, Forgejo hook delivery failure).
// The reconciler closes that gap:
//
//   - On daemon startup, every configured repo is checked: if its current
//     Forgejo HEAD SHA differs from the last SHA sentinel has recorded in
//     sync_runs, Mode 3 sync is triggered.
//   - At a configurable interval, the same check runs as a safety net.
//
// All drift-triggered syncs go through the existing Mode 3 sanitization
// pipeline, so sensitive values are never pushed to GitHub unchecked.
package reconcile

import (
	"context"
	"log/slog"
	"sync"
	"time"

	gogit "github.com/go-git/go-git/v5"

	"github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/store"
	syncp "github.com/andusystems/sentinel/internal/sync"
	"github.com/andusystems/sentinel/internal/types"
	"github.com/andusystems/sentinel/internal/worktree"
)

// Reconciler checks for Forgejo→GitHub drift and triggers Mode 3 sync.
type Reconciler struct {
	cfg    *config.Config
	db     *store.DB
	wt     *worktree.Manager
	wtLock types.ForgejoWorktreeLock
	runner *syncp.Runner

	// mu serialises concurrent RunOnce calls — drift checks should never
	// overlap, since each one takes a per-repo worktree write lock.
	mu sync.Mutex
}

// NewReconciler constructs a drift reconciler.
func NewReconciler(
	cfg *config.Config,
	db *store.DB,
	wt *worktree.Manager,
	wtLock types.ForgejoWorktreeLock,
	runner *syncp.Runner,
) *Reconciler {
	return &Reconciler{cfg: cfg, db: db, wt: wt, wtLock: wtLock, runner: runner}
}

// Result summarises one reconciliation pass.
type Result struct {
	Checked  int
	Synced   int
	UpToDate int
	Failed   int
}

// RunOnce checks all configured sync-enabled repos for Forgejo→DB drift
// and calls syncRunner.Sync on any that are behind.
func (r *Reconciler) RunOnce(ctx context.Context) Result {
	r.mu.Lock()
	defer r.mu.Unlock()

	var res Result
	for _, repoCfg := range r.cfg.Repos {
		if repoCfg.Excluded || !repoCfg.SyncEnabled {
			continue
		}
		res.Checked++
		drifted, err := r.hasDrift(ctx, repoCfg.Name)
		if err != nil {
			slog.Error("reconcile: drift check failed",
				"repo", repoCfg.Name, "err", err)
			res.Failed++
			continue
		}
		if !drifted {
			res.UpToDate++
			continue
		}
		slog.Info("reconcile: drift detected, triggering Mode 3 sync",
			"repo", repoCfg.Name)
		if err := r.runner.Sync(ctx, repoCfg.Name); err != nil {
			slog.Error("reconcile: sync failed",
				"repo", repoCfg.Name, "err", err)
			res.Failed++
			continue
		}
		res.Synced++
	}
	slog.Info("reconcile: pass complete",
		"checked", res.Checked, "synced", res.Synced,
		"up_to_date", res.UpToDate, "failed", res.Failed)
	return res
}

// hasDrift returns true when the live Forgejo HEAD for repoName differs
// from the sentinel.sync_runs.last_sha recorded for that repo.
func (r *Reconciler) hasDrift(ctx context.Context, repoName string) (bool, error) {
	r.wtLock.Lock(repoName)
	defer r.wtLock.Unlock(repoName)

	if err := r.wt.EnsureForgejoWorktree(ctx, repoName); err != nil {
		return false, err
	}

	headSHA, err := readHeadSHA(r.wt.ForgejoDir(repoName))
	if err != nil {
		return false, err
	}

	lastSHA, err := r.db.SyncRuns.GetRepoSyncSHA(ctx, repoName)
	if err != nil {
		return false, err
	}
	return headSHA != lastSHA, nil
}

// StartPeriodic launches a background goroutine that calls RunOnce on the
// configured interval. Returns a stop function that cancels the loop and
// waits for the current pass to finish.
func (r *Reconciler) StartPeriodic(ctx context.Context, interval time.Duration) func() {
	if interval <= 0 {
		return func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		slog.Info("reconcile: periodic pass scheduled", "interval", interval)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.RunOnce(ctx)
			}
		}
	}()
	return func() { cancel(); <-done }
}

// readHeadSHA returns the HEAD commit SHA of a git repo at dir.
func readHeadSHA(dir string) (string, error) {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return "", err
	}
	head, err := repo.Head()
	if err != nil {
		return "", err
	}
	return head.Hash().String(), nil
}
