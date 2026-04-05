// Package executor invokes the [AI_ASSISTANT] Code CLI via os/exec.
// Only this package invokes the [AI_ASSISTANT] Code CLI binary.
// Must-NOT call Ollama or Forgejo API directly.
package executor

// claudeCodeSemaphore limits concurrent [AI_ASSISTANT] Code CLI invocations to 1.
// [AI_ASSISTANT] Code spawns [AI_ASSISTANT] API calls and runs git operations; concurrency
// would cause git conflicts and exhaust API quota.
var claudeCodeSemaphore = make(chan struct{}, 1)

func init() {
	claudeCodeSemaphore <- struct{}{}
}

func acquireClaudeCode() { <-claudeCodeSemaphore }
func releaseClaudeCode() { claudeCodeSemaphore <- struct{}{} }
