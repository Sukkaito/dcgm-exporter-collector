package scraper

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/transport"
)

func TestScraper_FailureIsolation(t *testing.T) {
	// Server 1: Returns valid metrics
	s1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("DCGM_FI_DEV_GPU_UTIL{gpu=\"0\"} 45\n"))
	}))
	defer s1.Close()

	// Server 2: Returns 500 error
	s2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer s2.Close()

	// Server 3: Slow server that triggers timeout
	s3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer s3.Close()

	parseHostPort := func(u string) (string, int) {
		raw := strings.TrimPrefix(u, "http://")
		parts := strings.Split(raw, ":")
		p, _ := strconv.Atoi(parts[1])
		return parts[0], p
	}

	h1, p1 := parseHostPort(s1.URL)
	h2, p2 := parseHostPort(s2.URL)
	h3, p3 := parseHostPort(s3.URL)

	targets := []api.TargetVM{
		{VMID: "vm-ok", VMName: "vm-healthy", GuestIP: h1, Port: p1},
		{VMID: "vm-err", VMName: "vm-failing", GuestIP: h2, Port: p2},
		{VMID: "vm-slow", VMName: "vm-timeout", GuestIP: h3, Port: p3},
	}

	tr := transport.NewHTTPTransport(50*time.Millisecond, 1024*1024)
	scraper := NewScraper(tr, 50*time.Millisecond)

	results := scraper.ScrapeAll(context.Background(), targets)

	// vm-ok must succeed
	if res, ok := results["vm-ok"]; !ok || res.Error != nil {
		t.Fatalf("expected vm-ok to succeed, got: %v", res.Error)
	} else if !strings.Contains(string(res.Payload), "DCGM_FI_DEV_GPU_UTIL") {
		t.Fatalf("unexpected payload: %s", string(res.Payload))
	}

	// vm-err must report error
	if res, ok := results["vm-err"]; !ok || res.Error == nil {
		t.Fatalf("expected vm-err to fail, got nil error")
	}

	// vm-slow must report timeout
	if res, ok := results["vm-slow"]; !ok || res.Error == nil {
		t.Fatalf("expected vm-slow to timeout, got nil error")
	}

	// Check status
	status := scraper.GetStatus()
	if status.TotalTargets != 3 {
		t.Errorf("expected 3 total targets, got %d", status.TotalTargets)
	}
	if status.HealthyTargets != 1 {
		t.Errorf("expected 1 healthy target, got %d", status.HealthyTargets)
	}
}

type mockTransport struct {
	responses map[string][]byte
	errors    map[string]error
}

func (m *mockTransport) GetMetrics(ctx context.Context, target api.TargetVM) ([]byte, error) {
	if err, ok := m.errors[target.VMID]; ok {
		return nil, err
	}
	if data, ok := m.responses[target.VMID]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("unknown target")
}

func TestScraper_MockTransport(t *testing.T) {
	mock := &mockTransport{
		responses: map[string][]byte{
			"vm-1": []byte("# HELP dcgm test\n"),
		},
		errors: map[string]error{
			"vm-2": fmt.Errorf("connection refused"),
		},
	}

	scraper := NewScraper(mock, time.Second)
	targets := []api.TargetVM{
		{VMID: "vm-1", VMName: "inst-1", GuestIP: "10.0.0.10", Port: 9400},
		{VMID: "vm-2", VMName: "inst-2", GuestIP: "10.0.0.11", Port: 9400},
	}

	results := scraper.ScrapeAll(context.Background(), targets)
	if results["vm-1"].Error != nil {
		t.Errorf("vm-1 failed: %v", results["vm-1"].Error)
	}
	if results["vm-2"].Error == nil {
		t.Errorf("vm-2 should have failed")
	}
}

func TestScraper_BoundedConcurrency(t *testing.T) {
	var activeCount int32
	var maxActive int32
	var countMu sync.Mutex

	blockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		countMu.Lock()
		activeCount++
		if activeCount > maxActive {
			maxActive = activeCount
		}
		countMu.Unlock()

		time.Sleep(30 * time.Millisecond)

		countMu.Lock()
		activeCount--
		countMu.Unlock()

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("gpu_temp 40\n"))
	}))
	defer blockServer.Close()

	parts := strings.Split(strings.TrimPrefix(blockServer.URL, "http://"), ":")
	p, _ := strconv.Atoi(parts[1])

	targets := make([]api.TargetVM, 10)
	for i := 0; i < 10; i++ {
		targets[i] = api.TargetVM{
			VMID:    fmt.Sprintf("vm-%d", i),
			GuestIP: parts[0],
			Port:    p,
		}
	}

	tr := transport.NewHTTPTransport(time.Second, 1024*1024)
	// Bounded to 3 workers
	scraper := NewScraper(tr, time.Second, 3)
	results := scraper.ScrapeAll(context.Background(), targets)

	if len(results) != 10 {
		t.Fatalf("expected 10 results, got %d", len(results))
	}
	for id, res := range results {
		if res.Error != nil {
			t.Errorf("target %s failed: %v", id, res.Error)
		}
	}

	if maxActive > 3 {
		t.Errorf("expected maxActive <= 3, got %d", maxActive)
	}
}
