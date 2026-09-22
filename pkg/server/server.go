package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/coordinator"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/processor"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/scraper"
	dto "github.com/prometheus/client_model/go"
)

// Server serves Prometheus telemetry and health endpoints.
type Server struct {
	addr        string
	coordinator *coordinator.Coordinator
	scraper     *scraper.Scraper
	enricher    *processor.MetricEnricher
	cacheTTL    time.Duration

	mu         sync.RWMutex
	cachedData []byte
	lastScrape time.Time
	httpServer *http.Server
}

// NewServer creates a new HTTP telemetry server.
func NewServer(addr string, coord *coordinator.Coordinator, sc *scraper.Scraper, enricher *processor.MetricEnricher, cacheTTL time.Duration) *Server {
	if addr == "" {
		addr = ":9405"
	}
	if cacheTTL <= 0 {
		cacheTTL = 5 * time.Second
	}

	s := &Server{
		addr:        addr,
		coordinator: coord,
		scraper:     sc,
		enricher:    enricher,
		cacheTTL:    cacheTTL,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	mux.HandleFunc("/status", s.handleStatus)

	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	return s
}

// Handler returns the configured http.Handler.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Start begins serving HTTP requests.
func (s *Server) Start() error {
	log.Printf("[server] Starting telemetry server on %s", s.addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server failed: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	log.Printf("[server] Shutting down telemetry server")
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK\n"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	lastSync, err := s.coordinator.GetSyncStatus()
	if err != nil && lastSync.IsZero() {
		http.Error(w, fmt.Sprintf("Not ready: sync error: %v", err), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("READY\n"))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := s.scraper.GetStatus()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(status)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	now := time.Now()
	if len(s.cachedData) > 0 && now.Sub(s.lastScrape) < s.cacheTTL {
		cached := s.cachedData
		s.mu.RUnlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		_, _ = w.Write(cached)
		return
	}
	s.mu.RUnlock()

	// Perform live scrape across active targets
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	targets := s.coordinator.GetTargets()
	results := s.scraper.ScrapeAll(ctx, targets)

	combinedFamilies := make(map[string]*dto.MetricFamily)
	for _, res := range results {
		if res.Error != nil || len(res.Payload) == 0 {
			continue
		}
		mfs, err := s.enricher.EnrichTargetMetrics(res.Payload, res.Target)
		if err != nil {
			log.Printf("[server] Error enriching metrics for vm %s: %v", res.Target.VMID, err)
			continue
		}
		processor.MergeFamilies(combinedFamilies, mfs)
	}

	encoded, err := processor.EncodeMetricFamilies(combinedFamilies)
	if err != nil {
		http.Error(w, fmt.Sprintf("encoding metrics failed: %v", err), http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	s.cachedData = encoded
	s.lastScrape = now
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	_, _ = w.Write(encoded)
}
