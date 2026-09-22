package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/server"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/transport"
)

func parseHostAndPort(rawURL string) (string, int) {
	clean := strings.TrimPrefix(rawURL, "http://")
	parts := strings.Split(clean, ":")
	port, _ := strconv.Atoi(parts[1])
	return parts[0], port
}

func TestEndToEndCollectorStack(t *testing.T) {
	// 1. Mock Guest Exporter 1
	guest1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization (in %).\n# TYPE DCGM_FI_DEV_GPU_UTIL gauge\nDCGM_FI_DEV_GPU_UTIL{gpu=\"0\",UUID=\"GPU-111\"} 75\n"))
	}))
	defer guest1.Close()

	// 2. Mock Guest Exporter 2
	guest2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# HELP DCGM_FI_DEV_GPU_UTIL GPU utilization (in %).\n# TYPE DCGM_FI_DEV_GPU_UTIL gauge\nDCGM_FI_DEV_GPU_UTIL{gpu=\"0\",UUID=\"GPU-222\"} 90\n"))
	}))
	defer guest2.Close()

	ip1, port1 := parseHostAndPort(guest1.URL)
	ip2, port2 := parseHostAndPort(guest2.URL)

	// 3. Mock Control Node Sync API
	ctrl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/host/sync" {
			http.NotFound(w, r)
			return
		}

		resp := api.SyncResponse{
			Status: "ok",
			Endpoints: []api.HostNetworkEndpoint{
				{NetworkID: "net-a", PortID: "port-a", MAC: "fa:16:3e:00:11:22", IP: "10.0.0.254/24", VethName: "host-net-a"},
			},
			Targets: []api.TargetVM{
				{VMID: "vm-uuid-1", VMName: "worker-gpu-1", ProjectID: "proj-1", GuestIP: ip1, Port: port1},
				{VMID: "vm-uuid-2", VMName: "worker-gpu-2", ProjectID: "proj-2", GuestIP: ip2, Port: port2},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ctrl.Close()

	// 4. Initialize Compute Agent subsystems
	coordCfg := coordinator.Config{
		ControllerURL: ctrl.URL,
		HostName:      "compute-node-hgx",
		SyncTimeout:   2 * time.Second,
	}
	coord, err := coordinator.NewCoordinator(coordCfg, nil)
	if err != nil {
		t.Fatalf("failed to create coordinator: %v", err)
	}

	tr := transport.NewHTTPTransport(2*time.Second, 1024*1024)
	sc := scraper.NewScraper(tr, 2*time.Second)
	enricher := processor.NewMetricEnricher("compute-node-hgx")

	srv := server.NewServer(":0", coord, sc, enricher, 50*time.Millisecond)

	// Perform initial sync
	if err := coord.SyncOnce(context.Background()); err != nil {
		t.Fatalf("sync failed: %v", err)
	}

	testServer := httptest.NewServer(srv.Handler())
	defer testServer.Close()

	// Test GET /healthz
	resp, err := http.Get(fmt.Sprintf("%s/healthz", testServer.URL))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz failed: %v, status: %d", err, resp.StatusCode)
	}

	// Test GET /readyz
	resp, err = http.Get(fmt.Sprintf("%s/readyz", testServer.URL))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz failed: %v, status: %d", err, resp.StatusCode)
	}

	// Test GET /metrics
	resp, err = http.Get(fmt.Sprintf("%s/metrics", testServer.URL))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics failed: %v, status: %d", err, resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	metricsOutput := string(body)

	// Check enriched labels
	if !strings.Contains(metricsOutput, "host=\"compute-node-hgx\"") {
		t.Errorf("metrics missing host label: %s", metricsOutput)
	}
	if !strings.Contains(metricsOutput, "vm_id=\"vm-uuid-1\"") {
		t.Errorf("metrics missing vm-uuid-1: %s", metricsOutput)
	}
	if !strings.Contains(metricsOutput, "vm_id=\"vm-uuid-2\"") {
		t.Errorf("metrics missing vm-uuid-2: %s", metricsOutput)
	}
	if !strings.Contains(metricsOutput, "project_id=\"proj-1\"") {
		t.Errorf("metrics missing proj-1: %s", metricsOutput)
	}

	// Test GET /status
	resp, err = http.Get(fmt.Sprintf("%s/status", testServer.URL))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("status failed: %v, status: %d", err, resp.StatusCode)
	}
	var status api.CollectorStatus
	_ = json.NewDecoder(resp.Body).Decode(&status)
	if status.TotalTargets != 2 || status.HealthyTargets != 2 {
		t.Errorf("expected 2 healthy targets, got %+v", status)
	}
}
