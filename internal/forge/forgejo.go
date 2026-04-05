// Package forge implements ForgejoProvider and GitHubProvider.
// Only this package makes Forgejo/GitHub API calls.
package forge

import (
	"context"
	"fmt"
	"strings"

	gitea "code.gitea.io/sdk/gitea"

	"github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/types"
)

// ForgejoClient implements types.ForgejoProvider using the Gitea SDK.
// Forgejo is API-compatible with Gitea.
type ForgejoClient struct {
	cfg    *config.Config
	client *gitea.Client
}

// NewForgejoClient creates a ForgejoClient authenticated with the sentinel service account token.
func NewForgejoClient(cfg *config.Config) (*ForgejoClient, error) {
	client, err := gitea.NewClient(cfg.Forgejo.BaseURL,
		gitea.SetToken(cfg.Forgejo.SentinelToken),
	)
	if err != nil {
		return nil, fmt.Errorf("create forgejo client: %w", err)
	}
	return &ForgejoClient{cfg: cfg, client: client}, nil
}

// splitPath splits "org/repo" into ("org", "repo").
func splitPath(repoPath string) (string, string, error) {
	parts := strings.SplitN(repoPath, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid repo path %q (expected org/repo)", repoPath)
	}
	return parts[0], parts[1], nil
}

// forgejoPaths returns the org and repo name for a repo name (looked up via config).
func (c *ForgejoClient) forgejoPaths(repo string) (string, string, error) {
	for _, r := range c.cfg.Repos {
		if r.Name == repo {
			return splitPath(r.ForgejoPath)
		}
	}
	return "", "", fmt.Errorf("repo %q not found in config", repo)
}

// GetPRDiff fetches the raw diff for a Forgejo pull request.
func (c *ForgejoClient) GetPRDiff(ctx context.Context, repo string, prNumber int) (string, error) {
	org, repoName, err := c.forgejoPaths(repo)
	if err != nil {
		return "", err
	}

	diff, _, err := c.client.GetPullRequestDiff(org, repoName, int64(prNumber), gitea.PullRequestDiffOptions{})
	if err != nil {
		return "", fmt.Errorf("get PR diff %s#%d: %w", repo, prNumber, err)
	}
	return string(diff), nil
}

// CreatePR opens a new pull request on Forgejo.
func (c *ForgejoClient) CreatePR(ctx context.Context, opts types.OpenPROptions) (int, string, error) {
	org, repoName, err := c.forgejoPaths(opts.Repo)
	if err != nil {
		return 0, "", err
	}

	pr, _, err := c.client.CreatePullRequest(org, repoName, gitea.CreatePullRequestOption{
		Head:   opts.Branch,
		Base:   opts.BaseBranch,
		Title:  opts.Title,
		Body:   opts.Body,
		Labels: labelsToIDs(opts.Labels),
	})
	if err != nil {
		return 0, "", fmt.Errorf("create PR %s: %w", opts.Repo, err)
	}
	return int(pr.Index), pr.HTMLURL, nil
}

// CreateBranch creates a new branch from fromSHA on Forgejo.
func (c *ForgejoClient) CreateBranch(ctx context.Context, repo, name, fromSHA string) error {
	org, repoName, err := c.forgejoPaths(repo)
	if err != nil {
		return err
	}
	_, _, err = c.client.CreateBranch(org, repoName, gitea.CreateBranchOption{
		BranchName: name,
		OldBranchName: fromSHA, // Gitea SDK accepts SHA or branch name here
	})
	if err != nil {
		return fmt.Errorf("create branch %s in %s: %w", name, repo, err)
	}
	return nil
}

// MergePR merges a pull request using the specified token (sentinel or operator).
func (c *ForgejoClient) MergePR(ctx context.Context, repo string, prNumber int, strategy, token string) error {
	org, repoName, err := c.forgejoPaths(repo)
	if err != nil {
		return err
	}

	// Create a separate client for this token (operator token for merges).
	mergeClient, err := gitea.NewClient(c.cfg.Forgejo.BaseURL, gitea.SetToken(token))
	if err != nil {
		return fmt.Errorf("create merge client: %w", err)
	}

	style := gitea.MergeStyleSquash
	switch strategy {
	case "merge":
		style = gitea.MergeStyleMerge
	case "rebase":
		style = gitea.MergeStyleRebase
	}

	_, _, err = mergeClient.MergePullRequest(org, repoName, int64(prNumber), gitea.MergePullRequestOption{
		Style: style,
	})
	if err != nil {
		return fmt.Errorf("merge PR %s#%d: %w", repo, prNumber, err)
	}
	return nil
}

// ClosePR closes a pull request without merging.
func (c *ForgejoClient) ClosePR(ctx context.Context, repo string, prNumber int) error {
	org, repoName, err := c.forgejoPaths(repo)
	if err != nil {
		return err
	}
	closed := gitea.StateClosed
	_, _, err = c.client.EditPullRequest(org, repoName, int64(prNumber), gitea.EditPullRequestOption{
		State: &closed,
	})
	if err != nil {
		return fmt.Errorf("close PR %s#%d: %w", repo, prNumber, err)
	}
	return nil
}

// CreateIssue creates a Forgejo issue and returns the issue number.
func (c *ForgejoClient) CreateIssue(ctx context.Context, repo string, opts types.IssueOptions) (int, error) {
	org, repoName, err := c.forgejoPaths(repo)
	if err != nil {
		return 0, err
	}
	issue, _, err := c.client.CreateIssue(org, repoName, gitea.CreateIssueOption{
		Title: opts.Title,
		Body:  opts.Body,
	})
	if err != nil {
		return 0, fmt.Errorf("create issue in %s: %w", repo, err)
	}
	return int(issue.Index), nil
}

// PostPRComment posts a comment on an existing pull request.
func (c *ForgejoClient) PostPRComment(ctx context.Context, repo string, prNumber int, body string) error {
	org, repoName, err := c.forgejoPaths(repo)
	if err != nil {
		return err
	}
	_, _, err = c.client.CreateIssueComment(org, repoName, int64(prNumber), gitea.CreateIssueCommentOption{
		Body: body,
	})
	if err != nil {
		return fmt.Errorf("post PR comment %s#%d: %w", repo, prNumber, err)
	}
	return nil
}

// PostReview posts a review (verdict + body) on a pull request.
func (c *ForgejoClient) PostReview(ctx context.Context, repo string, prNumber int, verdict, body string) error {
	org, repoName, err := c.forgejoPaths(repo)
	if err != nil {
		return err
	}

	reviewType := gitea.ReviewStateComment
	switch verdict {
	case "APPROVE":
		reviewType = gitea.ReviewStateApproved
	case "REQUEST_CHANGES":
		reviewType = gitea.ReviewStateRequestChanges
	}

	_, _, err = c.client.CreatePullReview(org, repoName, int64(prNumber), gitea.CreatePullReviewOptions{
		State: reviewType,
		Body:  body,
	})
	if err != nil {
		return fmt.Errorf("post review %s#%d: %w", repo, prNumber, err)
	}
	return nil
}

// ListOpenPRs lists all open pull requests for a repo.
func (c *ForgejoClient) ListOpenPRs(ctx context.Context, repo string) ([]types.ForgejoPR, error) {
	org, repoName, err := c.forgejoPaths(repo)
	if err != nil {
		return nil, err
	}

	prs, _, err := c.client.ListRepoPullRequests(org, repoName, gitea.ListPullRequestsOptions{
		State: gitea.StateOpen,
	})
	if err != nil {
		return nil, fmt.Errorf("list open PRs for %s: %w", repo, err)
	}

	var out []types.ForgejoPR
	for _, pr := range prs {
		fp := types.ForgejoPR{
			Number:  int(pr.Index),
			Title:   pr.Title,
			URL:     pr.HTMLURL,
			HeadRef: pr.Head.Ref,
			BaseRef: pr.Base.Ref,
			State:   string(pr.State),
		}
		if pr.Merged != nil {
			fp.MergedAt = pr.Merged
		}
		out = append(out, fp)
	}
	return out, nil
}

// GetWebhookEvents lists the event types for existing webhooks on a repo.
func (c *ForgejoClient) GetWebhookEvents(ctx context.Context, repo string) ([]string, error) {
	org, repoName, err := c.forgejoPaths(repo)
	if err != nil {
		return nil, err
	}
	hooks, _, err := c.client.ListRepoHooks(org, repoName, gitea.ListHooksOptions{})
	if err != nil {
		return nil, fmt.Errorf("list hooks for %s: %w", repo, err)
	}

	var events []string
	for _, h := range hooks {
		events = append(events, h.Events...)
	}
	return events, nil
}

// RegisterWebhook creates or updates the sentinel webhook on a Forgejo repo.
func (c *ForgejoClient) RegisterWebhook(ctx context.Context, repo, url, secret string, events []string) error {
	return RegisterSentinelWebhook(ctx, c.client, c.cfg, repo, url, secret, events)
}

// GetHeadSHA returns the HEAD SHA of the given branch on Forgejo.
func (c *ForgejoClient) GetHeadSHA(ctx context.Context, repo, branch string) (string, error) {
	org, repoName, err := c.forgejoPaths(repo)
	if err != nil {
		return "", err
	}
	ref, _, err := c.client.GetRepoBranch(org, repoName, branch)
	if err != nil {
		return "", fmt.Errorf("get head SHA for %s/%s: %w", repo, branch, err)
	}
	return ref.Commit.ID, nil
}

// ---- helpers ----------------------------------------------------------------

// labelsToIDs converts label name strings to Gitea label IDs.
// Forgejo creates labels by name — this placeholder returns empty slice.
// Full label support requires looking up label IDs by name first.
func labelsToIDs(labels []string) []int64 {
	// Label ID lookup would require a separate API call. For now we submit
	// without label IDs; labels can be applied manually or via a future enhancement.
	_ = labels
	return nil
}
