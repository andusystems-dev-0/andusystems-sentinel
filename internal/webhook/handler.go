package webhook

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/andusystems/sentinel/internal/types"
)

const maxBodyBytes = 10 * 1024 * 1024 // 10 MB

// Handler is the HTTP handler for incoming Forgejo webhook requests.
type Handler struct {
	queue  *Queue
	secret string
}

// NewHandler creates a webhook Handler.
func NewHandler(queue *Queue, secret string) *Handler {
	return &Handler{queue: queue, secret: secret}
}

// ServeHTTP handles POST /webhooks/forgejo.
//
// Flow:
//  1. Read body (max 10 MB)
//  2. Validate HMAC-SHA256 synchronously — HTTP 403 on fail
//  3. Parse event type and repo name
//  4. Enqueue event — HTTP 429 if full
//  5. Return HTTP 200 immediately (ACK boundary)
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		slog.Error("webhook: read body", "err", err)
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// HMAC validation — synchronous, before any parsing.
	signature := r.Header.Get("X-Gitea-Signature")
	if !ValidateHMAC(body, signature, h.secret) {
		slog.Warn("webhook: HMAC validation failed", "remote", r.RemoteAddr)
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	eventType := r.Header.Get("X-Gitea-Event")
	if eventType == "" {
		http.Error(w, "missing X-Gitea-Event", http.StatusBadRequest)
		return
	}

	// Extract repo name from payload.
	repo := extractRepo(body)

	event := types.ForgejoEvent{
		Type:       eventType,
		Repo:       repo,
		Payload:    body,
		ReceivedAt: time.Now(),
	}

	if err := h.queue.Enqueue(event); err != nil {
		slog.Warn("webhook: queue full", "event", eventType, "repo", repo)
		http.Error(w, "queue full — retry later", http.StatusTooManyRequests)
		return
	}

	slog.Info("webhook: enqueued", "event", eventType, "repo", repo)
	w.WriteHeader(http.StatusOK)
}

// HealthHandler responds to /health and /ready probes.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// extractRepo reads repository.name from a JSON webhook payload.
func extractRepo(payload []byte) string {
	var partial struct {
		Repository struct {
			Name string `json:"name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(payload, &partial); err != nil {
		return "unknown"
	}
	return partial.Repository.Name
}
