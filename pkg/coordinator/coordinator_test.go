package coordinator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
)

func TestCoordinator_SyncOnce(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/host/sync" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		resp := api.SyncResponse{
			Status: "ok",
			Endpoints: []api.HostNetworkEndpoint{
				{NetworkID: "net-1", PortID: "p-1", MAC: "fa:16:3e:11:22:33", IP: "10.0.0.254/24", VethName: "host-net1"},
			},
			Targets: []api.TargetVM{
				{VMID: "vm-1", VMName: "test-vm-1", GuestIP: "10.0.0.10", Port: 9400},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	cfg := Config{
		ControllerURL: ts.URL,
		HostName:      "test-host",
		SyncTimeout:   2 * time.Second,
	}

	coord, err := NewCoordinator(cfg, nil)
	if err != nil {
		t.Fatalf("failed to create coordinator: %v", err)
	}

	err = coord.SyncOnce(context.Background())
	if err != nil {
		t.Fatalf("unexpected sync error: %v", err)
	}

	targets := coord.GetTargets()
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].VMID != "vm-1" {
		t.Errorf("expected vm-1, got %s", targets[0].VMID)
	}

	lastSync, syncErr := coord.GetSyncStatus()
	if syncErr != nil {
		t.Errorf("expected nil syncErr, got %v", syncErr)
	}
	if lastSync.IsZero() {
		t.Errorf("expected non-zero lastSync time")
	}
}

func TestCoordinator_StaticTargets(t *testing.T) {
	static := []api.TargetVM{
		{VMID: "static-1", VMName: "static-vm", GuestIP: "127.0.0.1", Port: 9400},
	}
	cfg := Config{
		StaticTargets: static,
	}

	coord, err := NewCoordinator(cfg, nil)
	if err != nil {
		t.Fatalf("failed to create coordinator: %v", err)
	}

	targets := coord.GetTargets()
	if len(targets) != 1 || targets[0].VMID != "static-1" {
		t.Errorf("unexpected static targets: %v", targets)
	}

	// Calling SyncOnce with empty ControllerURL should succeed
	err = coord.SyncOnce(context.Background())
	if err != nil {
		t.Errorf("unexpected error on empty controller URL: %v", err)
	}
}
