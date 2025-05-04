package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"rproxy/internal/certs"
	"time"
)

// Server manages the HTTP/S proxy server.
type Server struct {
	router      *Router
	certManager *certs.Manager
	httpServer  *http.Server
}

// NewServer creates a new proxy server instance.
func NewServer(router *Router, certMgr *certs.Manager) *Server {
	proxyHandler := NewProxyHandler(router)

	tlsConfig := &tls.Config{
		GetCertificate: certMgr.GetCertificateForSNI,
		MinVersion:     tls.VersionTLS12,
	}

	server := &http.Server{
		Addr:         ":443", // Revert to default dual-stack address
		Handler:      proxyHandler,
		TLSConfig:    tlsConfig,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{
		router:      router,
		certManager: certMgr,
		httpServer:  server,
	}
}

// Start runs the HTTPS server.
func (s *Server) Start(ctx context.Context) error {
	slog.Info("Starting HTTPS proxy server", "address", s.httpServer.Addr)

	// Channel to listen for errors from ListenAndServeTLS
	errChan := make(chan error, 1)

	go func() {
		// Use ListenAndServeTLS for default dual-stack behavior.
		// Certs are provided by http.Server.TLSConfig.GetCertificate
		if err := s.httpServer.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("HTTPS server error: %w", err)
		} else {
			errChan <- nil // Signal graceful shutdown
		}
	}()

	// Wait for context cancellation or server error
	select {
	case err := <-errChan:
		if err != nil {
			slog.Error("Server error", "error", err)
			// Listener is closed by ListenAndServeTLS on error or Shutdown
			return err
		}
		slog.Info("Server shutdown initiated gracefully (via server stop).")
	case <-ctx.Done():
		slog.Info("Server shutdown initiated (via context cancellation)...")
		// Attempt graceful shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Shutdown stops the server AND closes the listener(s)
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Warn("Server graceful shutdown failed", "error", err)
			return err
		}
		slog.Info("Server gracefully stopped.")
	}

	return nil
} 