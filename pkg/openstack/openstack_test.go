package openstack

import (
	"context"
	"testing"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
)

func TestMockOpenStackClient(t *testing.T) {
	mock := NewMockOpenStackClient()
	mock.VMsByHost["hgx087"] = []api.TargetVM{
		{VMID: "vm-1", VMName: "ai-box-1", GuestIP: "10.0.0.15", Port: 9400},
	}

	targets, err := mock.DiscoverComputeVMs(context.Background(), "hgx087")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 || targets[0].VMID != "vm-1" {
		t.Fatalf("unexpected targets: %+v", targets)
	}

	endpoints, err := mock.EnsureHostPorts(context.Background(), "hgx087", []string{"net-uuid-123456789"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(endpoints))
	}
	if endpoints[0].VethName != "host-net-net-uuid" {
		t.Errorf("expected host-net-net-uuid, got %s", endpoints[0].VethName)
	}
}

func TestGophercloudClient_MatchesExtraSpecs(t *testing.T) {
	client := &GophercloudClient{
		filter: DefaultVMFilterConfig(),
	}

	// 1. Matches user's exact metadata with RTX4090:1 and family=gpu
	userSpecs := map[string]string{
		"availability_zone":     "lab-24-a",
		"family":                "gpu",
		"pci_passthrough:alias": "RTX4090:1",
	}
	if !client.matchesExtraSpecs(userSpecs) {
		t.Errorf("expected userSpecs to match GPU filter")
	}

	// 2. Matches arbitrary GPU alias (e.g. RTX4090:1) even without family=gpu
	specsRTX := map[string]string{
		"pci_passthrough:alias": "RTX4090:1",
	}
	if !client.matchesExtraSpecs(specsRTX) {
		t.Errorf("expected specsRTX to match GPU filter regardless of GPU name")
	}

	// 3. Matches family=gpu even with different key
	specsFamily := map[string]string{
		"family": "gpu",
	}
	if !client.matchesExtraSpecs(specsFamily) {
		t.Errorf("expected specsFamily to match GPU filter")
	}

	// 4. Missing key and non-GPU family
	specsNone := map[string]string{
		"availability_zone": "lab-24-a",
		"family":            "cpu",
		"hw:cpu_policy":     "dedicated",
	}
	if client.matchesExtraSpecs(specsNone) {
		t.Errorf("expected specsNone not to match GPU filter")
	}
}
