package transport

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
)

type mockNetNSDialer struct {
	mu           sync.Mutex
	dialedNetNS  []string
	targetServer *httptest.Server
}

func (m *mockNetNSDialer) DialContext(ctx context.Context, netns, network, address string) (net.Conn, error) {
	m.mu.Lock()
	m.dialedNetNS = append(m.dialedNetNS, netns)
	m.mu.Unlock()

	var dialer net.Dialer
	// Route all mock dials to the test server address
	serverAddr := strings.TrimPrefix(m.targetServer.URL, "http://")
	return dialer.DialContext(ctx, network, serverAddr)
}

func TestHTTPTransport_PerNetNSClient(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# HELP test_metric\ntest_metric 1\n"))
	}))
	defer ts.Close()

	mockDialer := &mockNetNSDialer{targetServer: ts}
	tr := NewHTTPTransport(2*time.Second, 1024*1024, mockDialer)

	target1 := api.TargetVM{
		VMID:    "vm-1",
		GuestIP: "10.0.1.10",
		Port:    9400,
		NetNS:   "dcgm-net1",
	}

	target2 := api.TargetVM{
		VMID:    "vm-2",
		GuestIP: "10.0.2.20",
		Port:    9400,
		NetNS:   "dcgm-net2",
	}

	target3 := api.TargetVM{
		VMID:    "vm-3",
		GuestIP: "127.0.0.1",
		Port:    9400,
		NetNS:   "", // root namespace
	}

	// 1. Scrape target1 in dcgm-net1
	data1, err := tr.GetMetrics(context.Background(), target1)
	if err != nil {
		t.Fatalf("target1 scrape failed: %v", err)
	}
	if !strings.Contains(string(data1), "test_metric 1") {
		t.Errorf("unexpected payload from target1: %s", string(data1))
	}

	// 2. Scrape target2 in dcgm-net2
	data2, err := tr.GetMetrics(context.Background(), target2)
	if err != nil {
		t.Fatalf("target2 scrape failed: %v", err)
	}
	if !strings.Contains(string(data2), "test_metric 1") {
		t.Errorf("unexpected payload from target2: %s", string(data2))
	}

	// 3. Scrape target3 in root namespace
	data3, err := tr.GetMetrics(context.Background(), target3)
	if err != nil {
		t.Fatalf("target3 scrape failed: %v", err)
	}
	if !strings.Contains(string(data3), "test_metric 1") {
		t.Errorf("unexpected payload from target3: %s", string(data3))
	}

	mockDialer.mu.Lock()
	defer mockDialer.mu.Unlock()

	if len(mockDialer.dialedNetNS) != 3 {
		t.Fatalf("expected 3 dial attempts, got %d", len(mockDialer.dialedNetNS))
	}

	if mockDialer.dialedNetNS[0] != "dcgm-net1" {
		t.Errorf("expected first dial in dcgm-net1, got: %s", mockDialer.dialedNetNS[0])
	}
	if mockDialer.dialedNetNS[1] != "dcgm-net2" {
		t.Errorf("expected second dial in dcgm-net2, got: %s", mockDialer.dialedNetNS[1])
	}
	if mockDialer.dialedNetNS[2] != "" {
		t.Errorf("expected third dial in root ns (empty), got: %s", mockDialer.dialedNetNS[2])
	}

	// Verify clients map has cached 3 distinct clients
	tr.mu.RLock()
	clientCount := len(tr.clients)
	tr.mu.RUnlock()
	if clientCount != 3 {
		t.Errorf("expected 3 cached http.Clients, got %d", clientCount)
	}
}
