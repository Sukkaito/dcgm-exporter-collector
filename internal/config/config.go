package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
)

// AgentConfig holds the complete configuration for the compute-node collector agent.
type AgentConfig struct {
	ControllerURL    string         `json:"controller_url"`
	HostName         string         `json:"hostname"`
	CACertPath       string         `json:"ca_cert_path"`
	CertPath         string         `json:"cert_path"`
	KeyPath          string         `json:"key_path"`
	SyncInterval     time.Duration  `json:"sync_interval"`
	SyncTimeout      time.Duration  `json:"sync_timeout"`
	ListenAddr       string         `json:"listen_addr"`
	ScrapeTimeout    time.Duration  `json:"scrape_timeout"`
	MaxScrapeWorkers int            `json:"max_scrape_workers"`
	CacheTTL         time.Duration  `json:"cache_ttl"`
	OVSBridge        string         `json:"ovs_bridge"`
	OVSVsctlPath     string         `json:"ovs_vsctl_path"`
	LogFormat        string         `json:"log_format"`
	LogLevel         string         `json:"log_level"`
	StaticTargets    []api.TargetVM `json:"static_targets,omitempty"`
}

// Load loads configuration from command-line flags, environment variables, and optional config file.
func Load(args []string) (*AgentConfig, error) {
	fs := flag.NewFlagSet("dcgm-compute-agent", flag.ContinueOnError)

	cfgFile := fs.String("config", getEnv("DCGM_CONFIG_FILE", ""), "Path to JSON configuration file")
	controllerURL := fs.String("controller-url", getEnv("DCGM_CONTROLLER_URL", ""), "Control node HTTPS URL (e.g. https://control.openstack.local:8443)")
	hostName := fs.String("hostname", getEnv("DCGM_HOSTNAME", defaultHostName()), "Compute host name for enrichment and sync")
	caCert := fs.String("ca-cert", getEnv("DCGM_CA_CERT", ""), "Path to CA certificate for mTLS")
	certPath := fs.String("cert", getEnv("DCGM_CLIENT_CERT", ""), "Path to client certificate for mTLS")
	keyPath := fs.String("key", getEnv("DCGM_CLIENT_KEY", ""), "Path to client private key for mTLS")
	syncInterval := fs.Duration("sync-interval", getEnvDuration("DCGM_SYNC_INTERVAL", 60*time.Second), "Interval between control node syncs")
	syncTimeout := fs.Duration("sync-timeout", getEnvDuration("DCGM_SYNC_TIMEOUT", 10*time.Second), "Timeout for control node sync requests")
	listenAddr := fs.String("listen-addr", getEnv("DCGM_LISTEN_ADDR", ":9405"), "Listen address for Prometheus and health endpoints")
	scrapeTimeout := fs.Duration("scrape-timeout", getEnvDuration("DCGM_SCRAPE_TIMEOUT", 3*time.Second), "Per-target scrape timeout")
	maxScrapeWorkers := fs.Int("max-scrape-workers", getEnvInt("DCGM_MAX_SCRAPE_WORKERS", 8), "Maximum concurrent scrape workers")
	cacheTTL := fs.Duration("cache-ttl", getEnvDuration("DCGM_CACHE_TTL", 5*time.Second), "Metric cache TTL")
	ovsBridge := fs.String("ovs-bridge", getEnv("DCGM_OVS_BRIDGE", "br-int"), "Open vSwitch integration bridge")
	ovsVsctlPath := fs.String("ovs-vsctl", getEnv("DCGM_OVS_VSCTL", "ovs-vsctl"), "Path to ovs-vsctl binary")
	logFormat := fs.String("log-format", getEnv("DCGM_LOG_FORMAT", "text"), "Log format (text or json)")
	logLevel := fs.String("log-level", getEnv("DCGM_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg := &AgentConfig{
		ControllerURL:    *controllerURL,
		HostName:         *hostName,
		CACertPath:       *caCert,
		CertPath:         *certPath,
		KeyPath:          *keyPath,
		SyncInterval:     *syncInterval,
		SyncTimeout:      *syncTimeout,
		ListenAddr:       *listenAddr,
		ScrapeTimeout:    *scrapeTimeout,
		MaxScrapeWorkers: *maxScrapeWorkers,
		CacheTTL:         *cacheTTL,
		OVSBridge:        *ovsBridge,
		OVSVsctlPath:     *ovsVsctlPath,
		LogFormat:        *logFormat,
		LogLevel:         *logLevel,
	}

	if *cfgFile != "" {
		if err := loadFromFile(*cfgFile, cfg); err != nil {
			return nil, fmt.Errorf("loading config file %s: %w", *cfgFile, err)
		}
	}

	return cfg, nil
}

func loadFromFile(path string, target *AgentConfig) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Intermediate representation allowing string durations like "60s"
	var raw struct {
		ControllerURL    string         `json:"controller_url"`
		HostName         string         `json:"hostname"`
		CACertPath       string         `json:"ca_cert_path"`
		CertPath         string         `json:"cert_path"`
		KeyPath          string         `json:"key_path"`
		SyncInterval     interface{}    `json:"sync_interval"`
		SyncTimeout      interface{}    `json:"sync_timeout"`
		ListenAddr       string         `json:"listen_addr"`
		ScrapeTimeout    interface{}    `json:"scrape_timeout"`
		MaxScrapeWorkers int            `json:"max_scrape_workers"`
		CacheTTL         interface{}    `json:"cache_ttl"`
		OVSBridge        string         `json:"ovs_bridge"`
		OVSVsctlPath     string         `json:"ovs_vsctl_path"`
		LogFormat        string         `json:"log_format"`
		LogLevel         string         `json:"log_level"`
		StaticTargets    []api.TargetVM `json:"static_targets"`
	}

	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if raw.ControllerURL != "" {
		target.ControllerURL = raw.ControllerURL
	}
	if raw.HostName != "" {
		target.HostName = raw.HostName
	}
	if raw.CACertPath != "" {
		target.CACertPath = raw.CACertPath
	}
	if raw.CertPath != "" {
		target.CertPath = raw.CertPath
	}
	if raw.KeyPath != "" {
		target.KeyPath = raw.KeyPath
	}
	if raw.ListenAddr != "" {
		target.ListenAddr = raw.ListenAddr
	}
	if raw.OVSBridge != "" {
		target.OVSBridge = raw.OVSBridge
	}
	if raw.OVSVsctlPath != "" {
		target.OVSVsctlPath = raw.OVSVsctlPath
	}
	if len(raw.StaticTargets) > 0 {
		target.StaticTargets = raw.StaticTargets
	}
	if raw.MaxScrapeWorkers > 0 {
		target.MaxScrapeWorkers = raw.MaxScrapeWorkers
	}
	if raw.LogFormat != "" {
		target.LogFormat = raw.LogFormat
	}
	if raw.LogLevel != "" {
		target.LogLevel = raw.LogLevel
	}

	if d, err := parseDurationValue(raw.SyncInterval); err == nil && d > 0 {
		target.SyncInterval = d
	}
	if d, err := parseDurationValue(raw.SyncTimeout); err == nil && d > 0 {
		target.SyncTimeout = d
	}
	if d, err := parseDurationValue(raw.ScrapeTimeout); err == nil && d > 0 {
		target.ScrapeTimeout = d
	}
	if d, err := parseDurationValue(raw.CacheTTL); err == nil && d > 0 {
		target.CacheTTL = d
	}

	return nil
}

func parseDurationValue(v interface{}) (time.Duration, error) {
	if v == nil {
		return 0, fmt.Errorf("nil value")
	}
	switch val := v.(type) {
	case string:
		return time.ParseDuration(val)
	case float64:
		return time.Duration(val), nil
	default:
		return 0, fmt.Errorf("invalid duration format")
	}
}

func getEnv(key, def string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return def
}

func getEnvInt(key string, def int) int {
	if val := os.Getenv(key); val != "" {
		var n int
		if _, err := fmt.Sscanf(val, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return def
}

func defaultHostName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "compute-node"
	}
	return h
}
