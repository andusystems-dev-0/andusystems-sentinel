package config

import (
	"fmt"
	"strings"
)

// validate checks all config fields for correctness.
// Critical: category_reasons values must NOT contain '>' (would break sentinel tag parsing).
func validate(cfg *Config) error {
	var errs []string

	// Validate category_reasons: no '>' allowed in any reason string.
	for category, reason := range cfg.Sanitize.CategoryReasons {
		if strings.Contains(reason, ">") {
			errs = append(errs, fmt.Sprintf(
				"sanitize.category_reasons[%s] contains forbidden character '>': %q", category, reason))
		}
		if len(reason) > 200 {
			errs = append(errs, fmt.Sprintf(
				"sanitize.category_reasons[%s] exceeds 200 chars", category))
		}
	}

	// Validate Forgejo base URL.
	if cfg.Forgejo.BaseURL == "" {
		errs = append(errs, "forgejo.base_url is required")
	}

	// Validate Discord config.
	if cfg.Discord.GuildID == "" {
		errs = append(errs, "discord.guild_id is required")
	}
	if cfg.Discord.PRChannelID == "" {
		errs = append(errs, "discord.pr_channel_id is required")
	}
	if cfg.Discord.FindingsChannelID == "" {
		errs = append(errs, "discord.findings_channel_id is required")
	}
	if cfg.Discord.CommandChannelID == "" {
		errs = append(errs, "discord.command_channel_id is required")
	}
	if len(cfg.Discord.OperatorUserIDs) == 0 {
		errs = append(errs, "discord.operator_user_ids must have at least one entry")
	}

	// Validate repos.
	for i, repo := range cfg.Repos {
		if repo.Name == "" {
			errs = append(errs, fmt.Sprintf("repos[%d].name is required", i))
		}
		if repo.ForgejoPath == "" {
			errs = append(errs, fmt.Sprintf("repos[%d].forgejo_path is required", i))
		}
		if repo.MaxTasksPerRun == 0 {
			// Default applied after validation; set here for clarity.
			cfg.Repos[i].MaxTasksPerRun = 10
		}
		// Validate per-repo merge_strategy if set.
		if repo.MergeStrategy != "" && !validMergeStrategy(repo.MergeStrategy) {
			errs = append(errs, fmt.Sprintf(
				"repos[%d].merge_strategy %q is invalid (must be squash|merge|rebase)", i, repo.MergeStrategy))
		}
	}

	// Validate global merge strategy.
	if cfg.PR.MergeStrategy != "" && !validMergeStrategy(cfg.PR.MergeStrategy) {
		errs = append(errs, fmt.Sprintf(
			"pr.merge_strategy %q is invalid (must be squash|merge|rebase)", cfg.PR.MergeStrategy))
	}

	// Validate Ollama config.
	if cfg.Ollama.Host == "" {
		errs = append(errs, "ollama.host is required")
	}
	if cfg.Ollama.Model == "" {
		errs = append(errs, "ollama.model is required")
	}

	if len(errs) > 0 {
		return fmt.Errorf("%d validation error(s):\n  - %s", len(errs), strings.Join(errs, "\n  - "))
	}

	return nil
}

func validMergeStrategy(s string) bool {
	switch s {
	case "squash", "merge", "rebase":
		return true
	}
	return false
}
