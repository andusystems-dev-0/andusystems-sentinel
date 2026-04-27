package forge

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	gogithub "github.com/google/go-github/v66/github"
	"golang.org/x/oauth2"

	"github.com/andusystems/sentinel/internal/config"
)

// GitHubClient implements types.GitHubProvider using the go-github library.
// Used exclusively for the GitHub mirror (Modes 3 and 4).
type GitHubClient struct {
	cfg    *config.Config
	client *gogithub.Client
}

// NewGitHubClient creates a GitHubClient authenticated with the GitHub token.
func NewGitHubClient(cfg *config.Config) *GitHubClient {
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: cfg.GitHub.Token})
	tc := oauth2.NewClient(context.Background(), ts)
	client := gogithub.NewClient(tc)

	if cfg.GitHub.BaseURL != "" && cfg.GitHub.BaseURL != "https://api.github.com" {
		// Support GitHub Enterprise.
		client, _ = gogithub.NewClient(tc).WithEnterpriseURLs(cfg.GitHub.BaseURL+"/", cfg.GitHub.BaseURL+"/")
	}

	return &GitHubClient{cfg: cfg, client: client}
}

// splitGitHubPath splits "org/repo" into ("org", "repo").
func splitGitHubPath(repoPath string) (string, string, error) {
	parts := strings.SplitN(repoPath, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid GitHub path %q (expected org/repo)", repoPath)
	}
	return parts[0], parts[1], nil
}

func (c *GitHubClient) githubPaths(repoPath string) (string, string, error) {
	return splitGitHubPath(repoPath)
}

// EnsureRepo creates the GitHub mirror repo if it doesn't exist, and sets its
// description. If the repo already exists, the description is updated to match
// description (unless description is empty, in which case it is left alone).
func (c *GitHubClient) EnsureRepo(ctx context.Context, repoPath, description string) error {
	owner, name, err := c.githubPaths(repoPath)
	if err != nil {
		return err
	}

	existing, resp, err := c.client.Repositories.Get(ctx, owner, name)
	if err == nil {
		// Already exists — sync description if it drifted.
		if description == "" {
			return nil
		}
		if existing.GetDescription() == description {
			return nil
		}
		_, _, err = c.client.Repositories.Edit(ctx, owner, name, &gogithub.Repository{
			Description: gogithub.String(description),
		})
		if err != nil {
			return fmt.Errorf("update github repo description %s: %w", repoPath, err)
		}
		return nil
	}
	if resp == nil || resp.StatusCode != 404 {
		return fmt.Errorf("check github repo %s: %w", repoPath, err)
	}

	// Create it. POST /orgs/{owner}/repos works for organizations; for user
	// accounts that endpoint 404s and we must use POST /user/repos, which
	// always creates under the authenticated user. We detect the owner type
	// via Users.Get and pick the correct endpoint.
	private := true
	newRepo := &gogithub.Repository{
		Name:    gogithub.String(name),
		Private: &private,
	}
	if description != "" {
		newRepo.Description = gogithub.String(description)
	}

	ownerInfo, _, ownerErr := c.client.Users.Get(ctx, owner)
	if ownerErr != nil {
		return fmt.Errorf("lookup github owner %s: %w", owner, ownerErr)
	}
	createOwner := owner
	if ownerInfo.GetType() == "User" {
		// Verify the configured owner is the authenticated user — POST
		// /user/repos can only create under the token's own account.
		authUser, _, authErr := c.client.Users.Get(ctx, "")
		if authErr != nil {
			return fmt.Errorf("lookup authenticated user: %w", authErr)
		}
		if !strings.EqualFold(authUser.GetLogin(), owner) {
			return fmt.Errorf(
				"cannot create repo under user %q with token belonging to %q: GitHub only allows user-account repo creation under the authenticated user",
				owner, authUser.GetLogin())
		}
		createOwner = ""
	}
	_, _, err = c.client.Repositories.Create(ctx, createOwner, newRepo)
	if err != nil {
		return fmt.Errorf("create github mirror repo %s: %w", repoPath, err)
	}
	return nil
}

// IsEmpty reports whether the GitHub repo at repoPath contains zero commits,
// or does not exist at all. Bootstrap uses this to decide whether an initial
// Mode 4 migration is required.
func (c *GitHubClient) IsEmpty(ctx context.Context, repoPath string) (bool, error) {
	owner, name, err := c.githubPaths(repoPath)
	if err != nil {
		return false, err
	}
	_, resp, err := c.client.Repositories.ListCommits(ctx, owner, name,
		&gogithub.CommitsListOptions{ListOptions: gogithub.ListOptions{PerPage: 1}})
	if err == nil {
		return false, nil
	}
	// GitHub returns 409 Conflict with "Git Repository is empty" for an
	// existing-but-empty repo, and 404 if the repo doesn't exist.
	if resp != nil && (resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusNotFound) {
		return true, nil
	}
	return false, fmt.Errorf("list commits %s: %w", repoPath, err)
}

// PushFile creates or updates a single file in the GitHub mirror repo.
func (c *GitHubClient) PushFile(ctx context.Context, repoPath, filename, commitMsg string, content []byte) error {
	owner, name, err := c.githubPaths(repoPath)
	if err != nil {
		return err
	}

	// Get existing file SHA if it exists (required for update).
	var existingSHA *string
	existing, _, _, err := c.client.Repositories.GetContents(ctx, owner, name, filename, nil)
	if err == nil && existing != nil {
		existingSHA = existing.SHA
	}

	encoded := base64.StdEncoding.EncodeToString(content)
	opts := &gogithub.RepositoryContentFileOptions{
		Message: gogithub.String(commitMsg),
		Content: []byte(encoded),
		SHA:     existingSHA,
	}

	if existingSHA != nil {
		_, _, err = c.client.Repositories.UpdateFile(ctx, owner, name, filename, opts)
	} else {
		_, _, err = c.client.Repositories.CreateFile(ctx, owner, name, filename, opts)
	}
	if err != nil {
		return fmt.Errorf("push file %s to github %s: %w", filename, repoPath, err)
	}
	return nil
}

// PushFiles pushes multiple files as separate commits to the GitHub mirror.
// For large batches, prefer using the git push path via WorktreeManager.
func (c *GitHubClient) PushFiles(ctx context.Context, repoPath, commitMsg string, files map[string][]byte) error {
	for filename, content := range files {
		msg := fmt.Sprintf("%s: %s", commitMsg, filename)
		if err := c.PushFile(ctx, repoPath, filename, msg, content); err != nil {
			return err
		}
	}
	return nil
}
