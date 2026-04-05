package webhook

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Server wraps an http.Server for the webhook endpoint.
type Server struct {
	httpServer *http.Server
}

// NewServer creates and configures the webhook HTTP server.
func NewServer(port int, queue *Queue, secret string) *Server {
	mux := http.NewServeMux()

	handler := NewHandler(queue, secret)
	mux.HandleFunc("/webhooks/forgejo", handler.ServeHTTP)
	mux.HandleFunc("/health", HealthHandler)
	mux.HandleFunc("/ready", HealthHandler)

	return &Server{
		httpServer: &http.Server{
			Addr:         fmt.Sprintf(":%d", port),
			Handler:      mux,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  120 * time.Second,
		},
	}
}

// Start begins listening for requests. Blocks until the server stops.
func (s *Server) Start() error {
	slog.Info("webhook server starting", "addr", s.httpServer.Addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("webhook server: %w", err)
	}
	return nil
}

// Stop gracefully shuts down the HTTP server within the given timeout.
func (s *Server) Stop(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	slog.Info("webhook server shutting down")
	return s.httpServer.Shutdown(ctx)
}
