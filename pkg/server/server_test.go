package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/coordinator"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/processor"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/scraper"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/transport"
)

func TestServer_Endpoints(t *testing.T) {
	// Mock guest dcgm-exporter
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization\n# TYPE DCGM_FI_DEV_GPU_UTIL gauge\nDCGM_FI_DEV_GPU_UTIL{gpu=\"0\"} 88\n"))
	}))
	defer guest.Close()

	parts := strings.Split(strings.TrimPrefix(guest.URL, "http://"), ":")
	port := 9400
	if len(parts) > 1 {
		var p int
		for _, c := range parts[1] {
			p = p*10 + int(c-'0')
		}
		port = p
	}

	targets := []api.TargetVM{
		{VMID: "test-vm-1", VMName: "ai-box", GuestIP: parts[0], Port: port},
	}

	coord, err := coordinator.NewCoordinator(coordinator.Config{
		StaticTargets: targets,
	}, nil)
	if err != nil {
		t.Fatalf("failed to create coordinator: %v", err)
	}
	_ = coord.SyncOnce(context.Background())

	tr := transport.NewHTTPTransport(time.Second, 1024*1024)
	sc := scraper.NewScraper(tr, time.Second)
	enricher := processor.NewMetricEnricher("hgx087")

	srv := NewServer(":0", coord, sc, enricher, 100*time.Millisecond)

	// Test /healthz
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.handleHealthz(w, req)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != "OK" {
		t.Errorf("healthz failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Test /readyz
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	srv.handleReadyz(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("readyz failed: code=%d", w.Code)
	}

	// Test /metrics
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.handleMetrics(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics failed: code=%d body=%s", w.Code, w.Body.String())
	}
	metricBody := w.Body.String()
	if !strings.Contains(metricBody, "DCGM_FI_DEV_GPU_UTIL") {
		t.Errorf("metrics body missing DCGM_FI_DEV_GPU_UTIL: %s", metricBody)
	}
	if !strings.Contains(metricBody, "host=\"hgx087\"") {
		t.Errorf("metrics body missing host label: %s", metricBody)
	}
	if !strings.Contains(metricBody, "vm_id=\"test-vm-1\"") {
		t.Errorf("metrics body missing vm_id label: %s", metricBody)
	}
	if !strings.Contains(metricBody, "dcgm_collector_scrape_success") {
		t.Errorf("metrics body missing dcgm_collector_scrape_success: %s", metricBody)
	}
	if !strings.Contains(metricBody, "dcgm_collector_targets_total") {
		t.Errorf("metrics body missing dcgm_collector_targets_total: %s", metricBody)
	}

	// Test /status
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/status", nil)
	srv.handleStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status failed: code=%d", w.Code)
	}
	var status api.CollectorStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to decode status JSON: %v", err)
	}
	if status.TotalTargets != 1 || status.HealthyTargets != 1 {
		t.Errorf("unexpected status numbers: %+v", status)
	}
}
