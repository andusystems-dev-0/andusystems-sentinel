package webhook

import (
	"context"
	"errors"

	"github.com/andusystems/sentinel/internal/types"
)

// ErrQueueFull is returned by Enqueue when the event queue is at capacity.
var ErrQueueFull = errors.New("webhook queue full")

// Queue implements types.WebhookQueue using a buffered channel.
// The channel size matches config.webhook.event_queue_size.
type Queue struct {
	ch chan types.ForgejoEvent
}

// NewQueue creates a Queue with the given buffer size.
func NewQueue(size int) *Queue {
	return &Queue{ch: make(chan types.ForgejoEvent, size)}
}

// Enqueue adds an event to the queue without blocking.
// Returns ErrQueueFull if the queue is at capacity (caller returns HTTP 429).
func (q *Queue) Enqueue(event types.ForgejoEvent) error {
	select {
	case q.ch <- event:
		return nil
	default:
		return ErrQueueFull
	}
}

// Dequeue returns the read channel for workers to consume from.
func (q *Queue) Dequeue(_ context.Context) (<-chan types.ForgejoEvent, error) {
	return q.ch, nil
}

// Close closes the underlying channel (called on graceful shutdown after
// all HTTP connections are closed and no new enqueues will occur).
func (q *Queue) Close() {
	close(q.ch)
}
