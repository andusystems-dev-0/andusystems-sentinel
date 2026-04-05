// Package sync implements Mode 3 incremental Forgejo→GitHub sync.
// Must-NOT open Forgejo PRs.
package sync

import (
	"fmt"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/andusystems/sentinel/internal/types"
)

// ChangedFiles returns per-file diffs between lastSHA and the current HEAD
// in the Forgejo worktree at dir.
// If lastSHA is empty, all files in HEAD are returned as additions.
func ChangedFiles(dir, lastSHA string) ([]types.FileDiff, string, error) {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return nil, "", fmt.Errorf("open git repo %s: %w", dir, err)
	}

	head, err := repo.Head()
	if err != nil {
		return nil, "", fmt.Errorf("get HEAD: %w", err)
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
		if baseCommit, err := repo.CommitObject(baseHash); err == nil {
			baseTree, _ = baseCommit.Tree()
		}
	}

	var filenames []string
	if baseTree == nil {
		// First run: collect all files from HEAD tree.
		err = headTree.Files().ForEach(func(f *object.File) error {
			filenames = append(filenames, f.Name)
			return nil
		})
		if err != nil {
			return nil, currentSHA, err
		}
	} else {
		changes, err := headTree.Diff(baseTree)
		if err != nil {
			return nil, currentSHA, err
		}
		for _, c := range changes {
			name := c.To.Name
			if name == "" {
				name = c.From.Name
			}
			filenames = append(filenames, name)
		}
	}

	// Build FileDiff objects (content rather than patch for sync mode).
	var diffs []types.FileDiff
	for _, name := range filenames {
		f, err := headTree.File(name)
		if err != nil {
			continue
		}
		content, err := f.Contents()
		if err != nil {
			continue
		}
		diffs = append(diffs, types.FileDiff{
			Filename:  name,
			Diff:      content,
			EstTokens: len(content) / 4,
		})
	}

	return diffs, currentSHA, nil
}
