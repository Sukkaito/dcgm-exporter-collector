package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestServer_StatusPrunesDeletedTargets(t *testing.T) {
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("# HELP dcgm test\nDCGM_FI_DEV_GPU_UTIL{gpu=\"0\"} 50\n"))
	}))
	defer ts1.Close()

	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("# HELP dcgm test\nDCGM_FI_DEV_GPU_UTIL{gpu=\"0\"} 75\n"))
	}))
	defer ts2.Close()

	parts1 := strings.Split(strings.TrimPrefix(ts1.URL, "http://"), ":")
	p1, _ := strconv.Atoi(parts1[1])
	parts2 := strings.Split(strings.TrimPrefix(ts2.URL, "http://"), ":")
	p2, _ := strconv.Atoi(parts2[1])

	initialTargets := []api.TargetVM{
		{VMID: "test-vm-1", VMName: "inst-1", GuestIP: parts1[0], Port: p1},
		{VMID: "test-vm-2", VMName: "inst-2", GuestIP: parts2[0], Port: p2},
	}

	coord, err := coordinator.NewCoordinator(coordinator.Config{
		StaticTargets: initialTargets,
	}, nil)
	if err != nil {
		t.Fatalf("failed to create coordinator: %v", err)
	}

	tr := transport.NewHTTPTransport(time.Second, 1024*1024)
	sc := scraper.NewScraper(tr, time.Second)
	enricher := processor.NewMetricEnricher("hgx087")
	srv := NewServer(":0", coord, sc, enricher, 100*time.Millisecond)

	// Scrape metrics initially to populate exporterStatus
	w := httptest.NewRecorder()
	srv.handleMetrics(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	// Status should have 2 targets
	w = httptest.NewRecorder()
	srv.handleStatus(w, httptest.NewRequest(http.MethodGet, "/status", nil))
	var status api.CollectorStatus
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to decode status: %v", err)
	}
	if status.TotalTargets != 2 {
		t.Fatalf("expected 2 targets initially, got %d", status.TotalTargets)
	}

	// Simulate VM deletion: coordinator target list updated with only test-vm-1
	coord.SetTargets([]api.TargetVM{initialTargets[0]})

	// Query /status: test-vm-2 must be pruned immediately without needing another /metrics scrape
	w = httptest.NewRecorder()
	srv.handleStatus(w, httptest.NewRequest(http.MethodGet, "/status", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("failed to decode status: %v", err)
	}
	if status.TotalTargets != 1 {
		t.Fatalf("expected 1 target after deletion, got %d", status.TotalTargets)
	}
	if status.Exporters[0].VMID != "test-vm-1" {
		t.Errorf("expected test-vm-1, got %s", status.Exporters[0].VMID)
	}
}
