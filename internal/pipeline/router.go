package pipeline

import (
	"fmt"
	"strings"
	"time"

	"github.com/andusystems/sentinel/internal/types"
)

// AssignExecutor determines which executor should handle a task based on type and complexity.
// Returns one of: "claude_code", "llm", "go"
func AssignExecutor(spec types.TaskSpec) string {
	switch spec.Type {
	case "vulnerability", "fix", "feat", "feature", "refactor":
		// Code changes → [AI_ASSISTANT] Code CLI.
		return "claude_code"
	case "comment":
		// Comment additions, removals, and TODO resolution → [AI_ASSISTANT] Code CLI.
		return "claude_code"
	case "doc-gen":
		// Full documentation generation → [AI_ASSISTANT] Code CLI.
		return "claude_code"
	case "docs", "readme", "changelog":
		// Incremental doc updates → [AI_ASSISTANT] Code CLI.
		return "claude_code"
	case "dependency-update":
		// Dependency bumps → Go (deterministic).
		return "go"
	case "issue":
		// Issue creation → LLM for prose.
		return "llm"
	default:
		if spec.Complexity == "large" {
			return "llm" // Too large for [AI_ASSISTANT] Code in one shot.
		}
		return "claude_code"
	}
}

// PriorityTier determines the Discord notification tier for a PR type.
func PriorityTier(prType string, highTypes []string) types.PRPriorityTier {
	for _, t := range highTypes {
		if prType == t {
			return types.PRTierHigh
		}
	}
	return types.PRTierLow
}

// BranchName generates a sentinel branch name for a task.
// Format: sentinel/<type>/<repo-slug>/<description-slug>-<unix-ts>
func BranchName(prType, repo, description string) string {
	ts := time.Now().Unix()
	slug := slugify(description, 40)
	return fmt.Sprintf("sentinel/%s/%s/%s-%d", prType, slugify(repo, 20), slug, ts)
}

// slugify converts a string to a URL-safe slug of at most maxLen characters.
func slugify(s string, maxLen int) string {
	s = strings.ToLower(s)
	var result []rune
	prevHyphen := false
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			result = append(result, c)
			prevHyphen = false
		} else if !prevHyphen && len(result) > 0 {
			result = append(result, '-')
			prevHyphen = true
		}
	}
	// Trim trailing hyphen.
	for len(result) > 0 && result[len(result)-1] == '-' {
		result = result[:len(result)-1]
	}
	if len(result) > maxLen {
		result = result[:maxLen]
	}
	return string(result)
}

// TaskTypeToPRType maps a task type to its PR branch/label type.
func TaskTypeToPRType(taskType string) string {
	switch taskType {
	case "vulnerability":
		return "fix"
	case "docs", "readme", "changelog":
		return "docs"
	case "dependency-update":
		return "chore"
	case "comment":
		return "chore"
	default:
		return taskType
	}
}

// PRTitleFor formats a PR title from a task spec per PLAN.md conventions.
func PRTitleFor(spec types.TaskSpec) string {
	switch spec.Type {
	case "vulnerability":
		return fmt.Sprintf("fix(security): %s", spec.Title)
	case "fix":
		return fmt.Sprintf("fix: %s", spec.Title)
	case "feat", "feature":
		return fmt.Sprintf("feat: %s", spec.Title)
	case "docs", "readme":
		return fmt.Sprintf("docs: %s", spec.Title)
	case "dependency-update":
		return fmt.Sprintf("chore(deps): %s", spec.Title)
	case "comment":
		return fmt.Sprintf("chore(comments): %s", spec.Title)
	case "sdlc-housekeeping":
		return fmt.Sprintf("chore(sdlc): %s", spec.Title)
	default:
		return fmt.Sprintf("chore: %s", spec.Title)
	}
}
