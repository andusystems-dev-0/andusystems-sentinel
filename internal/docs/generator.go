package docs

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v5"

	"github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/executor"
	"github.com/andusystems/sentinel/internal/store"
	"github.com/andusystems/sentinel/internal/types"
)

// Generator generates and progressively updates documentation for sentinel-managed repos.
// It uses the [AI_ASSISTANT] Code CLI to read the full repository and produce Markdown docs.
type Generator struct {
	cfg      *config.Config
	db       *store.DB
	wt       worktreeReader
	wtLock   types.ForgejoWorktreeLock
	executor *executor.TaskExecutor
	forge    types.ForgejoProvider
	discord  types.DiscordBot
	notifier types.PRNotifier
	prStore  types.SentinelPRStore
}

func (g *Generator) prChannelID() string {
	if g.cfg.Discord.PRChannelID != "" {
		return g.cfg.Discord.PRChannelID
	}
	return g.cfg.Discord.ActionsChannelID
}

// NewGenerator creates a documentation Generator.
func NewGenerator(
	cfg *config.Config,
	db *store.DB,
	wt worktreeReader,
	wtLock types.ForgejoWorktreeLock,
	exec *executor.TaskExecutor,
	forge types.ForgejoProvider,
	discord types.DiscordBot,
	notifier types.PRNotifier,
	prStore types.SentinelPRStore,
) *Generator {
	return &Generator{
		cfg:      cfg,
		db:       db,
		wt:       wt,
		wtLock:   wtLock,
		executor: exec,
		forge:    forge,
		discord:  discord,
		notifier: notifier,
		prStore:  prStore,
	}
}

// RunFull regenerates all documentation targets for repoName from scratch.
// Used by --mode doc-gen. Acquires the worktree write lock.
func (g *Generator) RunFull(ctx context.Context, repoName string) error {
	slog.Info("doc-gen: full run start", "repo", repoName)

	g.wtLock.Lock(repoName)
	if err := g.wt.EnsureForgejoWorktree(ctx, repoName); err != nil {
		g.wtLock.Unlock(repoName)
		return fmt.Errorf("doc-gen: ensure worktree: %w", err)
	}
	// Keep lock for entire generation — [AI_ASSISTANT] Code writes to the worktree.

	targets := RepoDocTargets(g.cfg, repoName)
	headSHA, err := g.headSHA(repoName)
	if err != nil {
		g.wtLock.Unlock(repoName)
		return fmt.Errorf("doc-gen: head SHA: %w", err)
	}

	if err := g.generate(ctx, repoName, headSHA, targets); err != nil {
		g.wtLock.Unlock(repoName)
		return err
	}
	g.wtLock.Unlock(repoName)

	slog.Info("doc-gen: full run complete", "repo", repoName, "targets", len(targets))
	return nil
}

// UpdateStale checks which doc targets are stale relative to headSHA and
// regenerates only those. Called by the nightly pipeline after task processing.
// Caller should NOT hold the worktree lock — this method acquires it internally.
func (g *Generator) UpdateStale(ctx context.Context, repoName, headSHA string) error {
	if !g.cfg.DocGen.Enabled {
		return nil
	}

	targets := RepoDocTargets(g.cfg, repoName)
	stale, err := g.db.DocState.StaleTargets(ctx, repoName, headSHA, targets)
	if err != nil {
		return fmt.Errorf("doc-gen: stale check: %w", err)
	}
	if len(stale) == 0 {
		return nil
	}

	slog.Info("doc-gen: stale targets found", "repo", repoName, "count", len(stale))

	g.wtLock.Lock(repoName)
	defer g.wtLock.Unlock(repoName)

	return g.generate(ctx, repoName, headSHA, stale)
}

// generate invokes [AI_ASSISTANT] Code to produce docTargets for repoName, then opens a Forgejo PR.
func (g *Generator) generate(ctx context.Context, repoName, headSHA string, docTargets []string) error {
	repoDir := g.wt.ForgejoDir(repoName)

	sourceCtx := SourceContext(repoDir, g.cfg.DocGen.MaxContextFiles)
	obsidianCtx := ReadVaultContext(g.cfg.Obsidian.VaultPath, repoName, 10)

	branch := fmt.Sprintf("sentinel/docs/%s/generate-documentation-%d",
		slugifyRepo(repoName), time.Now().Unix())
	id := newDocID()

	// Mark all targets as "generating" in DB.
	for _, t := range docTargets {
		_ = g.db.DocState.SetStatus(ctx, repoName, t, "generating")
	}

	result, err := g.executor.ExecuteDocGen(ctx, id, repoName, branch, docTargets, sourceCtx, obsidianCtx)
	if err != nil || !result.Success {
		for _, t := range docTargets {
			_ = g.db.DocState.SetStatus(ctx, repoName, t, "failed")
		}
		return fmt.Errorf("doc-gen execution failed for %s: %v", repoName, err)
	}

	// Open a PR on Forgejo.
	prBody := buildDocPRBody(docTargets)
	prNum, prURL, prErr := g.forge.CreatePR(ctx, types.OpenPROptions{
		Repo:         repoName,
		Branch:       branch,
		BaseBranch:   "main",
		Title:        "docs: generate/update documentation",
		Body:         prBody,
		PRType:       "docs",
		PriorityTier: types.PRTierLow,
	})
	if prErr != nil {
		slog.Warn("doc-gen: open PR failed", "repo", repoName, "err", prErr)
	} else {
		slog.Info("doc-gen: PR opened", "repo", repoName, "pr", prNum)
		g.notifyPR(ctx, repoName, prNum, prURL, branch, id)
	}

	// Update doc state for each target.
	now := time.Now()
	for _, t := range docTargets {
		_ = g.db.DocState.Upsert(ctx, store.DocState{
			Repo:             repoName,
			DocFile:          t,
			GeneratedFromSHA: headSHA,
			GeneratedAt:      now,
			PRBranch:         branch,
			Status:           "current",
		})

		// Write snapshot to Obsidian vault if it's a readable file.
		if docContent := g.readGeneratedFile(repoDir, branch, t); docContent != nil {
			if err := WriteDocSnapshot(
				g.cfg.Obsidian.VaultPath,
				g.cfg.Obsidian.DocsDir,
				repoName,
				t,
				docContent,
			); err != nil {
				slog.Warn("doc-gen: obsidian snapshot failed", "file", t, "err", err)
			}
		}
	}

	CommitVaultChanges(g.cfg.Obsidian.VaultPath,
		fmt.Sprintf("sentinel: doc snapshot for %s", repoName))

	g.db.Actions.Log(ctx, "doc_gen_complete", repoName, id,
		fmt.Sprintf(`{"targets":%d,"sha":"%s"}`, len(docTargets), headSHA))

	return nil
}

// notifyPR saves the PR to the store and posts a Discord embed with reactions.
func (g *Generator) notifyPR(ctx context.Context, repoName string, prNum int, prURL, branch, taskID string) {
	pr := types.SentinelPR{
		ID:               newDocID(),
		Repo:             repoName,
		PRNumber:         prNum,
		PRUrl:            prURL,
		Branch:           branch,
		BaseBranch:       "main",
		Title:            "docs: generate/update documentation",
		PRType:           "docs",
		PriorityTier:     types.PRTierLow,
		Status:           types.PRStatusOpen,
		OpenedAt:         time.Now(),
		TaskID:           taskID,
		DiscordChannelID: g.prChannelID(),
	}

	summary := fmt.Sprintf("Documentation generated for **%s**.\nReact ✅ to merge or ❌ to close.", repoName)
	msgID, err := g.notifier.PostPRNotification(ctx, pr, summary)
	if err != nil {
		slog.Warn("doc-gen: discord notification failed", "repo", repoName, "err", err)
		// Still save to DB without Discord ID so the PR is tracked.
	} else {
		pr.DiscordMessageID = msgID
		slog.Info("doc-gen: discord notification posted", "repo", repoName, "message_id", msgID)
	}

	if err := g.prStore.Create(ctx, pr); err != nil {
		slog.Warn("doc-gen: store PR failed", "repo", repoName, "err", err)
	}
}

// readGeneratedFile attempts to read a generated doc file from the worktree.
// Returns nil if the file doesn't exist ([AI_ASSISTANT] Code may not have created it).
func (g *Generator) readGeneratedFile(repoDir, _ /* branch */, docFile string) []byte {
	path := filepath.Join(repoDir, docFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

func (g *Generator) headSHA(repoName string) (string, error) {
	dir := g.wt.ForgejoDir(repoName)
	return readHeadSHA(dir)
}

func buildDocPRBody(targets []string) string {
	var sb strings.Builder
	sb.WriteString("Auto-generated documentation update.\n\n**Files updated:**\n")
	for _, t := range targets {
		sb.WriteString(fmt.Sprintf("- `%s`\n", t))
	}
	sb.WriteString("\nGenerated by Sentinel doc-gen. Review before merging.")
	return sb.String()
}

// slugifyRepo converts a repo name to a URL-safe slug (max 20 chars).
func slugifyRepo(s string) string {
	s = strings.ToLower(s)
	var result []rune
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			result = append(result, c)
		}
	}
	if len(result) > 20 {
		result = result[:20]
	}
	return string(result)
}

// readHeadSHA returns the HEAD commit SHA of the local git repo at dir.
func readHeadSHA(dir string) (string, error) {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return "", fmt.Errorf("open repo: %w", err)
	}
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("repo HEAD: %w", err)
	}
	return head.Hash().String(), nil
}
