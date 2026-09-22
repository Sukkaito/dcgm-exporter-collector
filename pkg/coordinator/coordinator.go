package coordinator

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
	"github.com/Sukkaito/dcgm-exporter-collector/pkg/network"
)

// Config configures the control-node client and synchronization parameters.
type Config struct {
	ControllerURL string
	HostName      string
	CACertPath    string
	CertPath      string
	KeyPath       string
	SyncInterval  time.Duration
	SyncTimeout   time.Duration
	StaticTargets []api.TargetVM
}

// Coordinator synchronizes network and target definitions from the control node.
type Coordinator struct {
	cfg            Config
	httpClient     *http.Client
	networkManager *network.NetworkManager
	mu             sync.RWMutex
	targets        []api.TargetVM
	lastSync       time.Time
	lastError      error
}

// NewCoordinator creates a new Coordinator.
func NewCoordinator(cfg Config, netMgr *network.NetworkManager) (*Coordinator, error) {
	if cfg.SyncInterval <= 0 {
		cfg.SyncInterval = 60 * time.Second
	}
	if cfg.SyncTimeout <= 0 {
		cfg.SyncTimeout = 10 * time.Second
	}

	client, err := buildHTTPClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("initializing mTLS client: %w", err)
	}

	c := &Coordinator{
		cfg:            cfg,
		httpClient:     client,
		networkManager: netMgr,
		targets:        cfg.StaticTargets,
	}

	return c, nil
}

func buildHTTPClient(cfg Config) (*http.Client, error) {
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	if cfg.CACertPath != "" {
		caCert, err := os.ReadFile(cfg.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("reading CA cert %s: %w", cfg.CACertPath, err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caPool
	}

	if cfg.CertPath != "" && cfg.KeyPath != "" {
		clientCert, err := tls.LoadX509KeyPair(cfg.CertPath, cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("loading client keypair (%s, %s): %w", cfg.CertPath, cfg.KeyPath, err)
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	return &http.Client{
		Timeout: cfg.SyncTimeout,
		Transport: &http.Transport{
			TLSClientConfig:   tlsConfig,
			MaxIdleConns:      10,
			IdleConnTimeout:   30 * time.Second,
			DisableKeepAlives: false,
		},
	}, nil
}

// SyncOnce performs a single synchronization request to the control node.
func (c *Coordinator) SyncOnce(ctx context.Context) error {
	if c.cfg.ControllerURL == "" {
		// No controller URL provided, keep using static targets
		c.mu.Lock()
		c.lastSync = time.Now()
		c.lastError = nil
		c.mu.Unlock()
		return nil
	}

	reqBody := api.SyncRequest{
		Action:   "sync",
		Hostname: c.cfg.HostName,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshaling sync request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v1/host/sync", c.cfg.ControllerURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("building sync request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.recordSyncError(err)
		return fmt.Errorf("performing sync with %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("sync returned HTTP status %d", resp.StatusCode)
		c.recordSyncError(err)
		return err
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		c.recordSyncError(err)
		return fmt.Errorf("reading sync response: %w", err)
	}

	var syncResp api.SyncResponse
	if err := json.Unmarshal(respBytes, &syncResp); err != nil {
		c.recordSyncError(err)
		return fmt.Errorf("unmarshaling sync response: %w", err)
	}

	// Reconcile network endpoints if network manager is configured
	if c.networkManager != nil && len(syncResp.Endpoints) > 0 {
		if err := c.networkManager.ReconcileEndpoints(ctx, syncResp.Endpoints); err != nil {
			slog.Warn("Network reconciliation warning", "error", err)
		}
	}

	c.mu.Lock()
	c.targets = syncResp.Targets
	c.lastSync = time.Now()
	c.lastError = nil
	c.mu.Unlock()

	slog.Info("Sync complete", "endpoints", len(syncResp.Endpoints), "targets", len(syncResp.Targets))
	return nil
}

func (c *Coordinator) recordSyncError(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastError = err
}

// Start begins periodic synchronization in the background until ctx is cancelled.
func (c *Coordinator) Start(ctx context.Context) {
	ticker := time.NewTicker(c.cfg.SyncInterval)
	defer ticker.Stop()

	// Initial sync immediately
	if err := c.SyncOnce(ctx); err != nil {
		slog.Warn("Initial sync failed", "error", err)
	}

	for {
		select {
		case <-ctx.Done():
			slog.Info("Stopping sync loop")
			return
		case <-ticker.C:
			if err := c.SyncOnce(ctx); err != nil {
				slog.Warn("Periodic sync failed", "error", err)
			}
		}
	}
}

// GetTargets returns the currently active list of target VMs.
func (c *Coordinator) GetTargets() []api.TargetVM {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]api.TargetVM, len(c.targets))
	copy(result, c.targets)
	return result
}

// GetSyncStatus returns the last sync timestamp and error if any.
func (c *Coordinator) GetSyncStatus() (time.Time, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastSync, c.lastError
}
