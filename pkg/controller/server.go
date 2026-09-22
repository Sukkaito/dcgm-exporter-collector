package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// Server is the HTTPS control-node service server.
type Server struct {
	addr       string
	handler    *Handler
	httpServer *http.Server
	certFile   string
	keyFile    string
	useTLS     bool
}

// NewServer creates a new control-node server with optional mTLS.
func NewServer(addr string, handler *Handler, certFile, keyFile, clientCAFile string) (*Server, error) {
	if addr == "" {
		addr = ":8443"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/host/sync", handler.HandleSync)
	mux.HandleFunc("/healthz", handler.HandleHealthz)
	mux.HandleFunc("/readyz", handler.HandleReadyz)

	s := &Server{
		addr:     addr,
		handler:  handler,
		certFile: certFile,
		keyFile:  keyFile,
		useTLS:   certFile != "" && keyFile != "",
	}

	var tlsConfig *tls.Config

	if s.useTLS {
		tlsConfig = &tls.Config{
			MinVersion: tls.VersionTLS13,
		}

		if clientCAFile != "" {
			caCert, err := os.ReadFile(clientCAFile)
			if err != nil {
				return nil, fmt.Errorf("reading client CA certificate %s: %w", clientCAFile, err)
			}
			caPool := x509.NewCertPool()
			if !caPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse client CA certificate from %s", clientCAFile)
			}
			tlsConfig.ClientCAs = caPool
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
		}
	}

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		TLSConfig:    tlsConfig,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	return s, nil
}

// Handler returns the configured http.Handler for testing.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Start runs the server (using TLS if certs provided).
func (s *Server) Start() error {
	slog.Info("Starting control service", "addr", s.addr, "tls", s.useTLS)
	if s.useTLS {
		if err := s.httpServer.ListenAndServeTLS(s.certFile, s.keyFile); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("https server failed: %w", err)
		}
		return nil
	}

	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server failed: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("Shutting down control service")
	return s.httpServer.Shutdown(ctx)
}
