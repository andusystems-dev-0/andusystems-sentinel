package prnotify

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/andusystems/sentinel/internal/types"
)

// background returns a detached context for fire-and-forget goroutines.
// We use context.Background() so that cancelling the webhook ctx doesn't
// abort an in-flight sync that was already triggered.
var background = context.Background

// PRWebhookPayload represents the fields sentinel reads from a Forgejo pull_request webhook.
type PRWebhookPayload struct {
	Action string `json:"action"`
	Number int    `json:"number"`
	PullRequest struct {
		Index  int    `json:"number"`
		Merged bool   `json:"merged"`
		Head   struct{ Ref string `json:"ref"` } `json:"head"`
		Base   struct{ Ref string `json:"ref"` } `json:"base"`
	} `json:"pull_request"`
	Repository struct {
		Name string `json:"name"`
	} `json:"repository"`
}

// SyncHandler processes pull_request webhook events for Forgejo→Discord sync.
type SyncHandler struct {
	notifier   *Notifier
	prStore    types.SentinelPRStore
	actions    types.ActionLogger
	syncRunner types.SyncRunner // if set, auto-syncs to GitHub after sentinel PR merge
}

// NewSyncHandler creates a SyncHandler.
// syncRunner may be nil; if non-nil, a sync is triggered after every sentinel PR merge.
func NewSyncHandler(n *Notifier, prStore types.SentinelPRStore, actions types.ActionLogger, syncRunner types.SyncRunner) *SyncHandler {
	return &SyncHandler{notifier: n, prStore: prStore, actions: actions, syncRunner: syncRunner}
}

// HandlePRWebhook processes a Forgejo pull_request event.
// Dispatches sentinel PR events to Forgejo→Discord sync,
// and developer PR events to housekeeping PR management.
func (s *SyncHandler) HandlePRWebhook(ctx context.Context, event types.ForgejoEvent) {
	var payload PRWebhookPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		slog.Error("parse PR webhook payload", "err", err)
		return
	}

	repo := payload.Repository.Name
	prNumber := payload.PullRequest.Index
	if prNumber == 0 {
		prNumber = payload.Number
	}
	headRef := payload.PullRequest.Head.Ref

	// Determine if this is a sentinel-owned PR.
	if strings.HasPrefix(headRef, "sentinel/") {
		s.handleSentinelPR(ctx, repo, prNumber, payload)
	} else {
		s.handleDeveloperPR(ctx, repo, prNumber, payload)
	}
}

// handleSentinelPR processes merge/close of a sentinel-owned PR from Forgejo UI.
func (s *SyncHandler) handleSentinelPR(ctx context.Context, repo string, prNumber int, payload PRWebhookPayload) {
	if payload.Action != "closed" {
		return // Only care about close events (which includes merges)
	}

	merged := payload.PullRequest.Merged
	if err := s.notifier.HandleForgejoResolution(ctx, repo, prNumber, merged); err != nil {
		slog.Error("handle Forgejo resolution", "repo", repo, "pr", prNumber, "err", err)
	}

	// Auto-sync to GitHub after a sentinel PR is merged.
	if merged && s.syncRunner != nil {
		go func() {
			slog.Info("auto-sync triggered by PR merge", "repo", repo, "pr", prNumber)
			if err := s.syncRunner.Sync(background(), repo); err != nil {
				slog.Error("auto-sync failed", "repo", repo, "pr", prNumber, "err", err)
			}
		}()
	}
}

// handleDeveloperPR handles close/merge of a non-sentinel PR.
// Checks for associated housekeeping PRs and notifies accordingly.
func (s *SyncHandler) handleDeveloperPR(ctx context.Context, repo string, prNumber int, payload PRWebhookPayload) {
	if payload.Action != "closed" {
		return
	}

	// Look for an open sentinel PR associated with this developer PR (housekeeping companion).
	// The association is stored via RelatedPRNumber in sentinel_prs.
	openPRs, err := s.prStore.GetOpenPRsForRepo(ctx, repo)
	if err != nil {
		slog.Error("get open PRs for housekeeping check", "repo", repo, "err", err)
		return
	}

	for _, pr := range openPRs {
		if pr.RelatedPRNumber != prNumber {
			continue
		}

		merged := payload.PullRequest.Merged
		if merged {
			// Notify that original PR merged; housekeeping can now be merged.
			s.notifier.forge.PostPRComment(ctx, repo, pr.PRNumber,
				"Original PR #"+itoa(prNumber)+" was merged. You can now merge this housekeeping PR at your convenience.")
			slog.Info("housekeeping PR notified: original merged", "housekeeping_pr", pr.PRNumber)
		} else {
			// Original PR closed without merge — auto-close housekeeping PR.
			s.notifier.forge.PostPRComment(ctx, repo, pr.PRNumber,
				"Original PR #"+itoa(prNumber)+" was closed without merging. This housekeeping PR is no longer relevant.")
			s.notifier.forge.ClosePR(ctx, repo, pr.PRNumber)
			s.prStore.MarkClosed(ctx, pr.ID, "sentinel-auto-close")
			s.notifier.bot.EditPRFooter(ctx, pr.DiscordChannelID, pr.DiscordMessageID,
				"❌ Auto-closed — original PR closed without merge")
		}
		s.actions.Log(ctx, "housekeeping_pr_handled", repo, pr.ID,
			`{"original_pr":`+itoa(prNumber)+`,"merged":`+btoa(merged)+`}`)
		break
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	result := ""
	for n > 0 {
		result = string(rune('0'+n%10)) + result
		n /= 10
	}
	return result
}

func btoa(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
