package discord

import (
	"context"
	"log/slog"

	"github.com/andusystems/sentinel/internal/types"
)

// PostDigest fetches pending PRs and findings and posts the nightly digest.
func (b *Bot) PostDigest(ctx context.Context, prStore types.SentinelPRStore) error {
	openPRs, err := prStore.GetOpenPRs(ctx)
	if err != nil {
		return err
	}

	var high, low []types.SentinelPR
	for _, pr := range openPRs {
		if pr.PriorityTier == types.PRTierHigh {
			high = append(high, pr)
		} else {
			low = append(low, pr)
		}
	}

	digest := types.NightlyDigest{
		HighPriorityPRs: high,
		LowPriorityPRs:  low,
	}

	if err := b.PostNightlyDigest(ctx, digest); err != nil {
		slog.Error("failed to post nightly digest", "err", err)
		return err
	}
	return nil
}
