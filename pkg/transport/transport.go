package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
)

// TelemetryTransport abstracts the mechanism used to fetch raw metrics from a target VM.
type TelemetryTransport interface {
	GetMetrics(ctx context.Context, target api.TargetVM) ([]byte, error)
}

// HTTPTransport implements TelemetryTransport over IPv4 HTTP.
type HTTPTransport struct {
	client       *http.Client
	maxBytesRead int64
}

// NewHTTPTransport creates an HTTP transport with custom timeouts and connection pooling.
func NewHTTPTransport(timeout time.Duration, maxBytesRead int64) *HTTPTransport {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if maxBytesRead <= 0 {
		maxBytesRead = 10 * 1024 * 1024 // 10MB limit
	}

	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
	}

	tr := &http.Transport{
		DialContext:         dialer.DialContext,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
	}

	return &HTTPTransport{
		client: &http.Client{
			Transport: tr,
			Timeout:   timeout,
		},
		maxBytesRead: maxBytesRead,
	}
}

// GetMetrics scrapes the Prometheus endpoint of the specified target VM.
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

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scraping %s: %w", url, err)
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
