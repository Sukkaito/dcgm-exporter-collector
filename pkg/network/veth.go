package network

import (
	"context"
	"fmt"
	"strings"
)

// VethManager handles creating and configuring Linux veth interfaces.
type VethManager struct {
	runner CommandRunner
}

// NewVethManager creates a new VethManager.
func NewVethManager(runner CommandRunner) *VethManager {
	return &VethManager{runner: runner}
}

// LinkExists checks whether an interface exists in the current network namespace.
func (m *VethManager) LinkExists(ctx context.Context, ifName string) (bool, error) {
	_, err := m.runner.Run(ctx, "ip", "link", "show", ifName)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "Device") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// EnsureVethPair idempotently creates a veth pair, assigns MAC and IP address, and brings both ends UP.
func (m *VethManager) EnsureVethPair(ctx context.Context, hostIf, ovsIf, mac, ipWithCIDR string) error {
	exists, err := m.LinkExists(ctx, hostIf)
	if err != nil {
		return fmt.Errorf("checking link %s: %w", hostIf, err)
	}

	if !exists {
		// Create the veth pair
		_, err = m.runner.Run(ctx, "ip", "link", "add", hostIf, "type", "veth", "peer", "name", ovsIf)
		if err != nil {
			return fmt.Errorf("creating veth pair %s <-> %s: %w", hostIf, ovsIf, err)
		}
	}

	// Set MAC address on the host-facing interface
	if mac != "" {
		if _, err := m.runner.Run(ctx, "ip", "link", "set", hostIf, "address", mac); err != nil {
			return fmt.Errorf("setting MAC on %s: %w", hostIf, err)
		}
	}

	// Bring up both interfaces
	if _, err := m.runner.Run(ctx, "ip", "link", "set", hostIf, "up"); err != nil {
		return fmt.Errorf("bringing up %s: %w", hostIf, err)
	}
	if _, err := m.runner.Run(ctx, "ip", "link", "set", ovsIf, "up"); err != nil {
		return fmt.Errorf("bringing up %s: %w", ovsIf, err)
	}

	// Assign local IP address (using 'replace' for idempotency)
	if ipWithCIDR != "" {
		if _, err := m.runner.Run(ctx, "ip", "addr", "replace", ipWithCIDR, "dev", hostIf); err != nil {
			return fmt.Errorf("assigning IP %s to %s: %w", ipWithCIDR, hostIf, err)
		}
	}

	return nil
}

// DeleteVethPair deletes the host interface, which automatically cleans up the peer interface.
func (m *VethManager) DeleteVethPair(ctx context.Context, hostIf string) error {
	exists, err := m.LinkExists(ctx, hostIf)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err = m.runner.Run(ctx, "ip", "link", "del", hostIf)
	return err
}
