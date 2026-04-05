package discord

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sync"

	"github.com/andusystems/sentinel/internal/types"
)

// customTokenRe validates ✏️ custom replacement tokens.
// Must match ^<[A-Z][A-Z0-9_]{0,98}>$ per PLAN.md section 19.
var customTokenRe = regexp.MustCompile(`^<[A-Z][A-Z0-9_]{0,98}>$`)

// ---- Finding reaction handlers ----------------------------------------------

// FindingApproveHandler handles ✅ reactions on finding messages.
// On approval, it writes the suggested replacement to the staging file.
type FindingApproveHandler struct {
	bot         *Bot
	resolutions types.PendingResolutionStore
	worktree    types.WorktreeManager
	fileLocks   types.FileMutexRegistry
	actions     types.ActionLogger
	mu          sync.Map // per-messageID once-mutex for first-reaction-wins
}

func NewFindingApproveHandler(bot *Bot, res types.PendingResolutionStore, wt types.WorktreeManager, fl types.FileMutexRegistry, al types.ActionLogger) *FindingApproveHandler {
	return &FindingApproveHandler{bot: bot, resolutions: res, worktree: wt, fileLocks: fl, actions: al}
}

func (h *FindingApproveHandler) Emoji() string { return "✅" }

func (h *FindingApproveHandler) Handle(ctx context.Context, messageID, userID string) error {
	if !h.bot.IsOperator(userID) {
		return nil // silently ignore non-operators
	}

	// First-reaction-wins: use sync.Map as a set of settled messageIDs.
	if _, loaded := h.mu.LoadOrStore(messageID, struct{}{}); loaded {
		return nil // already handled by another reaction
	}
	defer h.mu.Delete(messageID)

	r, err := h.resolutions.GetByMessageID(ctx, messageID)
	if err != nil || r == nil {
		return err
	}
	if r.Status != types.StatusPending {
		return nil // idempotent: already resolved
	}

	h.fileLocks.Lock(r.Repo, r.Filename)
	defer h.fileLocks.Unlock(r.Repo, r.Filename)

	// Count resolved predecessors to adjust token_index.
	resolved, err := h.resolutions.CountResolvedPredecessors(ctx, r.Repo, r.Filename, 0)
	if err != nil {
		return fmt.Errorf("count resolved predecessors: %w", err)
	}

	// Resolve the tag in the staging file.
	if err := h.worktree.ResolveTag(ctx, r.Repo, r.Filename, 0, resolved, r.SuggestedReplacement); err != nil {
		slog.Error("resolve tag failed", "finding", r.ID, "err", err)
		h.bot.PostChannelMessage(ctx, h.bot.cfg.Discord.FindingsChannelID,
			fmt.Sprintf("⚠️ Failed to resolve finding in `%s` — check logs.", r.Filename))
		return err
	}

	if err := h.resolutions.Approve(ctx, r.ID, userID, r.SuggestedReplacement); err != nil {
		return err
	}

	h.bot.EditFindingFooter(ctx, r.DiscordChannelID, messageID,
		fmt.Sprintf("✅ Approved by <@%s>", userID))
	h.actions.Log(ctx, "finding_approved", r.Repo, r.ID, fmt.Sprintf(`{"user":"%s"}`, userID))
	return nil
}

// FindingRejectHandler handles ❌ reactions on finding messages.
type FindingRejectHandler struct {
	bot         *Bot
	resolutions types.PendingResolutionStore
	actions     types.ActionLogger
}

func NewFindingRejectHandler(bot *Bot, res types.PendingResolutionStore, al types.ActionLogger) *FindingRejectHandler {
	return &FindingRejectHandler{bot: bot, resolutions: res, actions: al}
}

func (h *FindingRejectHandler) Emoji() string { return "❌" }

func (h *FindingRejectHandler) Handle(ctx context.Context, messageID, userID string) error {
	if !h.bot.IsOperator(userID) {
		return nil
	}

	r, err := h.resolutions.GetByMessageID(ctx, messageID)
	if err != nil || r == nil {
		return err
	}
	if r.Status != types.StatusPending {
		return nil
	}

	if err := h.resolutions.Reject(ctx, r.ID, userID); err != nil {
		return err
	}

	h.bot.EditFindingFooter(ctx, r.DiscordChannelID, messageID,
		fmt.Sprintf("❌ Rejected by <@%s>", userID))
	h.actions.Log(ctx, "finding_rejected", r.Repo, r.ID, fmt.Sprintf(`{"user":"%s"}`, userID))
	return nil
}

// FindingReanalyzeHandler handles 🔍 reactions — triggers [AI_ASSISTANT] API re-analysis.
type FindingReanalyzeHandler struct {
	bot         *Bot
	resolutions types.PendingResolutionStore
	[AI_ASSISTANT]      types.ClaudeAPIClient
	actions     types.ActionLogger
}

func NewFindingReanalyzeHandler(bot *Bot, res types.PendingResolutionStore, [AI_ASSISTANT] types.ClaudeAPIClient, al types.ActionLogger) *FindingReanalyzeHandler {
	return &FindingReanalyzeHandler{bot: bot, resolutions: res, [AI_ASSISTANT]: [AI_ASSISTANT], actions: al}
}

func (h *FindingReanalyzeHandler) Emoji() string { return "🔍" }

func (h *FindingReanalyzeHandler) Handle(ctx context.Context, messageID, userID string) error {
	if !h.bot.IsOperator(userID) {
		return nil
	}

	r, err := h.resolutions.GetByMessageID(ctx, messageID)
	if err != nil || r == nil {
		return err
	}
	if r.Status != types.StatusPending {
		return nil
	}

	if err := h.resolutions.MarkReanalyzing(ctx, r.ID); err != nil {
		return err
	}

	h.bot.EditFindingFooter(ctx, r.DiscordChannelID, messageID, "🔍 Re-analyzing with [AI_ASSISTANT] API...")
	h.actions.Log(ctx, "finding_reanalyze_requested", r.Repo, r.ID, fmt.Sprintf(`{"user":"%s"}`, userID))

	// Re-analysis runs async; caller observes new resolution via next bot event.
	return nil
}

// FindingCustomHandler handles ✏️ reactions — operator provides custom replacement token.
type FindingCustomHandler struct {
	bot         *Bot
	resolutions types.PendingResolutionStore
	worktree    types.WorktreeManager
	fileLocks   types.FileMutexRegistry
	actions     types.ActionLogger
}

func NewFindingCustomHandler(bot *Bot, res types.PendingResolutionStore, wt types.WorktreeManager, fl types.FileMutexRegistry, al types.ActionLogger) *FindingCustomHandler {
	return &FindingCustomHandler{bot: bot, resolutions: res, worktree: wt, fileLocks: fl, actions: al}
}

func (h *FindingCustomHandler) Emoji() string { return "✏️" }

func (h *FindingCustomHandler) Handle(ctx context.Context, messageID, userID string) error {
	if !h.bot.IsOperator(userID) {
		return nil
	}

	r, err := h.resolutions.GetByMessageID(ctx, messageID)
	if err != nil || r == nil {
		return err
	}
	if r.Status != types.StatusPending {
		return nil
	}

	// Prompt operator for custom token via DM or thread.
	h.bot.PostChannelMessage(ctx, r.DiscordChannelID,
		fmt.Sprintf("<@%s> React ✏️ acknowledged. Reply with your custom replacement token (format: `<CATEGORY_OR_LABEL>`) in the thread.", userID))

	h.actions.Log(ctx, "finding_custom_requested", r.Repo, r.ID, fmt.Sprintf(`{"user":"%s"}`, userID))
	return nil
}

// ApplyCustomToken applies a validated custom token to a finding.
// Called when operator replies in the thread with their custom token.
func (h *FindingCustomHandler) ApplyCustomToken(ctx context.Context, resolutionID, userID, token string) error {
	if !customTokenRe.MatchString(token) {
		h.bot.PostChannelMessage(ctx, h.bot.cfg.Discord.FindingsChannelID,
			fmt.Sprintf("⚠️ Invalid token format. Must match `<CATEGORY>` (e.g. `<ENV_VAR>`). No changes made."))
		return nil
	}

	r, err := h.resolutions.GetByMessageID(ctx, resolutionID)
	if err != nil || r == nil {
		return err
	}

	h.fileLocks.Lock(r.Repo, r.Filename)
	defer h.fileLocks.Unlock(r.Repo, r.Filename)

	resolved, err := h.resolutions.CountResolvedPredecessors(ctx, r.Repo, r.Filename, 0)
	if err != nil {
		return err
	}

	if err := h.worktree.ResolveTag(ctx, r.Repo, r.Filename, 0, resolved, token); err != nil {
		return err
	}

	if err := h.resolutions.CustomReplace(ctx, r.ID, userID, token); err != nil {
		return err
	}

	h.bot.EditFindingFooter(ctx, r.DiscordChannelID, r.DiscordMessageID,
		fmt.Sprintf("✏️ Custom token applied by <@%s>", userID))
	h.actions.Log(ctx, "finding_custom_applied", r.Repo, r.ID, fmt.Sprintf(`{"user":"%s","token":"%s"}`, userID, token))
	return nil
}
