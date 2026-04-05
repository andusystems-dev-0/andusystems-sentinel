package worktree

import "sync"

// fileMutexRegistry implements types.FileMutexRegistry.
// It provides a per-(repo, filename) sync.Mutex to serialise concurrent
// token_index resolutions on the same file.
type fileMutexRegistry struct {
	mu    sync.Mutex
	locks map[fileKey]*sync.Mutex
}

type fileKey struct{ repo, filename string }

// NewFileMutexRegistry returns a ready-to-use FileMutexRegistry.
func NewFileMutexRegistry() *fileMutexRegistry {
	return &fileMutexRegistry{locks: make(map[fileKey]*sync.Mutex)}
}

func (r *fileMutexRegistry) get(repo, filename string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := fileKey{repo, filename}
	if _, ok := r.locks[k]; !ok {
		r.locks[k] = &sync.Mutex{}
	}
	return r.locks[k]
}

func (r *fileMutexRegistry) Lock(repo, filename string)   { r.get(repo, filename).Lock() }
func (r *fileMutexRegistry) Unlock(repo, filename string) { r.get(repo, filename).Unlock() }
