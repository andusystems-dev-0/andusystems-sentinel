package prnotify

import (
	"context"
	"log/slog"
	"time"

	"github.com/andusystems/sentinel/internal/store"
)

// MentionTracker implements per-repo @here cooldown backed by sentinel_actions DB.
// The cooldown is per-repo and survives process restarts.
type MentionTracker struct {
	actions         *store.ActionStore
	cooldownMinutes int
}

// NewMentionTracker creates a MentionTracker.
func NewMentionTracker(actions *store.ActionStore, cooldownMinutes int) *MentionTracker {
	return &MentionTracker{actions: actions, cooldownMinutes: cooldownMinutes}
}

// ShouldMention returns true if the @here cooldown for the given repo has expired.
func (m *MentionTracker) ShouldMention(ctx context.Context, repo string) bool {
	lastMention, err := m.actions.LatestMentionAt(ctx, repo)
	if err != nil {
		slog.Warn("mention tracker DB error", "repo", repo, "err", err)
		return false // Fail safe: suppress mention on error
	}

	if lastMention.IsZero() {
		return true // Never mentioned
	}

	return time.Since(lastMention) >= time.Duration(m.cooldownMinutes)*time.Minute
}

// RecordMention logs an @here mention action for the given repo.
func (m *MentionTracker) RecordMention(ctx context.Context, repo string) {
	if err := m.actions.Log(ctx, "discord_mention_here", repo, "", ""); err != nil {
		slog.Error("record mention failed", "repo", repo, "err", err)
	}
}
