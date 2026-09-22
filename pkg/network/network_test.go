package network

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
)

type MockCommandRunner struct {
	mu       sync.Mutex
	Commands [][]string
	Outputs  map[string]string
	Errors   map[string]error
}

func NewMockCommandRunner() *MockCommandRunner {
	return &MockCommandRunner{
		Outputs: make(map[string]string),
		Errors:  make(map[string]error),
	}
}

func (m *MockCommandRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cmd := append([]string{name}, args...)
	m.Commands = append(m.Commands, cmd)
	cmdKey := strings.Join(cmd, " ")

	if err, ok := m.Errors[cmdKey]; ok {
		return "", err
	}
	if out, ok := m.Outputs[cmdKey]; ok {
		return out, nil
	}
	return "", nil
}

func TestVethManager_EnsureVethPair(t *testing.T) {
	mock := NewMockCommandRunner()
	// simulate link show failing because device does not exist
	mock.Errors["ip link show host-net1"] = fmt.Errorf("Device \"host-net1\" does not exist.")

	veth := NewVethManager(mock)
	err := veth.EnsureVethPair(context.Background(), "host-net1", "host-net1-ovs", "fa:16:3e:aa:bb:cc", "10.0.0.254/24")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify executed commands
	expectedCmds := []string{
		"ip link show host-net1",
		"ip link add host-net1 type veth peer name host-net1-ovs",
		"ip link set host-net1 address fa:16:3e:aa:bb:cc",
		"ip link set host-net1 up",
		"ip link set host-net1-ovs up",
		"ip addr replace 10.0.0.254/24 dev host-net1",
	}

	if len(mock.Commands) != len(expectedCmds) {
		t.Fatalf("expected %d commands, got %d: %v", len(expectedCmds), len(mock.Commands), mock.Commands)
	}

	for i, expected := range expectedCmds {
		got := strings.Join(mock.Commands[i], " ")
		if got != expected {
			t.Errorf("step %d: expected %q, got %q", i, expected, got)
		}
	}
}

func TestOVSManager_EnsurePort(t *testing.T) {
	mock := NewMockCommandRunner()
	ovs := NewOVSManager(mock, "ovs-vsctl", "br-int")

	err := ovs.EnsurePort(context.Background(), "br-int", "host-net1-ovs", "port-uuid-1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedCmds := []string{
		"ovs-vsctl --may-exist add-port br-int host-net1-ovs",
		"ovs-vsctl set Interface host-net1-ovs external_ids:iface-id=port-uuid-1234",
	}

	if len(mock.Commands) != len(expectedCmds) {
		t.Fatalf("expected %d commands, got %d", len(expectedCmds), len(mock.Commands))
	}

	for i, expected := range expectedCmds {
		got := strings.Join(mock.Commands[i], " ")
		if got != expected {
			t.Errorf("step %d: expected %q, got %q", i, expected, got)
		}
	}
}

func TestNetworkManager_ReconcileEndpoints(t *testing.T) {
	mock := NewMockCommandRunner()
	// simulate link show returning empty output (device exists)
	mock.Outputs["ip link show host-net1"] = "exists"

	veth := NewVethManager(mock)
	ovs := NewOVSManager(mock, "ovs-vsctl", "br-int")
	mgr := NewNetworkManager(veth, ovs, "br-int")

	endpoints := []api.HostNetworkEndpoint{
		{
			NetworkID: "net-1",
			PortID:    "port-1",
			MAC:       "fa:16:3e:11:22:33",
			IP:        "192.168.1.254/24",
			VethName:  "host-net1",
		},
	}

	err := mgr.ReconcileEndpoints(context.Background(), endpoints)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Now reconcile with empty endpoints, which should trigger cleanup of host-net1
	mock.Commands = nil
	err = mgr.ReconcileEndpoints(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error on cleanup reconciliation: %v", err)
	}

	foundDelPort := false
	foundDelLink := false
	for _, cmd := range mock.Commands {
		cmdStr := strings.Join(cmd, " ")
		if strings.Contains(cmdStr, "ovs-vsctl --if-exists del-port br-int host-net1-ovs") {
			foundDelPort = true
		}
		if strings.Contains(cmdStr, "ip link del host-net1") {
			foundDelLink = true
		}
	}

	if !foundDelPort {
		t.Errorf("expected del-port for host-net1-ovs")
	}
	if !foundDelLink {
		t.Errorf("expected ip link del for host-net1")
	}
}
