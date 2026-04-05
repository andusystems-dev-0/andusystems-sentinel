package worktree

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"

	"github.com/andusystems/sentinel/internal/config"
)

// PushStagingFile commits and pushes a single file from the GitHub staging
// worktree to the GitHub mirror repo. Returns the commit SHA on success.
// Caller MUST hold FileMutexRegistry.Lock(repo, filename).
func (m *Manager) PushStagingFile(ctx context.Context, repo, filename, commitMsg string) (string, error) {
	return m.pushFiles(ctx, repo, commitMsg, []string{filename})
}

// PushAllStaging commits and pushes all staged changes in the GitHub staging
// worktree as a single squashed commit. Returns the commit SHA on success.
func (m *Manager) PushAllStaging(ctx context.Context, repo, commitMsg string) (string, error) {
	return m.pushFiles(ctx, repo, commitMsg, nil) // nil = add all
}

func (m *Manager) pushFiles(ctx context.Context, repo, commitMsg string, filenames []string) (string, error) {
	dir := m.githubDirFor(repo)

	r, err := m.ensureGitHubRepo(ctx, repo, dir)
	if err != nil {
		return "", err
	}

	wt, err := r.Worktree()
	if err != nil {
		return "", err
	}

	if filenames == nil {
		// Add all changes.
		if err := wt.AddGlob("."); err != nil {
			return "", fmt.Errorf("git add all in %s: %w", repo, err)
		}
	} else {
		for _, f := range filenames {
			rel, err := filepath.Rel(dir, filepath.Join(dir, f))
			if err != nil {
				return "", err
			}
			if _, err := wt.Add(rel); err != nil {
				return "", fmt.Errorf("git add %s in %s: %w", f, repo, err)
			}
		}
	}

	sig := &object.Signature{
		Name:  m.cfg.Sentinel.GitName,
		Email: m.cfg.Sentinel.GitEmail,
		When:  time.Now(),
	}

	hash, err := wt.Commit(commitMsg, &gogit.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		return "", fmt.Errorf("commit staging for %s: %w", repo, err)
	}

	repoConfig := m.repoConfig(repo)
	if repoConfig == nil {
		return "", fmt.Errorf("repo %q not found in config", repo)
	}

	pushURL := fmt.Sprintf("https://github.com/%s.git", repoConfig.GitHubPath)
	err = r.PushContext(ctx, &gogit.PushOptions{
		RemoteName: "origin",
		Auth: &http.BasicAuth{
			Username: m.cfg.Sentinel.GitHubUsername,
			Password: m.cfg.GitHub.Token,
		},
		RemoteURL: pushURL,
	})
	if err != nil && err != gogit.NoErrAlreadyUpToDate {
		return "", fmt.Errorf("push github staging for %s: %w", repo, err)
	}
	return hash.String(), nil
}

// ensureGitHubRepo opens or initialises the GitHub staging git repo at dir.
func (m *Manager) ensureGitHubRepo(ctx context.Context, repo, dir string) (*gogit.Repository, error) {
	r, err := gogit.PlainOpen(dir)
	if err == nil {
		return r, nil
	}

	// Init a new repo.
	r, err = gogit.PlainInit(dir, false)
	if err != nil {
		return nil, fmt.Errorf("git init github staging %s: %w", repo, err)
	}

	repoConfig := m.repoConfig(repo)
	if repoConfig == nil {
		return nil, fmt.Errorf("repo %q not found in config", repo)
	}

	_ = ctx // context available for future remote operations
	return r, nil
}

// ResolutionCommitMsg formats the commit message for a single resolved finding.
// Format: chore(sync): sentinel resolved <category> in <basename>:<line>
func ResolutionCommitMsg(category, filename string, lineNumber int) string {
	return fmt.Sprintf("chore(sync): sentinel resolved %s in %s:%d",
		category, filepath.Base(filename), lineNumber)
}

// config is a convenience accessor for the config used in tests.
func (m *Manager) Config() *config.Config { return m.cfg }
