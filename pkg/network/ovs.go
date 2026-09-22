package network

import (
	"context"
	"fmt"
	"strings"
)

// OVSManager manages Open vSwitch ports using ovs-vsctl.
type OVSManager struct {
	runner        CommandRunner
	ovsVsctlPath  string
	defaultBridge string
}

// NewOVSManager creates a new OVSManager.
func NewOVSManager(runner CommandRunner, ovsVsctlPath, defaultBridge string) *OVSManager {
	if ovsVsctlPath == "" {
		ovsVsctlPath = "ovs-vsctl"
	}
	if defaultBridge == "" {
		defaultBridge = "br-int"
	}
	return &OVSManager{
		runner:        runner,
		ovsVsctlPath:  ovsVsctlPath,
		defaultBridge: defaultBridge,
	}
}

// EnsurePort idempotently attaches a port to the bridge and sets its external_ids:iface-id.
func (o *OVSManager) EnsurePort(ctx context.Context, bridge, portName, ifaceID string) error {
	if bridge == "" {
		bridge = o.defaultBridge
	}

	// add-port with --may-exist ensures idempotency
	_, err := o.runner.Run(ctx, o.ovsVsctlPath, "--may-exist", "add-port", bridge, portName)
	if err != nil {
		return fmt.Errorf("ovs-vsctl add-port %s %s failed: %w", bridge, portName, err)
	}

	// Set external_ids:iface-id for OVN port binding
	if ifaceID != "" {
		extIDArg := fmt.Sprintf("external_ids:iface-id=%s", ifaceID)
		_, err = o.runner.Run(ctx, o.ovsVsctlPath, "set", "Interface", portName, extIDArg)
		if err != nil {
			return fmt.Errorf("ovs-vsctl set Interface %s %s failed: %w", portName, extIDArg, err)
		}
	}

	return nil
}

// DeletePort removes a port from the bridge if it exists.
func (o *OVSManager) DeletePort(ctx context.Context, bridge, portName string) error {
	if bridge == "" {
		bridge = o.defaultBridge
	}
	_, err := o.runner.Run(ctx, o.ovsVsctlPath, "--if-exists", "del-port", bridge, portName)
	return err
}

// PortExists checks whether a port exists in Open vSwitch.
func (o *OVSManager) PortExists(ctx context.Context, portName string) (bool, error) {
	_, err := o.runner.Run(ctx, o.ovsVsctlPath, "port-to-br", portName)
	if err != nil {
		if strings.Contains(err.Error(), "no port named") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
