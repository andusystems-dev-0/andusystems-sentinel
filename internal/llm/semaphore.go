// Package llm wraps the Ollama API for all LLM roles.
// Only this package calls Ollama. Must-NOT call Forgejo API, write files, or call [AI_ASSISTANT] API.
package llm

// ollamaSemaphore is a package-level channel semaphore limiting concurrent Ollama calls to 1.
// Ollama on a single GPU cannot handle concurrent inference; serialise via this semaphore.
var ollamaSemaphore = make(chan struct{}, 1)

func init() {
	// Pre-fill with one token so the first caller can proceed immediately.
	ollamaSemaphore <- struct{}{}
}

func acquireOllama() { <-ollamaSemaphore }
func releaseOllama() { ollamaSemaphore <- struct{}{} }
