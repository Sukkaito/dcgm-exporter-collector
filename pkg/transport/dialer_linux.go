//go:build linux

package transport

import (
	"context"
	"fmt"
	"net"
	"os"
	"runtime"
	"time"

	"golang.org/x/sys/unix"
)

// LinuxNetNSDialer implements NetNSDialer on Linux using runtime.LockOSThread and unix.Setns.
type LinuxNetNSDialer struct {
	timeout   time.Duration
	keepAlive time.Duration
	netnsDir  string
}

// NewDefaultNetNSDialer creates a new LinuxNetNSDialer.
func NewDefaultNetNSDialer(timeout, keepAlive time.Duration) *LinuxNetNSDialer {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if keepAlive <= 0 {
		keepAlive = 30 * time.Second
	}
	return &LinuxNetNSDialer{
		timeout:   timeout,
		keepAlive: keepAlive,
		netnsDir:  "/var/run/netns",
	}
}

// DialContext establishes a TCP connection, executing socket creation and connect within target netns if provided.
func (d *LinuxNetNSDialer) DialContext(ctx context.Context, netns, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   d.timeout,
		KeepAlive: d.keepAlive,
	}

	if netns == "" {
		return dialer.DialContext(ctx, network, address)
	}

	nsPath := fmt.Sprintf("%s/%s", d.netnsDir, netns)
	targetNS, err := os.Open(nsPath)
	if err != nil {
		return nil, fmt.Errorf("opening netns %s: %w", nsPath, err)
	}
	defer targetNS.Close()

	// Lock OS thread to prevent Go runtime goroutine migration while thread is in target netns
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origNS, err := os.Open("/proc/self/ns/net")
	if err != nil {
		return nil, fmt.Errorf("opening root netns: %w", err)
	}
	defer origNS.Close()

	// Switch OS thread into target netns
	if err := unix.Setns(int(targetNS.Fd()), unix.CLONE_NEWNET); err != nil {
		return nil, fmt.Errorf("entering netns %s: %w", netns, err)
	}

	// Restore thread back to the root network namespace upon completion
	defer func() {
		if err := unix.Setns(int(origNS.Fd()), unix.CLONE_NEWNET); err != nil {
			panic(fmt.Sprintf("failed restoring root netns: %v", err))
		}
	}()

	// Establish socket connection within target netns.
	// Once connected, the socket remains permanently associated with target netns.
	return dialer.DialContext(ctx, network, address)
}
