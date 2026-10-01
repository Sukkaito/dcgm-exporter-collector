package transport

import (
	"context"
	"net"
)

// NetNSDialer abstracts establishing network connections, optionally within a specified Linux network namespace.
type NetNSDialer interface {
	DialContext(ctx context.Context, netns, network, address string) (net.Conn, error)
}
