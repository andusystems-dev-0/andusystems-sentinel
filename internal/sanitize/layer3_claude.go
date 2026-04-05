package sanitize

import (
	"context"
	"log/slog"

	"github.com/andusystems/sentinel/internal/types"
)

// layer3Claude calls ClaudeAPIClient.SanitizeChunk for the final semantic safety net.
// Only this layer (and only this package) calls the [AI_ASSISTANT] API.
// Layer 3 is called only when the API key is configured.
type layer3Claude struct {
	[AI_ASSISTANT] types.ClaudeAPIClient
}

func newLayer3Claude([AI_ASSISTANT] types.ClaudeAPIClient) *layer3Claude {
	return &layer3Claude{[AI_ASSISTANT]: [AI_ASSISTANT]}
}

// scan calls the [AI_ASSISTANT] API with post-Layer-2 content and returns any remaining findings.
// STUB: [AI_ASSISTANT] API call is implemented; response parsing is the same as Layer 2.
func (l *layer3Claude) scan(
	ctx context.Context,
	repo, filename string,
	content []byte,
	zones []types.SkipZone,
	syncRunID string,
) ([]types.SanitizationFinding, error) {
	if l.[AI_ASSISTANT] == nil {
		return nil, nil
	}

	raw, err := l.[AI_ASSISTANT].SanitizeChunk(ctx, string(content))
	if err != nil {
		slog.Warn("layer3 [AI_ASSISTANT] API sanitize error", "repo", repo, "file", filename, "err", err)
		return nil, nil // non-fatal: Layer 3 is best-effort final pass
	}

	var valid []types.SanitizationFinding
	for _, f := range raw {
		if f.ByteOffsetStart < 0 || f.ByteOffsetEnd > len(content) {
			slog.Warn("layer3 finding out of bounds (discarded)", "file", filename,
				"start", f.ByteOffsetStart, "end", f.ByteOffsetEnd)
			continue
		}
		if isInSkipZone(f.ByteOffsetStart, f.ByteOffsetEnd, zones) {
			continue
		}
		f.Layer = 3
		f.Repo = repo
		f.Filename = filename
		f.SyncRunID = syncRunID
		valid = append(valid, f)
	}
	return valid, nil
}
