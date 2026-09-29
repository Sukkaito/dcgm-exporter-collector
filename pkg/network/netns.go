package network

import (
	"context"
	"fmt"
	"strings"
)

// NetNSManager manages Linux network namespaces using the iproute2 command runner.
type NetNSManager struct {
	runner CommandRunner
}

// NewNetNSManager creates a new NetNSManager.
func NewNetNSManager(runner CommandRunner) *NetNSManager {
	return &NetNSManager{runner: runner}
}

// NetNSExists checks whether a network namespace exists.
func (m *NetNSManager) NetNSExists(ctx context.Context, name string) (bool, error) {
	out, err := m.runner.Run(ctx, "ip", "netns", "list")
	if err != nil {
		return false, err
	}
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == name {
			return true, nil
		}
	}
	return false, nil
}

// EnsureNetNS idempotently creates a network namespace and brings up its loopback interface.
func (m *NetNSManager) EnsureNetNS(ctx context.Context, name string) error {
	exists, err := m.NetNSExists(ctx, name)
	if err != nil {
		return fmt.Errorf("checking netns %s: %w", name, err)
	}
	if !exists {
		if _, err := m.runner.Run(ctx, "ip", "netns", "add", name); err != nil {
			if !strings.Contains(err.Error(), "File exists") {
				return fmt.Errorf("creating netns %s: %w", name, err)
			}
		}
	}
	// Bring up loopback interface inside the netns
	if _, err := m.runner.Run(ctx, "ip", "-n", name, "link", "set", "lo", "up"); err != nil {
		return fmt.Errorf("bringing up loopback in netns %s: %w", name, err)
	}
	return nil
}

// DeleteNetNS removes a network namespace if it exists.
func (m *NetNSManager) DeleteNetNS(ctx context.Context, name string) error {
	exists, err := m.NetNSExists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err = m.runner.Run(ctx, "ip", "netns", "del", name)
	return err
}
