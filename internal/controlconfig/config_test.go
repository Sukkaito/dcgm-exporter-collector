package controlconfig

import (
	"os"
	"testing"
)

func TestConfig_LoadFlags(t *testing.T) {
	args := []string{
		"-listen-addr", ":8443",
		"-os-auth-url", "https://keystone:5000/v3",
		"-os-username", "admin",
		"-os-password", "secret",
		"-extra-specs-key", "pci_passthrough:alias",
		"-extra-specs-keyword", "gpu",
		"-use-mock",
	}

	cfg, err := Load(args)
	if err != nil {
		t.Fatalf("failed to load config: %v", err)
	}

	if cfg.ListenAddr != ":8443" {
		t.Errorf("expected :8443, got %s", cfg.ListenAddr)
	}
	if cfg.AuthURL != "https://keystone:5000/v3" {
		t.Errorf("expected https://keystone:5000/v3, got %s", cfg.AuthURL)
	}
	if !cfg.UseMock {
		t.Errorf("expected use_mock to be true")
	}

	filter := cfg.ToVMFilterConfig()
	if filter.ExtraSpecsKey != "pci_passthrough:alias" {
		t.Errorf("expected pci_passthrough:alias, got %s", filter.ExtraSpecsKey)
	}
}

func TestConfig_LoadFile(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "control-config-*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := `{"listen_addr": ":9443", "os_username": "operator"}`
	_, _ = tmpFile.WriteString(content)
	_ = tmpFile.Close()

	cfg, err := Load([]string{"-config", tmpFile.Name()})
	if err != nil {
		t.Fatalf("failed to load config from file: %v", err)
	}

	if cfg.ListenAddr != ":9443" {
		t.Errorf("expected :9443, got %s", cfg.ListenAddr)
	}
}
