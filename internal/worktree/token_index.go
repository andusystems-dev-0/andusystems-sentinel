package worktree

import (
	"bytes"
	"fmt"
)

const sentinelPrefix = "<REMOVED BY SENTINEL BOT:"

// resolveTokenIndex replaces the sentinel tag at position tokenIndex in content
// with finalValue, adjusting for already-resolved predecessor tags.
//
// Parameters:
//   - content: current file bytes (from GitHub staging)
//   - tokenIndex: 1-based index of the target tag (as assigned during staging construction)
//   - resolvedCount: number of predecessor tags (tokenIndex < this tokenIndex) already resolved
//   - finalValue: the operator-approved replacement string
//
// The caller MUST hold FileMutexRegistry.Lock(repo, filename) before calling.
// Returns the modified content, or an error if the tag cannot be located.
func resolveTokenIndex(content []byte, tokenIndex, resolvedCount int, finalValue string) ([]byte, error) {
	// Step 1: collect all start positions of sentinel tags.
	positions := findSentinelTagPositions(content)

	total := len(positions)
	if total < tokenIndex {
		return nil, fmt.Errorf("token_index error: expected at least %d tags, found %d", tokenIndex, total)
	}

	// Step 5: adjust for resolved predecessors.
	// Each resolved tag has already been replaced with its finalValue (no longer a placeholder),
	// so the index within the current set of tags shifts down by resolvedCount.
	targetPos := tokenIndex - 1 - resolvedCount
	if targetPos < 0 || targetPos >= total {
		return nil, fmt.Errorf("token_index error: targetPos %d out of range [0, %d)", targetPos, total)
	}

	tagStart := positions[targetPos]

	// Step 8: find the closing '>' of this tag.
	tagEnd := bytes.IndexByte(content[tagStart:], '>')
	if tagEnd < 0 {
		return nil, fmt.Errorf("token_index error: no closing '>' found starting at byte %d", tagStart)
	}
	tagEnd = tagStart + tagEnd + 1 // absolute position, inclusive of '>'

	// Step 9: replace [tagStart, tagEnd) with finalValue.
	out := make([]byte, 0, len(content)-( tagEnd-tagStart)+len(finalValue))
	out = append(out, content[:tagStart]...)
	out = append(out, []byte(finalValue)...)
	out = append(out, content[tagEnd:]...)

	return out, nil
}

// findSentinelTagPositions returns the start byte offsets of every
// "<REMOVED BY SENTINEL BOT:" occurrence in content, in ascending order.
func findSentinelTagPositions(content []byte) []int {
	needle := []byte(sentinelPrefix)
	var positions []int
	start := 0
	for {
		idx := bytes.Index(content[start:], needle)
		if idx < 0 {
			break
		}
		abs := start + idx
		positions = append(positions, abs)
		start = abs + len(needle)
	}
	return positions
}
