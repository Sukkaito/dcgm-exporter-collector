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
	err := veth.EnsureVethPair(context.Background(), "host-net1", "host-net1-ovs", "fa:16:3e:aa:bb:cc", "10.0.0.254/24", "")
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

func TestVethManager_EnsureVethPair_WithNetNS(t *testing.T) {
	mock := NewMockCommandRunner()
	mock.Errors["ip -n dcgm-net1 link show host-net1"] = fmt.Errorf("Device \"host-net1\" does not exist.")
	mock.Errors["ip link show host-net1"] = fmt.Errorf("Device \"host-net1\" does not exist.")

	veth := NewVethManager(mock)
	err := veth.EnsureVethPair(context.Background(), "host-net1", "host-net1-ovs", "fa:16:3e:aa:bb:cc", "10.0.0.254/24", "dcgm-net1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expectedCmds := []string{
		"ip -n dcgm-net1 link show host-net1",
		"ip link show host-net1",
		"ip link add host-net1 type veth peer name host-net1-ovs",
		"ip link set host-net1 netns dcgm-net1",
		"ip -n dcgm-net1 link set host-net1 address fa:16:3e:aa:bb:cc",
		"ip -n dcgm-net1 link set host-net1 up",
		"ip -n dcgm-net1 addr replace 10.0.0.254/24 dev host-net1",
		"ip link set host-net1-ovs up",
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

func TestNetNSManager(t *testing.T) {
	mock := NewMockCommandRunner()
	mock.Outputs["ip netns list"] = "other-ns\n"

	mgr := NewNetNSManager(mock)
	err := mgr.EnsureNetNS(context.Background(), "dcgm-net1")
	if err != nil {
		t.Fatalf("unexpected error ensuring netns: %v", err)
	}

	expectedCmds := []string{
		"ip netns list",
		"ip netns add dcgm-net1",
		"ip -n dcgm-net1 link set lo up",
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

	// Test delete
	mock.Commands = nil
	mock.Outputs["ip netns list"] = "dcgm-net1 (id: 0)\n"
	err = mgr.DeleteNetNS(context.Background(), "dcgm-net1")
	if err != nil {
		t.Fatalf("unexpected error deleting netns: %v", err)
	}

	expectedDelCmds := []string{
		"ip netns list",
		"ip netns del dcgm-net1",
	}
	if len(mock.Commands) != len(expectedDelCmds) {
		t.Fatalf("expected %d commands, got %d: %v", len(expectedDelCmds), len(mock.Commands), mock.Commands)
	}
	for i, expected := range expectedDelCmds {
		got := strings.Join(mock.Commands[i], " ")
		if got != expected {
			t.Errorf("del step %d: expected %q, got %q", i, expected, got)
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
	mock.Outputs["ip link show host-net1-ovs"] = "exists"
	mock.Outputs["ip netns list"] = "dcgm-net-1 (id: 0)\n"

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
			NetNS:     "dcgm-net-1",
		},
	}

	err := mgr.ReconcileEndpoints(context.Background(), endpoints)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Now reconcile with empty endpoints, which should trigger cleanup of host-net1 and dcgm-net-1
	mock.Commands = nil
	err = mgr.ReconcileEndpoints(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error on cleanup reconciliation: %v", err)
	}

	foundDelPort := false
	foundDelLink := false
	foundDelNetNS := false
	for _, cmd := range mock.Commands {
		cmdStr := strings.Join(cmd, " ")
		if strings.Contains(cmdStr, "ovs-vsctl --if-exists del-port br-int host-net1-ovs") {
			foundDelPort = true
		}
		if strings.Contains(cmdStr, "ip link del host-net1-ovs") || strings.Contains(cmdStr, "ip link del host-net1") || strings.Contains(cmdStr, "ip -n dcgm-net-1 link del host-net1") {
			foundDelLink = true
		}
		if strings.Contains(cmdStr, "ip netns del dcgm-net-1") {
			foundDelNetNS = true
		}
	}

	if !foundDelPort {
		t.Errorf("expected del-port for host-net1-ovs")
	}
	if !foundDelLink {
		t.Errorf("expected link del for veth pair")
	}
	if !foundDelNetNS {
		t.Errorf("expected ip netns del for dcgm-net-1")
	}
}
