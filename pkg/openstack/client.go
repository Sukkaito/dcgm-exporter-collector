package openstack

import (
	"context"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
)

// OpenStackClient defines the operations required from OpenStack Nova and Neutron.
type OpenStackClient interface {
	// DiscoverComputeVMs returns all active GPU passthrough VMs running on the specified compute host.
	DiscoverComputeVMs(ctx context.Context, computeHost string) ([]api.TargetVM, error)

	// EnsureHostPorts ensures persistent host-side Neutron ports exist for the given networks on the compute host.
	EnsureHostPorts(ctx context.Context, computeHost string, networkIDs []string) ([]api.HostNetworkEndpoint, error)

	// HealthCheck verifies connectivity to Keystone, Nova, and Neutron services.
	HealthCheck(ctx context.Context) error
}

// VMFilterConfig defines options for determining if an instance has GPU passthrough.
type VMFilterConfig struct {
	// ExtraSpecsKey is the flavor extra specs key to inspect (default "pci_passthrough:alias").
	ExtraSpecsKey string

	// ExtraSpecsKeyword matches the value of the extra spec (e.g. "gpu"). If empty, checks for key presence.
	ExtraSpecsKeyword string

	// RequireTag optionally requires a specific server tag (e.g. "dcgm-telemetry").
	RequireTag string
}

// DefaultVMFilterConfig returns standard GPU passthrough flavor extra specs filter.
func DefaultVMFilterConfig() VMFilterConfig {
	return VMFilterConfig{
		ExtraSpecsKey:     "pci_passthrough:alias",
		ExtraSpecsKeyword: "", // Empty matches any GPU name (e.g. RTX4090:1)
	}
}
