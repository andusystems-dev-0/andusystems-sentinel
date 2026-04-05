package store

import (
	"crypto/rand"
	"encoding/hex"
)

// NewID generates a random 16-byte hex ID (32 chars).
// Used for all DB primary keys created by sentinel.
func NewID() string {
	return newID()
}

func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("store: crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}
