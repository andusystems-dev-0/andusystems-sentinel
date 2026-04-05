package forge

import (
	"context"
	"fmt"
	"strings"

	gitea "code.gitea.io/sdk/gitea"

	"github.com/andusystems/sentinel/internal/config"
)

// RegisterSentinelWebhook idempotently registers the sentinel webhook on a Forgejo repo.
// If a webhook with the sentinel URL already exists, it is updated. Otherwise created.
//
// Called at startup for all watched repos.
// Token rotation: update Kubernetes Secret + restart pod (auto-updates on startup).
func RegisterSentinelWebhook(
	ctx context.Context,
	client *gitea.Client,
	cfg *config.Config,
	repo, webhookURL, secret string,
	events []string,
) error {
	org, repoName, err := orgRepoForName(cfg, repo)
	if err != nil {
		return err
	}

	// List existing hooks.
	hooks, _, err := client.ListRepoHooks(org, repoName, gitea.ListHooksOptions{})
	if err != nil {
		return fmt.Errorf("list hooks for %s: %w", repo, err)
	}

	// Find existing sentinel hook by URL prefix.
	for _, h := range hooks {
		if u, ok := h.Config["url"]; ok && strings.Contains(u, webhookURL) {
			// Update existing.
			_, err := client.EditRepoHook(org, repoName, h.ID, gitea.EditHookOption{
				Config: map[string]string{
					"url":          webhookURL,
					"content_type": "json",
					"secret":       secret,
				},
				Events: events,
				Active: boolPtr(true),
			})
			if err != nil {
				return fmt.Errorf("update webhook for %s: %w", repo, err)
			}
			return nil
		}
	}

	// Create new.
	_, _, err = client.CreateRepoHook(org, repoName, gitea.CreateHookOption{
		Type: "gitea",
		Config: map[string]string{
			"url":          webhookURL,
			"content_type": "json",
			"secret":       secret,
		},
		Events: events,
		Active: true,
	})
	if err != nil {
		return fmt.Errorf("create webhook for %s: %w", repo, err)
	}
	return nil
}

// RegisterAllWebhooks registers the sentinel webhook on all watched repos at startup.
// Use ForgejoClient.RegisterAllWebhooks from application code — this function is
// exported for testing only.
func RegisterAllWebhooks(ctx context.Context, client *gitea.Client, cfg *config.Config, ingressHost string) error {
	webhookURL := fmt.Sprintf("https://%s/webhooks/forgejo", ingressHost)
	events := []string{"pull_request", "push", "issue_comment"}

	for _, repo := range cfg.Repos {
		if repo.Excluded {
			continue
		}
		if err := RegisterSentinelWebhook(ctx, client, cfg, repo.Name, webhookURL, cfg.Webhook.Secret, events); err != nil {
			// Log but don't abort — other repos should still be registered.
			fmt.Printf("WARN: failed to register webhook for %s: %v\n", repo.Name, err)
		}
	}
	return nil
}

func orgRepoForName(cfg *config.Config, repo string) (string, string, error) {
	for _, r := range cfg.Repos {
		if r.Name == repo {
			return splitPath(r.ForgejoPath)
		}
	}
	return "", "", fmt.Errorf("repo %q not found in config", repo)
}

// RegisterAllWebhooks registers the sentinel webhook on all watched repos using
// this client's credentials. Intended to be called at daemon startup.
func (c *ForgejoClient) RegisterAllWebhooks(ctx context.Context, ingressHost string) error {
	return RegisterAllWebhooks(ctx, c.client, c.cfg, ingressHost)
}

func boolPtr(b bool) *bool { return &b }
