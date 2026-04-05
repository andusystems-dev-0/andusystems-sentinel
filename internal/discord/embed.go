// Package discord manages the Discord bot lifecycle, all messaging, reaction dispatch,
// thread management, and slash commands.
// Only this package owns the bot session and reaction dispatch.
// Must-NOT call Forgejo API directly or make PR merge decisions.
package discord

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/andusystems/sentinel/internal/config"
	"github.com/andusystems/sentinel/internal/types"
)

const (
	colorHighPriority = 0xE74C3C // red
	colorLowPriority  = 0x95A5A6 // grey
	colorFinding      = 0xF39C12 // amber
	colorInfo         = 0x3498DB // blue
)

// BuildPREmbed constructs the Discord embed for a sentinel PR notification.
func BuildPREmbed(pr types.SentinelPR, summary string, cfg config.PRConfig) *discordgo.MessageEmbed {
	var color int
	var prefix string

	if pr.PriorityTier == types.PRTierHigh {
		color = colorHighPriority
		if pr.PRType == "vulnerability" {
			prefix = "🚨"
		} else {
			prefix = "🔀"
		}
	} else {
		color = colorLowPriority
		prefix = "📝"
	}

	title := fmt.Sprintf("%s  %s — %s", prefix, pr.Title, pr.Repo)
	branchField := fmt.Sprintf("`%s` → `%s`", pr.Branch, pr.BaseBranch)

	return &discordgo.MessageEmbed{
		Title: title,
		Color: color,
		Description: fmt.Sprintf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n%s", summary),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Branch", Value: branchField, Inline: false},
			{Name: "Type", Value: pr.PRType, Inline: true},
			{Name: "Forgejo", Value: pr.PRUrl, Inline: false},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "React:  ✅ Merge   ❌ Close   💬 Discuss",
		},
		Timestamp: pr.OpenedAt.Format(time.RFC3339),
	}
}

// BuildFindingEmbed constructs a Discord embed for a sanitization finding.
func BuildFindingEmbed(r types.PendingResolution, f types.SanitizationFinding) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title: fmt.Sprintf("🔍 Finding: %s in %s", f.Category, shortPath(f.Filename)),
		Color: colorFinding,
		Description: fmt.Sprintf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n**Repo:** %s\n**File:** `%s`\n**Line:** %d\n**Layer:** %d\n**Confidence:** %s",
			f.Repo, f.Filename, f.LineNumber, f.Layer, f.Confidence),
		Fields: []*discordgo.MessageEmbedField{
			{Name: "Suggested replacement", Value: fmt.Sprintf("`%s`", r.SuggestedReplacement), Inline: false},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "React:  ✅ Approve   ❌ Reject   🔍 Re-analyze   ✏️ Custom",
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}
}

// BuildDigestEmbed constructs the nightly pending digest embed.
func BuildDigestEmbed(digest types.NightlyDigest, collapseLowThreshold int) *discordgo.MessageEmbed {
	var fields []*discordgo.MessageEmbedField

	// High-priority open PRs.
	if len(digest.HighPriorityPRs) > 0 {
		var lines string
		for _, pr := range digest.HighPriorityPRs {
			lines += fmt.Sprintf("• **#%d** %s — %s\n", pr.PRNumber, pr.Title, pr.Repo)
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  fmt.Sprintf("🚨 High-Priority Open PRs (%d)", len(digest.HighPriorityPRs)),
			Value: lines,
		})
	}

	// Low-priority open PRs (collapse if too many).
	if len(digest.LowPriorityPRs) > 0 {
		var val string
		if collapseLowThreshold > 0 && len(digest.LowPriorityPRs) > collapseLowThreshold {
			val = fmt.Sprintf("%d low-priority PRs pending approval", len(digest.LowPriorityPRs))
		} else {
			for _, pr := range digest.LowPriorityPRs {
				val += fmt.Sprintf("• **#%d** %s — %s\n", pr.PRNumber, pr.Title, pr.Repo)
			}
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  fmt.Sprintf("📝 Low-Priority Open PRs (%d)", len(digest.LowPriorityPRs)),
			Value: val,
		})
	}

	// Pending findings.
	if len(digest.PendingFindings) > 0 {
		var lines string
		for _, pf := range digest.PendingFindings {
			lines += fmt.Sprintf("• **%s**: %d finding(s) in %d file(s)\n",
				pf.Repo, pf.Count, len(pf.AffectedFiles))
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:  "🔍 Pending Sanitization Findings",
			Value: lines,
		})
	}

	if len(fields) == 0 {
		fields = []*discordgo.MessageEmbedField{
			{Name: "Status", Value: "✅ All caught up — no pending items."},
		}
	}

	return &discordgo.MessageEmbed{
		Title:     "📋 Sentinel Nightly Digest",
		Color:     colorInfo,
		Fields:    fields,
		Timestamp: time.Now().Format(time.RFC3339),
	}
}

func shortPath(filename string) string {
	// Return last two path components for display.
	parts := splitPath(filename)
	if len(parts) <= 2 {
		return filename
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

func splitPath(filename string) []string {
	var parts []string
	current := ""
	for _, c := range filename {
		if c == '/' || c == '\\' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
