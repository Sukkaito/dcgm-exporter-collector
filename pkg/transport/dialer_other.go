//go:build !linux

package transport

import (
	"context"
	"net"
	"time"
)

// FallbackNetNSDialer falls back to standard dialing on non-Linux platforms.
type FallbackNetNSDialer struct {
	timeout   time.Duration
	keepAlive time.Duration
}

// NewDefaultNetNSDialer creates a fallback dialer on non-Linux platforms.
func NewDefaultNetNSDialer(timeout, keepAlive time.Duration) NetNSDialer {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if keepAlive <= 0 {
		keepAlive = 30 * time.Second
	}
	return &FallbackNetNSDialer{
		timeout:   timeout,
		keepAlive: keepAlive,
	}
}

// DialContext establishes a connection in the default network namespace.
func (d *FallbackNetNSDialer) DialContext(ctx context.Context, netns, network, address string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout:   d.timeout,
		KeepAlive: d.keepAlive,
	}
	return dialer.DialContext(ctx, network, address)
}
