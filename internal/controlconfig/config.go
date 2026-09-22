package controlconfig

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/openstack"
	"github.com/gophercloud/gophercloud/v2"
)

// Config represents the control node service configuration.
type Config struct {
	ListenAddr       string `json:"listen_addr"`
	TLSCertPath      string `json:"tls_cert_path"`
	TLSKeyPath       string `json:"tls_key_path"`
	ClientCACertPath string `json:"client_ca_cert_path"`

	// OpenStack credentials
	AuthURL           string `json:"auth_url"`
	Username          string `json:"username"`
	Password          string `json:"password"`
	ProjectName       string `json:"project_name"`
	UserDomainName    string `json:"user_domain_name"`
	ProjectDomainName string `json:"project_domain_name"`
	Region            string `json:"region"`

	// GPU filter rules
	ExtraSpecsKey     string `json:"extra_specs_key"`
	ExtraSpecsKeyword string `json:"extra_specs_keyword"`

	// Mock mode for local testing
	UseMock bool `json:"use_mock"`

	// Logging
	LogFormat string `json:"log_format"`
	LogLevel  string `json:"log_level"`
}

// Load loads configuration from flags, environment variables, and optional JSON file.
func Load(args []string) (*Config, error) {
	fs := flag.NewFlagSet("dcgm-control-service", flag.ContinueOnError)

	cfgFile := fs.String("config", getEnv("DCGM_CONTROL_CONFIG_FILE", ""), "Path to JSON configuration file")
	listenAddr := fs.String("listen-addr", getEnv("DCGM_CONTROL_LISTEN_ADDR", ":8443"), "HTTPS listen address")
	tlsCert := fs.String("tls-cert", getEnv("DCGM_CONTROL_TLS_CERT", ""), "Path to server certificate")
	tlsKey := fs.String("tls-key", getEnv("DCGM_CONTROL_TLS_KEY", ""), "Path to server private key")
	clientCA := fs.String("client-ca-cert", getEnv("DCGM_CONTROL_CLIENT_CA_CERT", ""), "Path to CA certificate for verifying compute clients")

	authURL := fs.String("os-auth-url", getEnv("OS_AUTH_URL", ""), "OpenStack Keystone Auth URL")
	username := fs.String("os-username", getEnv("OS_USERNAME", ""), "OpenStack Keystone username")
	password := fs.String("os-password", getEnv("OS_PASSWORD", ""), "OpenStack Keystone password")
	projectName := fs.String("os-project-name", getEnv("OS_PROJECT_NAME", ""), "OpenStack Keystone project name")
	userDomain := fs.String("os-user-domain-name", getEnv("OS_USER_DOMAIN_NAME", "Default"), "OpenStack Keystone user domain")
	projectDomain := fs.String("os-project-domain-name", getEnv("OS_PROJECT_DOMAIN_NAME", "Default"), "OpenStack Keystone project domain")
	region := fs.String("os-region-name", getEnv("OS_REGION_NAME", ""), "OpenStack region name")

	extraSpecsKey := fs.String("extra-specs-key", getEnv("DCGM_EXTRA_SPECS_KEY", "pci_passthrough:alias"), "Flavor extra specs key for GPU check")
	extraSpecsKeyword := fs.String("extra-specs-keyword", getEnv("DCGM_EXTRA_SPECS_KEYWORD", ""), "Flavor extra specs keyword (empty matches any GPU alias)")

	useMock := fs.Bool("use-mock", getEnvBool("DCGM_USE_MOCK_OPENSTACK", false), "Enable mock OpenStack client for local testing")
	logFormat := fs.String("log-format", getEnv("DCGM_LOG_FORMAT", "text"), "Log format (text or json)")
	logLevel := fs.String("log-level", getEnv("DCGM_LOG_LEVEL", "info"), "Log level (debug, info, warn, error)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg := &Config{
		ListenAddr:        *listenAddr,
		TLSCertPath:       *tlsCert,
		TLSKeyPath:        *tlsKey,
		ClientCACertPath:  *clientCA,
		AuthURL:           *authURL,
		Username:          *username,
		Password:          *password,
		ProjectName:       *projectName,
		UserDomainName:    *userDomain,
		ProjectDomainName: *projectDomain,
		Region:            *region,
		ExtraSpecsKey:     *extraSpecsKey,
		ExtraSpecsKeyword: *extraSpecsKeyword,
		UseMock:           *useMock,
		LogFormat:         *logFormat,
		LogLevel:          *logLevel,
	}

	if *cfgFile != "" {
		if err := loadFromFile(*cfgFile, cfg); err != nil {
			return nil, fmt.Errorf("reading config file %s: %w", *cfgFile, err)
		}
	}

	return cfg, nil
}

func loadFromFile(path string, target *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// ToAuthOptions converts Config to Gophercloud AuthOptions.
func (c *Config) ToAuthOptions() gophercloud.AuthOptions {
	return gophercloud.AuthOptions{
		IdentityEndpoint: c.AuthURL,
		Username:         c.Username,
		Password:         c.Password,
		TenantName:       c.ProjectName,
		DomainName:       c.UserDomainName,
	}
}

// ToVMFilterConfig converts Config to openstack.VMFilterConfig.
func (c *Config) ToVMFilterConfig() openstack.VMFilterConfig {
	return openstack.VMFilterConfig{
		ExtraSpecsKey:     c.ExtraSpecsKey,
		ExtraSpecsKeyword: c.ExtraSpecsKeyword,
	}
}

func getEnv(key, def string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return def
}

func getEnvBool(key string, def bool) bool {
	if val := os.Getenv(key); val != "" {
		return val == "1" || val == "true" || val == "TRUE"
	}
	return def
}
