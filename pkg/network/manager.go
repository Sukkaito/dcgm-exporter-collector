package network

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
)

// NetworkManager coordinates Linux veth interfaces and OVS port attachments.
type NetworkManager struct {
	mu          sync.Mutex
	veth        *VethManager
	ovs         *OVSManager
	netns       *NetNSManager
	bridge      string
	activeVeths map[string]api.HostNetworkEndpoint
}

// NewNetworkManager creates a new NetworkManager.
func NewNetworkManager(veth *VethManager, ovs *OVSManager, bridge string, netnsMgr ...*NetNSManager) *NetworkManager {
	if bridge == "" {
		bridge = "br-int"
	}
	var nm *NetNSManager
	if len(netnsMgr) > 0 && netnsMgr[0] != nil {
		nm = netnsMgr[0]
	} else if veth != nil && veth.runner != nil {
		nm = NewNetNSManager(veth.runner)
	}
	return &NetworkManager{
		veth:        veth,
		ovs:         ovs,
		netns:       nm,
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

		// Determine network namespace
		netns := ep.NetNS
		if netns == "" && ep.NetworkID != "" {
			shortNetID := ep.NetworkID
			if len(shortNetID) > 8 {
				shortNetID = shortNetID[:8]
			}
			netns = fmt.Sprintf("dcgm-%s", shortNetID)
			ep.NetNS = netns
		}

		// 1. Ensure netns exists if specified
		if netns != "" && m.netns != nil {
			if err := m.netns.EnsureNetNS(ctx, netns); err != nil {
				return fmt.Errorf("ensuring netns %s: %w", netns, err)
			}
		}

		// 2. Ensure veth pair exists, MAC is configured, link is UP, and IP is assigned
		if err := m.veth.EnsureVethPair(ctx, hostIf, ovsIf, ep.MAC, ep.IP, netns); err != nil {
			return fmt.Errorf("reconciling veth %s (netns: %s): %w", hostIf, netns, err)
		}

		// 3. Ensure OVS-side interface is attached to br-int with external_ids:iface-id
		if err := m.ovs.EnsurePort(ctx, m.bridge, ovsIf, ep.PortID); err != nil {
			return fmt.Errorf("reconciling OVS port %s on %s: %w", ovsIf, m.bridge, err)
		}

		desired[hostIf] = ep
		slog.Info("Reconciled endpoint", "host_if", hostIf, "ovs_if", ovsIf, "port_id", ep.PortID, "ip", ep.IP, "netns", netns)
	}

	// Clean up stale endpoints that were previously active but are no longer desired
	for hostIf, oldEp := range m.activeVeths {
		if _, stillDesired := desired[hostIf]; !stillDesired {
			ovsIf := fmt.Sprintf("%s-ovs", oldEp.VethName)
			slog.Info("Removing obsolete endpoint", "host_if", hostIf, "ovs_if", ovsIf, "netns", oldEp.NetNS)
			_ = m.ovs.DeletePort(ctx, m.bridge, ovsIf)
			_ = m.veth.DeleteVethPair(ctx, hostIf, ovsIf, oldEp.NetNS)
			if oldEp.NetNS != "" && m.netns != nil {
				_ = m.netns.DeleteNetNS(ctx, oldEp.NetNS)
			}
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
		slog.Info("Cleanup deleting endpoint", "host_if", hostIf, "ovs_if", ovsIf, "netns", ep.NetNS)
		_ = m.ovs.DeletePort(ctx, m.bridge, ovsIf)
		_ = m.veth.DeleteVethPair(ctx, hostIf, ovsIf, ep.NetNS)
		if ep.NetNS != "" && m.netns != nil {
			_ = m.netns.DeleteNetNS(ctx, ep.NetNS)
		}
	}
	m.activeVeths = make(map[string]api.HostNetworkEndpoint)
}
