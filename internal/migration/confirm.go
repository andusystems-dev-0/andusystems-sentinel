// Package migration implements Mode 4 full-repo initial migration.
// Must-NOT make concurrent modifications to other repos.
package migration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/andusystems/sentinel/internal/store"
	"github.com/andusystems/sentinel/internal/types"
)

// ConfirmForce posts a confirmation message to Discord and polls the DB
// for operator ✅/❌ reaction (set by the reaction handler).
// Returns true if confirmed, false if rejected or TTL expired.
func ConfirmForce(
	ctx context.Context,
	repo string,
	db *store.DB,
	discord types.DiscordBot,
	channelID string,
	ttlMinutes int,
) (bool, error) {
	msgID, err := discord.PostChannelMessageID(ctx, channelID,
		fmt.Sprintf(
			"⚠️ **Force migration requested for `%s`**\n\n"+
				"The GitHub mirror for this repo is **non-empty**. Proceeding will overwrite its contents.\n\n"+
				"React ✅ to confirm or ❌ to cancel. This confirmation expires in %d minutes.",
			repo, ttlMinutes,
		),
	)
	if err != nil {
		return false, fmt.Errorf("post force confirmation: %w", err)
	}

	discord.SeedCommandReactions(ctx, channelID, msgID)

	// Store the confirmation with TTL and the Discord message ID.
	expires := time.Now().Add(time.Duration(ttlMinutes) * time.Minute)
	confirmation := store.PendingConfirmation{
		ID:               newID(),
		Kind:             "force_migration",
		Repo:             repo,
		DiscordMessageID: msgID,
		DiscordChannelID: channelID,
		RequestedBy:      "operator",
		CreatedAt:        time.Now(),
		ExpiresAt:        expires,
		Status:           "pending",
	}
	if err := db.Confirmations.Create(ctx, confirmation); err != nil {
		return false, err
	}

	// Poll DB for status change set by reaction handler.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	deadline := time.After(time.Duration(ttlMinutes) * time.Minute)

	for {
		select {
		case <-deadline:
			db.Confirmations.SetStatus(ctx, confirmation.ID, "expired")
			discord.PostChannelMessage(ctx, channelID,
				"⏱️ Confirmation expired — re-run the command to try again.")
			return false, nil

		case <-ticker.C:
			c, err := db.Confirmations.GetByMessageID(ctx, confirmation.DiscordMessageID)
			if err != nil || c == nil {
				continue
			}
			switch c.Status {
			case "confirmed":
				return true, nil
			case "rejected":
				discord.PostChannelMessage(ctx, channelID, "❌ Migration cancelled.")
				return false, nil
			case "expired":
				return false, nil
			}

		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}
