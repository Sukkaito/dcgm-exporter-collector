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

// LinkExistsInNetNS checks whether an interface exists in the specified network namespace (or root if netns is empty).
func (m *VethManager) LinkExistsInNetNS(ctx context.Context, ifName, netns string) (bool, error) {
	var args []string
	if netns != "" {
		args = []string{"-n", netns, "link", "show", ifName}
	} else {
		args = []string{"link", "show", ifName}
	}
	_, err := m.runner.Run(ctx, "ip", args...)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") || strings.Contains(err.Error(), "Device") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// LinkExists checks whether an interface exists in the current network namespace.
func (m *VethManager) LinkExists(ctx context.Context, ifName string) (bool, error) {
	return m.LinkExistsInNetNS(ctx, ifName, "")
}

// EnsureVethPair idempotently creates a veth pair, assigns MAC and IP address, and brings both ends UP.
// If netns is non-empty, hostIf is moved into and configured inside the specified network namespace.
func (m *VethManager) EnsureVethPair(ctx context.Context, hostIf, ovsIf, mac, ipWithCIDR, netns string) error {
	if netns == "" {
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

	// NetNS is specified:
	// 1. Check if hostIf exists in netns
	existsInNS, err := m.LinkExistsInNetNS(ctx, hostIf, netns)
	if err != nil {
		return fmt.Errorf("checking link %s in netns %s: %w", hostIf, netns, err)
	}

	if !existsInNS {
		// Check if hostIf exists in root ns (e.g. created previously before moving)
		existsInRoot, err := m.LinkExists(ctx, hostIf)
		if err != nil {
			return fmt.Errorf("checking link %s in root ns: %w", hostIf, err)
		}
		if !existsInRoot {
			// Create veth pair in root namespace
			_, err = m.runner.Run(ctx, "ip", "link", "add", hostIf, "type", "veth", "peer", "name", ovsIf)
			if err != nil {
				return fmt.Errorf("creating veth pair %s <-> %s: %w", hostIf, ovsIf, err)
			}
		}
		// Move hostIf into the target netns
		if _, err := m.runner.Run(ctx, "ip", "link", "set", hostIf, "netns", netns); err != nil {
			return fmt.Errorf("moving %s to netns %s: %w", hostIf, netns, err)
		}
	}

	// 2. Set MAC address on hostIf inside netns
	if mac != "" {
		if _, err := m.runner.Run(ctx, "ip", "-n", netns, "link", "set", hostIf, "address", mac); err != nil {
			return fmt.Errorf("setting MAC on %s in netns %s: %w", hostIf, netns, err)
		}
	}

	// 3. Bring up hostIf inside netns
	if _, err := m.runner.Run(ctx, "ip", "-n", netns, "link", "set", hostIf, "up"); err != nil {
		return fmt.Errorf("bringing up %s in netns %s: %w", hostIf, netns, err)
	}

	// 4. Assign local IP address to hostIf inside netns
	if ipWithCIDR != "" {
		if _, err := m.runner.Run(ctx, "ip", "-n", netns, "addr", "replace", ipWithCIDR, "dev", hostIf); err != nil {
			return fmt.Errorf("assigning IP %s to %s in netns %s: %w", ipWithCIDR, hostIf, netns, err)
		}
	}

	// 5. Bring up ovsIf in root namespace
	if _, err := m.runner.Run(ctx, "ip", "link", "set", ovsIf, "up"); err != nil {
		return fmt.Errorf("bringing up %s in root ns: %w", ovsIf, err)
	}

	return nil
}

// DeleteVethPair deletes the host interface, which automatically cleans up the peer interface.
func (m *VethManager) DeleteVethPair(ctx context.Context, hostIf, ovsIf, netns string) error {
	if ovsIf != "" {
		exists, _ := m.LinkExists(ctx, ovsIf)
		if exists {
			_, err := m.runner.Run(ctx, "ip", "link", "del", ovsIf)
			return err
		}
	}

	if netns != "" {
		exists, _ := m.LinkExistsInNetNS(ctx, hostIf, netns)
		if exists {
			_, err := m.runner.Run(ctx, "ip", "-n", netns, "link", "del", hostIf)
			return err
		}
	} else {
		exists, _ := m.LinkExists(ctx, hostIf)
		if exists {
			_, err := m.runner.Run(ctx, "ip", "link", "del", hostIf)
			return err
		}
	}
	return nil
}
