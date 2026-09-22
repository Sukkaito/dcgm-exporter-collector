package config

import (
	"os"
	"testing"
	"time"
)

func TestConfig_LoadFlags(t *testing.T) {
	args := []string{
		"-controller-url", "https://ctrl:8443",
		"-hostname", "hgx087",
		"-listen-addr", ":9999",
		"-sync-interval", "30s",
		"-scrape-timeout", "2s",
		"-max-scrape-workers", "16",
		"-log-format", "json",
		"-log-level", "debug",
	}

	cfg, err := Load(args)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.ControllerURL != "https://ctrl:8443" {
		t.Errorf("expected https://ctrl:8443, got %s", cfg.ControllerURL)
	}
	if cfg.HostName != "hgx087" {
		t.Errorf("expected hgx087, got %s", cfg.HostName)
	}
	if cfg.ListenAddr != ":9999" {
		t.Errorf("expected :9999, got %s", cfg.ListenAddr)
	}
	if cfg.SyncInterval != 30*time.Second {
		t.Errorf("expected 30s, got %v", cfg.SyncInterval)
	}
	if cfg.ScrapeTimeout != 2*time.Second {
		t.Errorf("expected 2s, got %v", cfg.ScrapeTimeout)
	}
	if cfg.MaxScrapeWorkers != 16 {
		t.Errorf("expected 16 workers, got %d", cfg.MaxScrapeWorkers)
	}
	if cfg.LogFormat != "json" {
		t.Errorf("expected json log format, got %s", cfg.LogFormat)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("expected debug log level, got %s", cfg.LogLevel)
	}
}

func TestConfig_LoadFile(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "agent-config-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := `{"controller_url": "https://file-ctrl:8443", "hostname": "file-host", "sync_interval": "45s", "scrape_timeout": "4s"}`
	_, _ = tmpFile.WriteString(content)
	_ = tmpFile.Close()

	cfg, err := Load([]string{"-config", tmpFile.Name()})
	if err != nil {
		t.Fatalf("failed to load config from file: %v", err)
	}

	if cfg.ControllerURL != "https://file-ctrl:8443" {
		t.Errorf("expected https://file-ctrl:8443, got %s", cfg.ControllerURL)
	}
	if cfg.HostName != "file-host" {
		t.Errorf("expected file-host, got %s", cfg.HostName)
	}
	if cfg.SyncInterval != 45*time.Second {
		t.Errorf("expected 45s, got %v", cfg.SyncInterval)
	}
	if cfg.ScrapeTimeout != 4*time.Second {
		t.Errorf("expected 4s, got %v", cfg.ScrapeTimeout)
	}
}
