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

// MigrationKind distinguishes confirmation message wording.
type MigrationKind string

const (
	// MigrationInitial — first-time migration to a fresh/empty GitHub mirror.
	MigrationInitial MigrationKind = "initial"
	// MigrationForce — re-migration that will overwrite existing GitHub history.
	MigrationForce MigrationKind = "force"
	// MigrationBootstrap — automatic daemon-startup migration of an empty mirror.
	MigrationBootstrap MigrationKind = "bootstrap"
)

// ConfirmMigration posts a confirmation message to Discord and polls the DB
// for operator ✅/❌ reaction (set by the reaction handler). Message wording
// varies by kind (initial / force / bootstrap). Returns true if confirmed,
// false if rejected or TTL expired.
func ConfirmMigration(
	ctx context.Context,
	repo string,
	githubPath string,
	kind MigrationKind,
	db *store.DB,
	discord types.DiscordBot,
	channelID string,
	ttlMinutes int,
) (bool, error) {
	var body string
	switch kind {
	case MigrationForce:
		body = fmt.Sprintf(
			"⚠️ **Force migration requested for `%s`**\n\n"+
				"The GitHub mirror `%s` is **non-empty**. Proceeding will overwrite its contents.\n\n"+
				"React ✅ to confirm or ❌ to cancel. Expires in %d minutes.",
			repo, githubPath, ttlMinutes,
		)
	case MigrationBootstrap:
		body = fmt.Sprintf(
			"🤖 **Auto-bootstrap migration requested for `%s`**\n\n"+
				"The GitHub mirror `%s` is empty or missing. Sentinel will run a full sanitization pass "+
				"and push the Forgejo HEAD to GitHub.\n\n"+
				"React ✅ to confirm or ❌ to skip this repo. Expires in %d minutes.",
			repo, githubPath, ttlMinutes,
		)
	default: // MigrationInitial
		body = fmt.Sprintf(
			"🚀 **Initial migration requested for `%s`**\n\n"+
				"Sentinel will sanitize every file at Forgejo HEAD and force-push a single commit to `%s`.\n\n"+
				"React ✅ to confirm or ❌ to cancel. Expires in %d minutes.",
			repo, githubPath, ttlMinutes,
		)
	}

	msgID, err := discord.PostChannelMessageID(ctx, channelID, body)
	if err != nil {
		return false, fmt.Errorf("post migration confirmation: %w", err)
	}

	discord.SeedCommandReactions(ctx, channelID, msgID)

	// Store the confirmation with TTL and the Discord message ID.
	expires := time.Now().Add(time.Duration(ttlMinutes) * time.Minute)
	confirmation := store.PendingConfirmation{
		ID:               newID(),
		Kind:             "migration_" + string(kind),
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
