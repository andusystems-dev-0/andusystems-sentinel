// Package sanitize implements the three-layer sanitization pipeline.
// Only this package calls the [AI_ASSISTANT] API (Layer 3).
// Must-NOT call Ollama directly (uses LLMClient interface), Forgejo API, or open PRs.
package sanitize

import (
	"fmt"
	"strings"

	"github.com/andusystems/sentinel/internal/config"
)

// SentinelTag constructs the placeholder tag string for a given category.
// Format: <REMOVED BY SENTINEL BOT: CATEGORY — reason>
//
// The '>' character is forbidden in reason strings (validated at config load).
func SentinelTag(category string, cfg *config.SanitizeConfig) string {
	reason, ok := cfg.CategoryReasons[category]
	if !ok {
		reason = "sensitive value detected"
	}
	return fmt.Sprintf("<REMOVED BY SENTINEL BOT: %s — %s>", category, reason)
}

// CollapseMultiLine collapses a multi-line value to a single-line representation
// for use inside a sentinel tag.
func CollapseMultiLine(value string) string {
	lines := strings.Split(value, "\n")
	if len(lines) <= 1 {
		return value
	}
	return fmt.Sprintf("%s (%d lines collapsed)", strings.TrimSpace(lines[0]), len(lines))
}
