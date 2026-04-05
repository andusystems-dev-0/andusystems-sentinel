// Package webhook owns the HTTP server, async event queue, HMAC validation, and worker pool.
// Must-NOT own Discord state or call Forgejo API from handlers (enqueue only).
package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ValidateHMAC validates the Gitea/Forgejo HMAC-SHA256 webhook signature.
// The X-Gitea-Signature header format is "sha256=<hex>".
//
// Uses hmac.Equal for constant-time comparison to prevent timing attacks.
// Returns false if secret is empty (always reject unsigned requests).
func ValidateHMAC(payload []byte, signature, secret string) bool {
	if secret == "" {
		return false
	}

	// Strip "sha256=" prefix if present.
	sig := strings.TrimPrefix(signature, "sha256=")
	if sig == "" {
		return false
	}

	sigBytes, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expected := mac.Sum(nil)

	return hmac.Equal(expected, sigBytes)
}
