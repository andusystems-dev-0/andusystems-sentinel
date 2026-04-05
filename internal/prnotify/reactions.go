package prnotify

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/andusystems/sentinel/internal/types"
)

// PRApproveHandler handles ✅ reactions on PR notification messages.
// Calls ForgejoProvider.MergePR with the operator token.
type PRApproveHandler struct {
	notifier *Notifier
	prStore  types.SentinelPRStore
	mu       sync.Map // per-messageID first-reaction-wins lock
}

func NewPRApproveHandler(n *Notifier, prStore types.SentinelPRStore) *PRApproveHandler {
	return &PRApproveHandler{notifier: n, prStore: prStore}
}

func (h *PRApproveHandler) Emoji() string { return "✅" }

func (h *PRApproveHandler) Handle(ctx context.Context, messageID, userID string) error {
	if !h.notifier.bot.IsOperator(userID) {
		return nil
	}

	// First-reaction-wins.
	if _, loaded := h.mu.LoadOrStore(messageID, struct{}{}); loaded {
		return nil
	}
	defer h.mu.Delete(messageID)

	pr, err := h.prStore.GetByMessageID(ctx, messageID)
	if err != nil || pr == nil {
		return err
	}

	return h.notifier.HandleApprove(ctx, pr, userID)
}

// PRCloseHandler handles ❌ reactions on PR notification messages.
type PRCloseHandler struct {
	notifier *Notifier
	prStore  types.SentinelPRStore
}

func NewPRCloseHandler(n *Notifier, prStore types.SentinelPRStore) *PRCloseHandler {
	return &PRCloseHandler{notifier: n, prStore: prStore}
}

func (h *PRCloseHandler) Emoji() string { return "❌" }

func (h *PRCloseHandler) Handle(ctx context.Context, messageID, userID string) error {
	if !h.notifier.bot.IsOperator(userID) {
		return nil
	}

	pr, err := h.prStore.GetByMessageID(ctx, messageID)
	if err != nil || pr == nil {
		return err
	}

	return h.notifier.HandleClose(ctx, pr, userID)
}

// PRDiscussHandler handles 💬 reactions on PR notification messages.
type PRDiscussHandler struct {
	notifier *Notifier
	prStore  types.SentinelPRStore
}

func NewPRDiscussHandler(n *Notifier, prStore types.SentinelPRStore) *PRDiscussHandler {
	return &PRDiscussHandler{notifier: n, prStore: prStore}
}

func (h *PRDiscussHandler) Emoji() string { return "💬" }

func (h *PRDiscussHandler) Handle(ctx context.Context, messageID, userID string) error {
	pr, err := h.prStore.GetByMessageID(ctx, messageID)
	if err != nil || pr == nil {
		if err != nil {
			slog.Warn("PR not found for discuss reaction", "messageID", messageID)
		}
		return err
	}

	if pr.DiscordThreadID != "" {
		// Thread already open; just post a message pointing to it.
		h.notifier.bot.PostChannelMessage(ctx, pr.DiscordChannelID,
			fmt.Sprintf("<@%s> Discussion thread already open: <#%s>", userID, pr.DiscordThreadID))
		return nil
	}

	return h.notifier.HandleDiscuss(ctx, pr)
}
