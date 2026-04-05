package sanitize

import (
	"regexp"
	"sort"

	"github.com/andusystems/sentinel/internal/types"
)

// templatePatterns matches common template/placeholder syntaxes that should
// never be flagged as sensitive values regardless of their content.
var templatePatterns = regexp.MustCompile(
	`\{\{[\s\S]*?\}\}` + // Jinja2/Ansible/Helm: {{ ... }}
		`|` + `\{%[\s\S]*?%\}` + // Jinja2 blocks: {% ... %}
		`|` + `\{#[\s\S]*?#\}` + // Jinja2 comments: {# ... #}
		`|` + `<%[\s\S]*?%>` + // ERB/EJS: <% ... %>
		`|` + `\$\{[^}]*\}`, // Shell/HCL interpolation: ${...}
)

// templateSkipZones scans content and returns skip zones covering all template
// expressions. This prevents any layer from flagging template variables as
// sensitive values, regardless of what words they contain (e.g. "email", "key").
func templateSkipZones(content []byte) []types.SkipZone {
	matches := templatePatterns.FindAllIndex(content, -1)
	zones := make([]types.SkipZone, 0, len(matches))
	for _, m := range matches {
		zones = append(zones, types.SkipZone{Start: m[0], End: m[1]})
	}
	return zones
}

// isInSkipZone returns true if the byte range [start, end) overlaps any skip zone.
func isInSkipZone(start, end int, zones []types.SkipZone) bool {
	for _, z := range zones {
		// Overlap: start < z.End && end > z.Start
		if start < z.End && end > z.Start {
			return true
		}
	}
	return false
}

// mergeSkipZones merges overlapping or adjacent skip zones and returns them sorted.
func mergeSkipZones(zones []types.SkipZone) []types.SkipZone {
	if len(zones) == 0 {
		return nil
	}

	// Sort by start.
	sort.Slice(zones, func(i, j int) bool { return zones[i].Start < zones[j].Start })

	merged := []types.SkipZone{zones[0]}
	for _, z := range zones[1:] {
		last := &merged[len(merged)-1]
		if z.Start <= last.End {
			if z.End > last.End {
				last.End = z.End
			}
		} else {
			merged = append(merged, z)
		}
	}
	return merged
}

// filterFindings removes any findings whose byte range overlaps a skip zone.
func filterFindings(findings []types.SanitizationFinding, zones []types.SkipZone) []types.SanitizationFinding {
	if len(zones) == 0 {
		return findings
	}
	zones = mergeSkipZones(zones)

	var out []types.SanitizationFinding
	for _, f := range findings {
		if !isInSkipZone(f.ByteOffsetStart, f.ByteOffsetEnd, zones) {
			out = append(out, f)
		}
	}
	return out
}
