package config

import (
	"fmt"
	"os"
)

// resolveEnv populates secret fields from environment variables.
// Secrets are never stored in config.yaml — they come from Kubernetes Secrets
// mounted as env vars.
func resolveEnv(cfg *Config) error {
	var missing []string

	cfg.Forgejo.SentinelToken = os.Getenv("FORGEJO_SENTINEL_TOKEN")
	if cfg.Forgejo.SentinelToken == "" {
		missing = append(missing, "FORGEJO_SENTINEL_TOKEN")
	}

	cfg.Forgejo.OperatorToken = os.Getenv("FORGEJO_OPERATOR_TOKEN")
	if cfg.Forgejo.OperatorToken == "" {
		missing = append(missing, "FORGEJO_OPERATOR_TOKEN")
	}

	cfg.Discord.BotToken = os.Getenv("DISCORD_BOT_TOKEN")
	if cfg.Discord.BotToken == "" {
		missing = append(missing, "DISCORD_BOT_TOKEN")
	}

	cfg.GitHub.Token = os.Getenv("GITHUB_TOKEN")
	if cfg.GitHub.Token == "" {
		missing = append(missing, "GITHUB_TOKEN")
	}

	cfg.ClaudeAPI.APIKey = os.Getenv("ANTHROPIC_API_KEY")
	// [AI_ASSISTANT] API key is optional — only needed for Layer 3 sanitization.

	cfg.Webhook.Secret = os.Getenv("FORGEJO_WEBHOOK_SECRET")
	if cfg.Webhook.Secret == "" {
		missing = append(missing, "FORGEJO_WEBHOOK_SECRET")
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %v", missing)
	}

	return nil
}
