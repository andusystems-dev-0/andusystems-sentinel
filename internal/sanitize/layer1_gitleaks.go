package sanitize

import (
	"bytes"
	"context"
	"log/slog"

	"github.com/zricethezav/gitleaks/v8/detect"

	appconfig "github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/types"
)

// layer1Gitleaks runs gitleaks as a library to detect secrets in file content.
// Returns findings sorted by byte_offset_start.
type layer1Gitleaks struct {
	detector *detect.Detector
}

func newLayer1Gitleaks() (*layer1Gitleaks, error) {
	// NewDetectorDefaultConfig uses the built-in gitleaks ruleset.
	detector, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return nil, err
	}
	return &layer1Gitleaks{detector: detector}, nil
}

// scan runs gitleaks on the given file content and returns sanitization findings.
// filename is used only for logging and line number calculation.
func (l *layer1Gitleaks) scan(
	_ context.Context,
	repo, filename string,
	content []byte,
	zones []types.SkipZone,
	syncRunID string,
	cfg *appconfig.SanitizeConfig,
) ([]types.SanitizationFinding, error) {
	// gitleaks DetectReader accepts an io.Reader and a buffer size in KB.
	reader := bytes.NewReader(content)

	findings, err := l.detector.DetectReader(reader, 32)
	if err != nil {
		slog.Warn("gitleaks scan error", "repo", repo, "file", filename, "err", err)
		return nil, nil // non-fatal: proceed to Layer 2
	}

	var results []types.SanitizationFinding
	for _, f := range findings {
		if f.Secret == "" {
			continue
		}

		start := f.StartColumn - 1
		end := start + len(f.Secret)

		// Bounds check.
		if start < 0 || end > len(content) {
			slog.Warn("gitleaks finding out of bounds", "file", filename, "start", start, "end", end)
			continue
		}

		// Skip if in an approved-values zone.
		if isInSkipZone(start, end, zones) {
			continue
		}

		results = append(results, types.SanitizationFinding{
			Layer:                1,
			Repo:                 repo,
			Filename:             filename,
			LineNumber:           f.StartLine,
			ByteOffsetStart:      start,
			ByteOffsetEnd:        end,
			OriginalValue:        f.Secret, // Never logged; held in memory only
			SuggestedReplacement: cfg.CategoryReasons["SECRET"],
			Category:             mapGitleaksRule(f.RuleID),
			Confidence:           "high", // gitleaks findings are high confidence
			SyncRunID:            syncRunID,
		})
	}
	return results, nil
}

// mapGitleaksRule maps a gitleaks RuleID to a sentinel category.
func mapGitleaksRule(ruleID string) string {
	switch {
	case contains(ruleID, "api", "token", "key"):
		return "API_KEY"
	case contains(ruleID, "password", "passwd", "pwd"):
		return "PASSWORD"
	case contains(ruleID, "private-key", "private_key", "rsa", "pem"):
		return "PRIVATE_KEY"
	case contains(ruleID, "connection", "dsn", "database"):
		return "CONNECTION_STRING"
	default:
		return "SECRET"
	}
}

func contains(s string, subs ...string) bool {
	lower := s
	for _, sub := range subs {
		for i := 0; i <= len(lower)-len(sub); i++ {
			if lower[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
