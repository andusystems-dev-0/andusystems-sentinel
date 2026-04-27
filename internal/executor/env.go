package executor

import (
	"os"
	"strings"
)

// SubprocessEnv returns os.Environ() with ANTHROPIC_API_KEY removed, so the
// Claude Code CLI subprocess is forced to use the operator's logged-in
// subscription session instead of falling back to API-key auth.
func SubprocessEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, e := range src {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			continue
		}
		out = append(out, e)
	}
	return out
}
