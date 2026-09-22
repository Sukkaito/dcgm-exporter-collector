package openstack

import (
	"context"
	"fmt"
	"sync"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
)

// MockOpenStackClient simulates Nova and Neutron operations for testing.
type MockOpenStackClient struct {
	mu          sync.RWMutex
	VMsByHost   map[string][]api.TargetVM
	Endpoints   map[string][]api.HostNetworkEndpoint
	HealthErr   error
	DiscoverErr error
	EnsureErr   error
}

// NewMockOpenStackClient creates a new MockOpenStackClient.
func NewMockOpenStackClient() *MockOpenStackClient {
	return &MockOpenStackClient{
		VMsByHost: make(map[string][]api.TargetVM),
		Endpoints: make(map[string][]api.HostNetworkEndpoint),
	}
}

// HealthCheck returns configured health error.
func (m *MockOpenStackClient) HealthCheck(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.HealthErr
}

// DiscoverComputeVMs returns mock VMs registered for the compute host.
func (m *MockOpenStackClient) DiscoverComputeVMs(ctx context.Context, computeHost string) ([]api.TargetVM, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.DiscoverErr != nil {
		return nil, m.DiscoverErr
	}

	vms, ok := m.VMsByHost[computeHost]
	if !ok {
		return nil, nil
	}
	res := make([]api.TargetVM, len(vms))
	copy(res, vms)
	return res, nil
}

// EnsureHostPorts returns mock endpoints for the given networks.
func (m *MockOpenStackClient) EnsureHostPorts(ctx context.Context, computeHost string, networkIDs []string) ([]api.HostNetworkEndpoint, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.EnsureErr != nil {
		return nil, m.EnsureErr
	}

	var result []api.HostNetworkEndpoint
	for _, netID := range networkIDs {
		short := netID
		if len(short) > 8 {
			short = short[:8]
		}

		result = append(result, api.HostNetworkEndpoint{
			NetworkID: netID,
			PortID:    fmt.Sprintf("mock-port-%s-%s", computeHost, short),
			MAC:       "fa:16:3e:aa:bb:cc",
			IP:        "10.0.0.254/24",
			VethName:  fmt.Sprintf("host-net-%s", short),
		})
	}
	return result, nil
}
