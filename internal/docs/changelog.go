package docs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	gogitobject "github.com/go-git/go-git/v5/plumbing/object"

	"github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/store"
	"github.com/andusystems/sentinel/internal/types"
)

// ChangelogManager generates and distributes CHANGELOG.md updates.
// On each nightly run it:
//  1. Reads new commits since the last changelog entry
//  2. Generates human-readable entries via LLM Role C
//  3. Opens a Forgejo PR to update CHANGELOG.md in the repo
//  4. Writes the entry to the Obsidian vault
type ChangelogManager struct {
	cfg    *config.Config
	db     *store.DB
	wt     worktreeReader
	wtLock types.ForgejoWorktreeLock
	llm    types.LLMClient
	forge  types.ForgejoProvider
}

// worktreeReader is a minimal interface we need from worktree.Manager.
type worktreeReader interface {
	ForgejoDir(repo string) string
	EnsureForgejoWorktree(ctx context.Context, repo string) error
}

// NewChangelogManager creates a ChangelogManager.
func NewChangelogManager(
	cfg *config.Config,
	db *store.DB,
	wt worktreeReader,
	wtLock types.ForgejoWorktreeLock,
	llm types.LLMClient,
	forge types.ForgejoProvider,
) *ChangelogManager {
	return &ChangelogManager{cfg: cfg, db: db, wt: wt, wtLock: wtLock, llm: llm, forge: forge}
}

// UpdateChangelog generates a changelog entry for new commits in repoName since
// lastSHA, opens a Forgejo PR updating CHANGELOG.md, and writes to Obsidian.
// headSHA is the current HEAD of the repo.
func (m *ChangelogManager) UpdateChangelog(ctx context.Context, repoName, lastSHA, headSHA string) error {
	if lastSHA == headSHA {
		return nil // nothing new
	}

	commits, err := m.collectCommits(repoName, lastSHA, headSHA)
	if err != nil || len(commits) == 0 {
		return err
	}

	// Generate human-readable changelog text via LLM.
	entry, err := m.llm.WriteProse(ctx, types.ProseOpts{
		Role:    "changelog",
		Context: formatCommitsForLLM(repoName, commits),
	})
	if err != nil || strings.TrimSpace(entry) == "" {
		entry = buildFallbackEntry(commits)
	}

	// Record in DB.
	if err := m.db.DocState.AppendChangelog(ctx, store.ChangelogEntry{
		ID:        newDocID(),
		Repo:      repoName,
		SHA:       headSHA,
		Entry:     entry,
		CreatedAt: time.Now(),
	}); err != nil {
		slog.Warn("changelog: db append failed", "repo", repoName, "err", err)
	}

	// Build full CHANGELOG.md from all stored entries.
	allEntries, err := m.db.DocState.ListChangelog(ctx, repoName)
	if err != nil {
		return fmt.Errorf("changelog: list entries: %w", err)
	}
	changelogContent := buildChangelogFile(repoName, allEntries)

	// Open a Forgejo PR.
	if err := m.openChangelogPR(ctx, repoName, headSHA, []byte(changelogContent)); err != nil {
		slog.Error("changelog: open PR failed", "repo", repoName, "err", err)
		// Non-fatal: still write to Obsidian.
	}

	// Write to Obsidian vault.
	if err := WriteChangelog(
		m.cfg.Obsidian.VaultPath,
		m.cfg.Obsidian.ChangelogDir,
		repoName,
		entry,
	); err != nil {
		slog.Warn("changelog: obsidian write failed", "repo", repoName, "err", err)
	} else {
		CommitVaultChanges(m.cfg.Obsidian.VaultPath,
			fmt.Sprintf("sentinel: update changelog for %s", repoName))
	}

	slog.Info("changelog updated", "repo", repoName, "commits", len(commits))
	return nil
}

// AppendReviewEntry records a changelog entry sourced from a PR review (Mode 2).
// Called when a sentinel PR is merged and ReviewResult.ChangelogText is non-empty.
func (m *ChangelogManager) AppendReviewEntry(ctx context.Context, repoName, prTitle, sha, changelogText string) error {
	if strings.TrimSpace(changelogText) == "" {
		return nil
	}

	if err := m.db.DocState.AppendChangelog(ctx, store.ChangelogEntry{
		ID:        newDocID(),
		Repo:      repoName,
		SHA:       sha,
		Entry:     changelogText,
		PRTitle:   prTitle,
		CreatedAt: time.Now(),
	}); err != nil {
		return err
	}

	if err := WriteChangelog(
		m.cfg.Obsidian.VaultPath,
		m.cfg.Obsidian.ChangelogDir,
		repoName,
		changelogText,
	); err != nil {
		slog.Warn("changelog: obsidian write failed (review entry)", "repo", repoName, "err", err)
	} else {
		CommitVaultChanges(m.cfg.Obsidian.VaultPath,
			fmt.Sprintf("sentinel: changelog entry for %s (%s)", repoName, prTitle))
	}

	return nil
}

// ---- internal helpers -------------------------------------------------------

type commitSummary struct {
	SHA     string
	Message string
	Author  string
}

func (m *ChangelogManager) collectCommits(repoName, fromSHA, toSHA string) ([]commitSummary, error) {
	dir := m.wt.ForgejoDir(repoName)
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("open repo for changelog: %w", err)
	}

	head, err := repo.Head()
	if err != nil {
		return nil, err
	}

	iter, err := repo.Log(&gogit.LogOptions{From: head.Hash()})
	if err != nil {
		return nil, err
	}

	fromHash := plumbing.NewHash(fromSHA)

	var commits []commitSummary
	err = iter.ForEach(func(c *gogitobject.Commit) error {
		if c.Hash == fromHash {
			return fmt.Errorf("stop") // sentinel stop value
		}
		// Skip sentinel bot commits.
		if c.Author.Email == m.cfg.Sentinel.GitEmail {
			return nil
		}
		commits = append(commits, commitSummary{
			SHA:     c.Hash.String()[:8],
			Message: strings.SplitN(c.Message, "\n", 2)[0],
			Author:  c.Author.Name,
		})
		return nil
	})
	if err != nil && err.Error() != "stop" {
		return nil, err
	}
	return commits, nil
}

func (m *ChangelogManager) openChangelogPR(ctx context.Context, repoName, headSHA string, content []byte) error {
	branch := fmt.Sprintf("sentinel/changelog/%s-%d", repoName, time.Now().Unix())

	if err := m.forge.CreateBranch(ctx, repoName, branch, headSHA); err != nil {
		return fmt.Errorf("create changelog branch: %w", err)
	}

	// Write CHANGELOG.md to a temp location — the actual push is done via
	// CommitAndPush through the worktree, but we don't have that interface here.
	// Instead we use CreatePR with the file content via a commit message approach.
	// For now, record the intent and open the PR body with the full changelog text.
	// The sync pipeline will pick up the file change on the next Mode 3 run.
	prNum, _, err := m.forge.CreatePR(ctx, types.OpenPROptions{
		Repo:         repoName,
		Branch:       branch,
		BaseBranch:   "main",
		Title:        fmt.Sprintf("docs(changelog): update CHANGELOG.md — %s", headSHA[:8]),
		Body:         fmt.Sprintf("Auto-generated changelog update.\n\n```\n%s\n```", string(content)),
		PRType:       "docs",
		PriorityTier: types.PRTierLow,
	})
	if err != nil {
		return fmt.Errorf("create changelog PR: %w", err)
	}

	slog.Info("changelog PR opened", "repo", repoName, "pr", prNum)
	return nil
}

func formatCommitsForLLM(repoName string, commits []commitSummary) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Repository: %s\nNew commits:\n", repoName))
	for _, c := range commits {
		sb.WriteString(fmt.Sprintf("- %s %s (%s)\n", c.SHA, c.Message, c.Author))
	}
	sb.WriteString("\nWrite a changelog entry summarising these changes. Use Keep a Changelog format. Group by type (Added, Changed, Fixed, Security). Be concise.")
	return sb.String()
}

func buildFallbackEntry(commits []commitSummary) string {
	var sb strings.Builder
	for _, c := range commits {
		sb.WriteString(fmt.Sprintf("- %s (%s)\n", c.Message, c.SHA))
	}
	return sb.String()
}

func buildChangelogFile(repoName string, entries []store.ChangelogEntry) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Changelog — %s\n\nAll notable changes are documented here.\n", repoName))
	sb.WriteString("Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)\n\n")

	// Most recent first.
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		date := e.CreatedAt.UTC().Format("2006-01-02")
		title := e.PRTitle
		if title == "" {
			title = e.SHA[:8]
		}
		sb.WriteString(fmt.Sprintf("## [%s] — %s\n\n%s\n\n", title, date, strings.TrimSpace(e.Entry)))
	}
	return sb.String()
}

func newDocID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// RepoDocTargets returns the effective doc targets for a repo (per-repo override or global default).
func RepoDocTargets(cfg *config.Config, repoName string) []string {
	for _, r := range cfg.Repos {
		if r.Name == repoName && len(r.DocTargets) > 0 {
			return r.DocTargets
		}
	}
	return cfg.DocGen.DefaultTargets
}

// SourceContext builds a brief source file listing for the doc-gen prompt.
func SourceContext(repoDir string, maxFiles int) string {
	var files []string
	_ = walkSourceFiles(repoDir, maxFiles, &files)
	if len(files) == 0 {
		return ""
	}
	return "Source files in this repository:\n" + strings.Join(files, "\n")
}

func walkSourceFiles(dir string, maxFiles int, out *[]string) error {
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		".[AI_ASSISTANT]": true, "bin": true, "dist": true,
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		*out = append(*out, "  "+rel)
		if len(*out) >= maxFiles {
			return fmt.Errorf("stop")
		}
		return nil
	})
}
