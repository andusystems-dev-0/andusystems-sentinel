package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"text/template"

	ollamaapi "github.com/ollama/ollama/api"

	"github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/types"
)

// Client implements types.LLMClient using the Ollama API.
// All calls acquire the global Ollama semaphore (max 1 concurrent).
type Client struct {
	ollama *ollamaapi.Client
	cfg    *config.Config
}

// NewClient creates an LLMClient connected to the Ollama server.
func NewClient(cfg *config.Config) (*Client, error) {
	u, err := url.Parse(cfg.Ollama.Host)
	if err != nil {
		return nil, fmt.Errorf("parse ollama host %q: %w", cfg.Ollama.Host, err)
	}
	c := ollamaapi.NewClient(u, http.DefaultClient)
	return &Client{ollama: c, cfg: cfg}, nil
}

// Analyze calls Role A (nightly analyst) for a batch of file diffs.
// Returns TaskSpec array; invalid items are skipped with a warning.
func (c *Client) Analyze(ctx context.Context, opts types.AnalyzeOpts) ([]types.TaskSpec, error) {
	userPrompt := buildAnalyzePrompt(opts)
	response, err := c.callOllama(ctx, roleASystem, userPrompt)
	if err != nil {
		return nil, err
	}

	var specs []types.TaskSpec
	if err := unmarshalJSON(response, &specs); err != nil {
		// Retry once.
		slog.Warn("LLM Analyze: invalid JSON, retrying", "err", err)
		response, err = c.callOllama(ctx, roleASystem, userPrompt)
		if err != nil {
			return nil, err
		}
		if err := unmarshalJSON(response, &specs); err != nil {
			return nil, fmt.Errorf("Analyze: invalid JSON after retry: %w", err)
		}
	}

	return validateTaskSpecs(specs), nil
}

// ReviewPR calls Role B (PR reviewer).
func (c *Client) ReviewPR(ctx context.Context, opts types.ReviewOpts) (*types.ReviewResult, error) {
	userPrompt := fmt.Sprintf("Repo: %s  PR: #%d — %s\nBase branch: %s\n\nDiff:\n%s\n\nProduce a JSON review result.",
		opts.Repo, opts.PRNumber, opts.PRTitle, opts.BaseBranch, opts.Diff)

	response, err := c.callOllama(ctx, roleBSystem, userPrompt)
	if err != nil {
		return nil, err
	}

	var result types.ReviewResult
	if err := unmarshalJSON(response, &result); err != nil {
		// Retry once.
		response, err = c.callOllama(ctx, roleBSystem, userPrompt)
		if err != nil {
			return nil, err
		}
		if err := unmarshalJSON(response, &result); err != nil {
			return nil, fmt.Errorf("ReviewPR: invalid JSON after retry: %w", err)
		}
	}

	// Validate verdict enum.
	switch result.Verdict {
	case "APPROVE", "REQUEST_CHANGES", "COMMENT":
	default:
		result.Verdict = "COMMENT"
	}

	return &result, nil
}

// WriteProse calls Role C (prose writer).
func (c *Client) WriteProse(ctx context.Context, opts types.ProseOpts) (string, error) {
	var userPrompt string
	switch opts.Role {
	case "pr_summary":
		userPrompt = fmt.Sprintf("Write a single concise paragraph summarizing this pull request for a Discord notification:\n\n%s", opts.Context)
	case "issue":
		userPrompt = fmt.Sprintf("Write a Forgejo issue title and body for the following finding:\n\n%s\n\nFormat: Title on line 1, blank line, then body.", opts.Context)
	case "readme":
		userPrompt = fmt.Sprintf("Write a README section about:\n\n%s\n\nUse Markdown. 2-4 paragraphs.", opts.Context)
	case "dependency_pr":
		userPrompt = fmt.Sprintf("Write a 2-sentence PR description:\n\n%s", opts.Context)
	case "release_notes":
		userPrompt = fmt.Sprintf("Write release notes for:\n\n%s", opts.Context)
	case "changelog":
		userPrompt = opts.Context // already formatted by ChangelogManager
	default:
		userPrompt = opts.Context
	}

	response, err := c.callOllama(ctx, roleCSystem, userPrompt)
	if err != nil {
		// Retry once.
		response, err = c.callOllama(ctx, roleCSystem, userPrompt)
		if err != nil {
			return "Automated prose generation unavailable.", nil
		}
	}

	result := strings.TrimSpace(response)
	if result == "" {
		return "Automated prose generation unavailable.", nil
	}
	return result, nil
}

// SanitizeChunk calls Role D (sanitization semantic pass).
// Returns raw findings; caller validates and assigns layer.
func (c *Client) SanitizeChunk(ctx context.Context, content string) ([]types.SanitizationFinding, error) {
	userPrompt := fmt.Sprintf("Identify any remaining sensitive values not already redacted. Return JSON array.\n\nContent:\n%s", content)
	response, err := c.callOllama(ctx, roleDSystem, userPrompt)
	if err != nil {
		return nil, err
	}

	var findings []types.SanitizationFinding
	if err := unmarshalJSON(response, &findings); err != nil {
		// Retry once.
		response, err = c.callOllama(ctx, roleDSystem, userPrompt)
		if err != nil {
			return nil, err
		}
		if err := unmarshalJSON(response, &findings); err != nil {
			return nil, fmt.Errorf("SanitizeChunk: invalid JSON after retry: %w", err)
		}
	}
	return findings, nil
}

// AnswerThread calls Role E (finding) or Role F (PR) for discussion thread Q&A.
func (c *Client) AnswerThread(ctx context.Context, opts types.ThreadOpts) (string, error) {
	systemPrompt := roleESystem
	if opts.Role == "pr" {
		systemPrompt = roleFSystem
	}

	userPrompt := fmt.Sprintf("%s\n\nOperator question: %s", opts.Context, opts.Question)
	response, err := c.callOllama(ctx, systemPrompt, userPrompt)
	if err != nil {
		return "Unable to answer at this time. Please review the finding directly.", nil
	}
	result := strings.TrimSpace(response)
	if result == "" {
		return "Unable to answer at this time.", nil
	}
	return result, nil
}

// WriteHousekeepingBody calls Role G (housekeeping PR body).
func (c *Client) WriteHousekeepingBody(ctx context.Context, opts types.HousekeepingOpts) (string, error) {
	t := template.Must(template.New("").Parse(
		`Original PR: {{.PRTitle}}
Diff summary: {{.DiffSummary}}
Housekeeping files changed: {{range .HousekeepingFiles}}- {{.}}
{{end}}

Write a 2-3 sentence PR body.`))

	var buf bytes.Buffer
	if err := t.Execute(&buf, opts); err != nil {
		return fmt.Sprintf("Housekeeping updates associated with the original PR."), nil
	}

	response, err := c.callOllama(ctx, roleGSystem, buf.String())
	if err != nil || strings.TrimSpace(response) == "" {
		return "Housekeeping updates: documentation and changelog maintenance.", nil
	}
	return strings.TrimSpace(response), nil
}

// ---- internal ---------------------------------------------------------------

// callOllama sends a chat completion request to Ollama and returns the response text.
// Acquires the global semaphore — only 1 concurrent Ollama call is allowed.
func (c *Client) callOllama(ctx context.Context, system, user string) (string, error) {
	acquireOllama()
	defer releaseOllama()

	model := c.cfg.Ollama.Model
	temp := float32(c.cfg.Ollama.Temperature)

	messages := []ollamaapi.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	var result strings.Builder
	err := c.ollama.Chat(ctx, &ollamaapi.ChatRequest{
		Model:    model,
		Messages: messages,
		Options: map[string]interface{}{
			"temperature": temp,
			"num_ctx":     c.cfg.Ollama.ContextWindow,
		},
	}, func(resp ollamaapi.ChatResponse) error {
		result.WriteString(resp.Message.Content)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("ollama chat: %w", err)
	}
	return result.String(), nil
}

// buildAnalyzePrompt constructs the user prompt for Role A.
func buildAnalyzePrompt(opts types.AnalyzeOpts) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Repo: %s\nFiles in this batch:", opts.Repo))
	for _, f := range opts.FileBatch {
		sb.WriteString(fmt.Sprintf("\n  %s (+%d/-%d)", f.Filename, f.LinesAdded, f.LinesRemoved))
	}
	sb.WriteString("\n\nDiffs:\n")
	for _, f := range opts.FileBatch {
		sb.WriteString(fmt.Sprintf("\n--- %s ---\n%s\n", f.Filename, f.Diff))
	}
	sb.WriteString("\nReturn a JSON array of tasks found in these files.")
	return sb.String()
}

// unmarshalJSON attempts to parse JSON from an LLM response string.
// It handles responses that may have leading/trailing prose around the JSON.
func unmarshalJSON(response string, out interface{}) error {
	response = strings.TrimSpace(response)

	// Try direct parse first.
	if err := json.Unmarshal([]byte(response), out); err == nil {
		return nil
	}

	// Try to extract JSON array or object from response.
	start := strings.IndexAny(response, "[{")
	end := strings.LastIndexAny(response, "}]")
	if start >= 0 && end > start {
		jsonPart := response[start : end+1]
		if err := json.Unmarshal([]byte(jsonPart), out); err == nil {
			return nil
		}
	}

	return fmt.Errorf("could not parse JSON from LLM response")
}

// validateTaskSpecs filters out invalid TaskSpec items.
func validateTaskSpecs(specs []types.TaskSpec) []types.TaskSpec {
	validTypes := map[string]bool{
		"bug": true, "vulnerability": true, "docs": true, "dependency-update": true,
		"feature": true, "refactor": true, "issue": true, "changelog": true, "readme": true,
	}
	validComplexity := map[string]bool{
		"trivial": true, "small": true, "medium": true, "large": true,
	}
	validPriority := map[string]bool{"1": true, "2": true, "3": true, "4": true, "5": true}

	var out []types.TaskSpec
	for _, s := range specs {
		if !validTypes[s.Type] || !validComplexity[s.Complexity] || !validPriority[s.Priority] {
			slog.Warn("LLM returned invalid TaskSpec (skipped)", "type", s.Type, "complexity", s.Complexity, "priority", s.Priority)
			continue
		}
		out = append(out, s)
	}
	return out
}

