package discord

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/andusystems/sentinel/internal/types"
)

// ThreadManager handles Q&A threads for both findings and PRs.
type ThreadManager struct {
	bot         *Bot
	llm         types.LLMClient
	resolutions types.PendingResolutionStore
	prStore     types.SentinelPRStore
}

// NewThreadManager creates a ThreadManager.
func NewThreadManager(bot *Bot, llm types.LLMClient, res types.PendingResolutionStore, prs types.SentinelPRStore) *ThreadManager {
	return &ThreadManager{bot: bot, llm: llm, resolutions: res, prStore: prs}
}

// OpenFindingThread opens a Discord thread on a finding message and registers it.
func (t *ThreadManager) OpenFindingThread(ctx context.Context, r *types.PendingResolution, f types.SanitizationFinding) error {
	name := fmt.Sprintf("Finding: %s in %s (line %d)", f.Category, shortPath(f.Filename), f.LineNumber)
	threadID, err := t.bot.OpenThread(ctx, r.DiscordChannelID, r.DiscordMessageID, name)
	if err != nil {
		return fmt.Errorf("open finding thread: %w", err)
	}

	if err := t.resolutions.SetThread(ctx, r.ID, threadID); err != nil {
		return err
	}

	// Post initial context into thread.
	initial := fmt.Sprintf(
		"**Finding context**\n- Repo: `%s`\n- File: `%s`\n- Line: %d\n- Category: `%s`\n- Confidence: `%s`\n- Suggested replacement: `%s`\n\nAsk a question and I'll answer using Role E (security assistant).",
		f.Repo, f.Filename, f.LineNumber, f.Category, f.Confidence, r.SuggestedReplacement,
	)
	return t.bot.PostInThread(ctx, threadID, initial)
}

// OpenPRThread opens a Discord thread on a PR notification message.
func (t *ThreadManager) OpenPRThread(ctx context.Context, pr *types.SentinelPR) error {
	name := fmt.Sprintf("Discussion: %s", pr.Title)
	threadID, err := t.bot.OpenThread(ctx, pr.DiscordChannelID, pr.DiscordMessageID, name)
	if err != nil {
		return fmt.Errorf("open PR thread: %w", err)
	}

	if err := t.prStore.SetThread(ctx, pr.ID, threadID); err != nil {
		return err
	}

	initial := fmt.Sprintf(
		"**PR Discussion**\n- Repo: `%s`\n- Branch: `%s` → `%s`\n- Forgejo: %s\n\nAsk a question and I'll answer using the PR diff as context.",
		pr.Repo, pr.Branch, pr.BaseBranch, pr.PRUrl,
	)
	return t.bot.PostInThread(ctx, threadID, initial)
}

// AnswerFindingQuestion calls Role E LLM to answer a question in a finding thread.
func (t *ThreadManager) AnswerFindingQuestion(ctx context.Context, threadID, question string, r types.PendingResolution, f types.SanitizationFinding) error {
	context := fmt.Sprintf(
		"Finding: %s in %s:%d\nSuggested replacement: %s",
		f.Category, f.Filename, f.LineNumber, r.SuggestedReplacement,
	)

	answer, err := t.llm.AnswerThread(ctx, types.ThreadOpts{
		Role:     "finding",
		Context:  context,
		Question: question,
	})
	if err != nil {
		slog.Warn("LLM thread answer failed", "thread", threadID, "err", err)
		answer = "Unable to answer at this time. Please review the finding directly."
	}

	return t.bot.PostInThread(ctx, threadID, answer)
}

// AnswerPRQuestion calls Role F LLM to answer a question in a PR thread.
func (t *ThreadManager) AnswerPRQuestion(ctx context.Context, threadID, question string, pr types.SentinelPR, diffSummary string) error {
	context := fmt.Sprintf(
		"PR: %s in %s\nBranch: %s → %s\n\nDiff summary:\n%s",
		pr.Title, pr.Repo, pr.Branch, pr.BaseBranch, diffSummary,
	)

	answer, err := t.llm.AnswerThread(ctx, types.ThreadOpts{
		Role:     "pr",
		Context:  context,
		Question: question,
	})
	if err != nil {
		answer = "Unable to answer at this time."
	}

	return t.bot.PostInThread(ctx, threadID, answer)
}
