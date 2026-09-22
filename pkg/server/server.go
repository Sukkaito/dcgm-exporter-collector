package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/coordinator"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/processor"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/scraper"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"
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
	slog.Info("Starting telemetry server", "addr", s.addr)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server failed: %w", err)
	}
	return nil
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	slog.Info("Shutting down telemetry server")
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
			slog.Error("Error enriching metrics", "vm_id", res.Target.VMID, "error", err)
			continue
		}
		processor.MergeFamilies(combinedFamilies, mfs)
	}

	// Inject collector-level operational metrics (scrape success, latency, target totals)
	collectorFamilies := s.buildCollectorMetrics(results)
	processor.MergeFamilies(combinedFamilies, collectorFamilies)

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

func (s *Server) buildCollectorMetrics(results map[string]scraper.ScrapeResult) map[string]*dto.MetricFamily {
	host := s.enricher.HostName()
	families := make(map[string]*dto.MetricFamily)

	successMF := &dto.MetricFamily{
		Name: proto.String("dcgm_collector_scrape_success"),
		Help: proto.String("Whether the guest dcgm-exporter scrape was successful (1 for success, 0 for failure)."),
		Type: dto.MetricType_GAUGE.Enum(),
	}

	durationMF := &dto.MetricFamily{
		Name: proto.String("dcgm_collector_scrape_duration_seconds"),
		Help: proto.String("Scrape duration for the guest dcgm-exporter in seconds."),
		Type: dto.MetricType_GAUGE.Enum(),
	}

	healthyCount := 0
	for _, res := range results {
		val := 0.0
		if res.Error == nil {
			val = 1.0
			healthyCount++
		}

		labels := []*dto.LabelPair{
			{Name: proto.String("host"), Value: proto.String(host)},
			{Name: proto.String("vm_id"), Value: proto.String(res.Target.VMID)},
			{Name: proto.String("vm_name"), Value: proto.String(res.Target.VMName)},
		}
		if res.Target.ProjectID != "" {
			labels = append(labels, &dto.LabelPair{Name: proto.String("project_id"), Value: proto.String(res.Target.ProjectID)})
		}

		successMF.Metric = append(successMF.Metric, &dto.Metric{
			Label: labels,
			Gauge: &dto.Gauge{Value: proto.Float64(val)},
		})

		durationMF.Metric = append(durationMF.Metric, &dto.Metric{
			Label: labels,
			Gauge: &dto.Gauge{Value: proto.Float64(res.Latency.Seconds())},
		})
	}

	totalMF := &dto.MetricFamily{
		Name: proto.String("dcgm_collector_targets_total"),
		Help: proto.String("Total number of GPU passthrough VM targets discovered on this host."),
		Type: dto.MetricType_GAUGE.Enum(),
		Metric: []*dto.Metric{
			{
				Label: []*dto.LabelPair{{Name: proto.String("host"), Value: proto.String(host)}},
				Gauge: &dto.Gauge{Value: proto.Float64(float64(len(results)))},
			},
		},
	}

	healthyMF := &dto.MetricFamily{
		Name: proto.String("dcgm_collector_targets_healthy"),
		Help: proto.String("Number of healthy GPU passthrough VM targets on this host."),
		Type: dto.MetricType_GAUGE.Enum(),
		Metric: []*dto.Metric{
			{
				Label: []*dto.LabelPair{{Name: proto.String("host"), Value: proto.String(host)}},
				Gauge: &dto.Gauge{Value: proto.Float64(float64(healthyCount))},
			},
		},
	}

	families["dcgm_collector_scrape_success"] = successMF
	families["dcgm_collector_scrape_duration_seconds"] = durationMF
	families["dcgm_collector_targets_total"] = totalMF
	families["dcgm_collector_targets_healthy"] = healthyMF

	return families
}
