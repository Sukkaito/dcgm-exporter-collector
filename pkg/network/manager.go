package network

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
)

// NetworkManager coordinates Linux veth interfaces and OVS port attachments.
type NetworkManager struct {
	mu          sync.Mutex
	veth        *VethManager
	ovs         *OVSManager
	bridge      string
	activeVeths map[string]api.HostNetworkEndpoint
}

// NewNetworkManager creates a new NetworkManager.
func NewNetworkManager(veth *VethManager, ovs *OVSManager, bridge string) *NetworkManager {
	if bridge == "" {
		bridge = "br-int"
	}
	return &NetworkManager{
		veth:        veth,
		ovs:         ovs,
		bridge:      bridge,
		activeVeths: make(map[string]api.HostNetworkEndpoint),
	}
}

// ReconcileEndpoints ensures all desired endpoints exist and are properly attached to OVS.
func (m *NetworkManager) ReconcileEndpoints(ctx context.Context, endpoints []api.HostNetworkEndpoint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	desired := make(map[string]api.HostNetworkEndpoint)

	for _, ep := range endpoints {
		if ep.VethName == "" {
			continue
		}
		hostIf := ep.VethName
		ovsIf := fmt.Sprintf("%s-ovs", ep.VethName)

		// 1. Ensure veth pair exists, MAC is configured, link is UP, and IP is assigned
		if err := m.veth.EnsureVethPair(ctx, hostIf, ovsIf, ep.MAC, ep.IP); err != nil {
			return fmt.Errorf("reconciling veth %s: %w", hostIf, err)
		}

		// 2. Ensure OVS-side interface is attached to br-int with external_ids:iface-id
		if err := m.ovs.EnsurePort(ctx, m.bridge, ovsIf, ep.PortID); err != nil {
			return fmt.Errorf("reconciling OVS port %s on %s: %w", ovsIf, m.bridge, err)
		}

		desired[hostIf] = ep
		log.Printf("[network] Reconciled endpoint host_if=%s ovs_if=%s port_id=%s ip=%s", hostIf, ovsIf, ep.PortID, ep.IP)
	}

	// Clean up stale endpoints that were previously active but are no longer desired
	for hostIf, oldEp := range m.activeVeths {
		if _, stillDesired := desired[hostIf]; !stillDesired {
			ovsIf := fmt.Sprintf("%s-ovs", oldEp.VethName)
			log.Printf("[network] Removing obsolete endpoint host_if=%s ovs_if=%s", hostIf, ovsIf)
			_ = m.ovs.DeletePort(ctx, m.bridge, ovsIf)
			_ = m.veth.DeleteVethPair(ctx, hostIf)
		}
	}

	m.activeVeths = desired
	return nil
}

// Cleanup removes all active veth pairs and OVS ports managed by this instance.
func (m *NetworkManager) Cleanup(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for hostIf, ep := range m.activeVeths {
		ovsIf := fmt.Sprintf("%s-ovs", ep.VethName)
		log.Printf("[network] Cleanup deleting host_if=%s ovs_if=%s", hostIf, ovsIf)
		_ = m.ovs.DeletePort(ctx, m.bridge, ovsIf)
		_ = m.veth.DeleteVethPair(ctx, hostIf)
	}
	m.activeVeths = make(map[string]api.HostNetworkEndpoint)
}
