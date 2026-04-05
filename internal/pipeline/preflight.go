// Package pipeline implements Mode 1 nightly SDLC orchestration.
// Must-NOT call LLM directly (uses LLMClient interface) or push to Forgejo directly.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/storer"

	"github.com/andusystems/sentinel/internal/types"
)

// PreflightCheck determines whether the nightly pipeline should run for a repo.
// Returns (skip=true, reason) if the repo should be skipped.
func PreflightCheck(
	ctx context.Context,
	repo string,
	worktreePath string,
	openPRs []types.SentinelPR,
	floodThreshold int,
	activeDevWithinHours int,
	gitEmail string,
) (skip bool, reason string, err error) {
	// Check flood threshold: max open sentinel PRs per repo.
	if len(openPRs) >= floodThreshold {
		return true, fmt.Sprintf("flood threshold: %d open sentinel PRs", len(openPRs)), nil
	}

	// Check for active development (non-sentinel commits in last N hours).
	// activeDevWithinHours <= 0 disables the check entirely.
	if activeDevWithinHours <= 0 {
		return false, "", nil
	}
	active, err := HasActiveDevCommits(ctx, worktreePath, activeDevWithinHours, gitEmail)
	if err != nil {
		slog.Warn("preflight: could not check active dev commits", "repo", repo, "err", err)
		// Non-fatal: proceed anyway.
	}
	if active {
		return true, fmt.Sprintf("active dev within %d hours", activeDevWithinHours), nil
	}

	return false, "", nil
}

// HasActiveDevCommits returns true if there are non-sentinel commits in the
// Forgejo worktree within the last withinHours hours.
func HasActiveDevCommits(ctx context.Context, worktreePath string, withinHours int, gitEmail string) (bool, error) {
	since := time.Now().Add(-time.Duration(withinHours) * time.Hour)

	r, err := gogit.PlainOpen(worktreePath)
	if err != nil {
		return false, err
	}

	iter, err := r.Log(&gogit.LogOptions{Since: &since})
	if err != nil {
		return false, err
	}

	var found bool
	err = iter.ForEach(func(c *object.Commit) error {
		if c.Author.Email == gitEmail {
			return nil // sentinel commit, skip
		}
		found = true
		return storer.ErrStop // non-sentinel commit found
	})
	if err != nil && err != storer.ErrStop {
		return false, err
	}
	return found, nil
}
