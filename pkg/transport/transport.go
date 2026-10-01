package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
)

// TelemetryTransport abstracts the mechanism used to fetch raw metrics from a target VM.
type TelemetryTransport interface {
	GetMetrics(ctx context.Context, target api.TargetVM) ([]byte, error)
}

// HTTPTransport implements TelemetryTransport over IPv4 HTTP with per-netns connection pooling.
type HTTPTransport struct {
	mu           sync.RWMutex
	clients      map[string]*http.Client // key: netns ("" for default root namespace)
	dialer       NetNSDialer
	timeout      time.Duration
	maxBytesRead int64
}

// NewHTTPTransport creates an HTTP transport with custom timeouts, connection pooling, and netns support.
func NewHTTPTransport(timeout time.Duration, maxBytesRead int64, dialers ...NetNSDialer) *HTTPTransport {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if maxBytesRead <= 0 {
		maxBytesRead = 10 * 1024 * 1024 // 10MB limit
	}

	var d NetNSDialer
	if len(dialers) > 0 && dialers[0] != nil {
		d = dialers[0]
	} else {
		d = NewDefaultNetNSDialer(timeout, 30*time.Second)
	}

	return &HTTPTransport{
		clients:      make(map[string]*http.Client),
		dialer:       d,
		timeout:      timeout,
		maxBytesRead: maxBytesRead,
	}
}

func (t *HTTPTransport) getClient(netns string) *http.Client {
	t.mu.RLock()
	c, ok := t.clients[netns]
	t.mu.RUnlock()
	if ok {
		return c
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if c, ok = t.clients[netns]; ok {
		return c
	}

	targetNS := netns
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return t.dialer.DialContext(ctx, targetNS, network, addr)
		},
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	client := &http.Client{
		Transport: tr,
		Timeout:   t.timeout,
	}

	t.clients[netns] = client
	return client
}

// GetMetrics scrapes the Prometheus endpoint of the specified target VM within its designated network namespace.
func (t *HTTPTransport) GetMetrics(ctx context.Context, target api.TargetVM) ([]byte, error) {
	port := target.Port
	if port <= 0 {
		port = 9400
	}
	url := fmt.Sprintf("http://%s:%d/metrics", target.GuestIP, port)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", url, err)
	}
	req.Header.Set("User-Agent", "dcgm-compute-collector")

	client := t.getClient(target.NetNS)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scraping %s (netns: %s): %w", url, target.NetNS, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("scraping %s returned HTTP %d", url, resp.StatusCode)
	}

	lr := io.LimitReader(resp.Body, t.maxBytesRead)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", url, err)
	}

	return data, nil
}
