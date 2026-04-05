package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// CommandDispatcher defines a function to call for a given command.
type CommandDispatcher struct {
	handlers map[string]CommandHandler
}

// CommandHandler processes a parsed sentinel command.
type CommandHandler func(ctx context.Context, bot *Bot, args []string, userID, channelID string) error

// NewCommandDispatcher creates a dispatcher with registered commands.
func NewCommandDispatcher(handlers map[string]CommandHandler) *CommandDispatcher {
	return &CommandDispatcher{handlers: handlers}
}

// handleCommand parses and dispatches a /sentinel command from the command channel.
// Commands take the form: /sentinel <subcommand> [args...]
func handleCommand(ctx context.Context, bot *Bot, message, userID, channelID string) {
	if !strings.HasPrefix(message, "/sentinel") {
		return
	}

	parts := strings.Fields(message)
	if len(parts) < 2 {
		bot.PostChannelMessage(ctx, channelID, usage())
		return
	}

	if !bot.IsOperator(userID) {
		bot.PostChannelMessage(ctx, channelID, "⛔ Only operators can use sentinel commands.")
		return
	}

	subcommand := parts[1]
	args := parts[2:]

	switch subcommand {
	case "status":
		bot.PostChannelMessage(ctx, channelID, "✅ Sentinel is running.")
	case "migrate":
		if len(args) == 0 {
			bot.PostChannelMessage(ctx, channelID, "Usage: `/sentinel migrate <repo> [--force]`")
			return
		}
		slog.Info("migrate command received", "repo", args[0], "user", userID)
		bot.PostChannelMessage(ctx, channelID, fmt.Sprintf("🔄 Migration for `%s` queued. Watch this channel for updates.", args[0]))
	case "sync":
		if len(args) == 0 {
			bot.PostChannelMessage(ctx, channelID, "Usage: `/sentinel sync <repo>`")
			return
		}
		slog.Info("sync command received", "repo", args[0], "user", userID)
		bot.PostChannelMessage(ctx, channelID, fmt.Sprintf("🔄 Sync for `%s` queued.", args[0]))
	case "run":
		slog.Info("manual nightly run requested", "user", userID)
		bot.PostChannelMessage(ctx, channelID, "🌙 Manual nightly run queued.")
	case "help":
		bot.PostChannelMessage(ctx, channelID, usage())
	default:
		bot.PostChannelMessage(ctx, channelID, fmt.Sprintf("Unknown command `%s`. %s", subcommand, usage()))
	}
}

func usage() string {
	return "**Sentinel Commands**\n" +
		"```\n" +
		"/sentinel status              — Check sentinel status\n" +
		"/sentinel migrate <repo>      — Run Mode 4 initial migration\n" +
		"/sentinel migrate <repo> --force  — Force re-migration\n" +
		"/sentinel sync <repo>         — Run Mode 3 incremental sync\n" +
		"/sentinel run                 — Trigger nightly pipeline now\n" +
		"/sentinel help                — Show this help\n" +
		"```"
}
