package llm

import (
	"context"
	"sort"
	"strings"

	"github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/types"
)

// Batcher partitions a set of file diffs into batches that fit within the
// Ollama context window, calls Analyze on each batch, then merges and deduplicates results.
type Batcher struct {
	client *Client
	cfg    *config.Config
}

// NewBatcher wraps an LLM client with batching logic.
func NewBatcher(client *Client, cfg *config.Config) *Batcher {
	return &Batcher{client: client, cfg: cfg}
}

// AnalyzeAll partitions diffs, calls Analyze per batch, merges and deduplicates results.
// Results are sorted by priority (1=highest) and capped at maxTasks.
func (b *Batcher) AnalyzeAll(ctx context.Context, repo string, diffs []types.FileDiff, focusAreas []string, maxTasks int) ([]types.TaskSpec, error) {
	batches := b.partition(diffs, focusAreas)

	var all []types.TaskSpec
	for _, batch := range batches {
		specs, err := b.client.Analyze(ctx, types.AnalyzeOpts{
			Repo:       repo,
			FileBatch:  batch,
			FocusAreas: focusAreas,
		})
		if err != nil {
			// Log and continue — per-batch failure should not abort other batches.
			continue
		}
		all = append(all, specs...)
	}

	// Deduplicate: for same (AffectedFiles[0], Type), keep the one with lower priority number.
	all = deduplicateSpecs(all)

	// Sort by priority ascending (1 = highest priority).
	sort.Slice(all, func(i, j int) bool {
		return all[i].Priority < all[j].Priority
	})

	// Cap at maxTasks.
	if maxTasks > 0 && len(all) > maxTasks {
		all = all[:maxTasks]
	}

	return all, nil
}

// partition greedily packs diffs into batches.
// Focus-area files go first within each batch.
// Token estimate: len(diff_bytes) / 4.
func (b *Batcher) partition(diffs []types.FileDiff, focusAreas []string) [][]types.FileDiff {
	budget := b.cfg.Ollama.ContextWindow - b.cfg.Ollama.ResponseBufferTokens

	// Estimate tokens for each diff.
	for i := range diffs {
		if diffs[i].EstTokens == 0 {
			diffs[i].EstTokens = len(diffs[i].Diff) / 4
		}
	}

	// Sort: focus-area matches first, then by (LinesAdded+LinesRemoved) descending.
	sort.SliceStable(diffs, func(i, j int) bool {
		isFocusI := isFocusFile(diffs[i], focusAreas)
		isFocusJ := isFocusFile(diffs[j], focusAreas)
		if isFocusI != isFocusJ {
			return isFocusI
		}
		return (diffs[i].LinesAdded + diffs[i].LinesRemoved) > (diffs[j].LinesAdded + diffs[j].LinesRemoved)
	})

	var batches [][]types.FileDiff
	var current []types.FileDiff
	currentTokens := 0

	for _, d := range diffs {
		tokens := d.EstTokens
		if tokens == 0 {
			tokens = 100 // minimum estimate
		}
		if currentTokens+tokens > budget && len(current) > 0 {
			batches = append(batches, current)
			current = nil
			currentTokens = 0
		}
		current = append(current, d)
		currentTokens += tokens
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

// isFocusFile returns true if the file matches any focus area keyword.
func isFocusFile(d types.FileDiff, focusAreas []string) bool {
	for _, area := range focusAreas {
		if strings.Contains(strings.ToLower(d.Filename), strings.ToLower(area)) ||
			strings.Contains(strings.ToLower(d.Diff), strings.ToLower(area)) {
			return true
		}
	}
	return false
}

// deduplicateSpecs: for same (AffectedFiles[0], Type), keep the spec with lower priority number.
func deduplicateSpecs(specs []types.TaskSpec) []types.TaskSpec {
	type key struct{ file, typ string }
	best := make(map[key]types.TaskSpec)

	for _, s := range specs {
		file := ""
		if len(s.AffectedFiles) > 0 {
			file = s.AffectedFiles[0]
		}
		k := key{file, s.Type}
		existing, ok := best[k]
		if !ok || s.Priority < existing.Priority {
			best[k] = s
		}
	}

	out := make([]types.TaskSpec, 0, len(best))
	for _, s := range best {
		out = append(out, s)
	}
	return out
}
