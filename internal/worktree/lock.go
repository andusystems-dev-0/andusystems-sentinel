// Package worktree manages the two-worktree model:
//   - Forgejo worktree: local clone of each watched repo (read for analysis, write for [AI_ASSISTANT] Code)
//   - GitHub staging worktree: sanitized content written here before mirroring to GitHub
//
// Must-NOT call Forgejo API, Ollama, or Discord directly.
// Only package that reads/writes worktree files and pushes to GitHub.
package worktree

import "sync"

// forgejoWorktreeLock implements types.ForgejoWorktreeLock.
// It lazily creates a per-repo *sync.RWMutex behind a global sync.Mutex.
//
// Write lock: git pull, branch creation, [AI_ASSISTANT] Code invocation.
// Read lock:  diff reads for LLM analysis, Mode 2 review.
type forgejoWorktreeLock struct {
	mu    sync.Mutex
	locks map[string]*sync.RWMutex
}

// ForgejoWorktreeLock is the concrete type returned by NewForgejoWorktreeLock.
// Exported so main can store the concrete type and pass as types.ForgejoWorktreeLock.
type ForgejoWorktreeLock = forgejoWorktreeLock

// NewForgejoWorktreeLock returns a ready-to-use ForgejoWorktreeLock.
func NewForgejoWorktreeLock() *forgejoWorktreeLock {
	return &forgejoWorktreeLock{locks: make(map[string]*sync.RWMutex)}
}

func (f *forgejoWorktreeLock) get(repo string) *sync.RWMutex {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.locks[repo]; !ok {
		f.locks[repo] = &sync.RWMutex{}
	}
	return f.locks[repo]
}

func (f *forgejoWorktreeLock) Lock(repo string)    { f.get(repo).Lock() }
func (f *forgejoWorktreeLock) Unlock(repo string)  { f.get(repo).Unlock() }
func (f *forgejoWorktreeLock) RLock(repo string)   { f.get(repo).RLock() }
func (f *forgejoWorktreeLock) RUnlock(repo string) { f.get(repo).RUnlock() }
