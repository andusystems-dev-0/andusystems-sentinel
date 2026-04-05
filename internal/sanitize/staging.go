package sanitize

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/types"
)

// BuildStagingContent performs a single left-to-right pass over source content,
// replacing all findings with sentinel tags.
//
//   - High-confidence findings: replaced with tag, AutoRedacted=true, TokenIndex=0
//   - Medium/low findings: replaced with tag, TokenIndex assigned 1, 2, 3... (sequential)
//
// Returns the staged content and the updated findings slice (with TokenIndex set).
// Caller must ensure findings are valid (byte offsets in bounds, non-overlapping).
func BuildStagingContent(content []byte, findings []types.SanitizationFinding, cfg *config.SanitizeConfig) ([]byte, []types.SanitizationFinding) {
	if len(findings) == 0 {
		return append([]byte(nil), content...), findings
	}

	// Sort by ByteOffsetStart ascending (required for single-pass).
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].ByteOffsetStart < findings[j].ByteOffsetStart
	})

	output := make([]byte, 0, len(content))
	cursor := 0
	tokenCounter := 0

	for i := range findings {
		f := &findings[i]

		// Append bytes before this finding.
		if f.ByteOffsetStart > cursor {
			output = append(output, content[cursor:f.ByteOffsetStart]...)
		}

		tag := SentinelTag(f.Category, cfg)

		if f.Confidence == "high" {
			f.AutoRedacted = true
			f.TokenIndex = 0
		} else {
			// Medium or low confidence: assign sequential token_index.
			tokenCounter++
			f.TokenIndex = tokenCounter
			f.AutoRedacted = false
		}

		output = append(output, []byte(tag)...)
		cursor = f.ByteOffsetEnd
	}

	// Append remaining content after last finding.
	if cursor < len(content) {
		output = append(output, content[cursor:]...)
	}

	return output, findings
}

// ApplyScrubPatterns applies regex substitutions to content before it is
// pushed to the GitHub mirror. Patterns are compiled once per call; a
// compilation error returns the original content unchanged plus the error.
func ApplyScrubPatterns(content []byte, patterns []config.ScrubPattern) ([]byte, error) {
	result := content
	for _, p := range patterns {
		re, err := regexp.Compile(p.Pattern)
		if err != nil {
			return content, fmt.Errorf("invalid scrub pattern %q: %w", p.Pattern, err)
		}
		result = re.ReplaceAll(result, []byte(p.Replacement))
	}
	return result, nil
}
