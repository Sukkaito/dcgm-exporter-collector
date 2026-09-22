package openstack

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/Sukkaito/dcgm-exporter-collector/pkg/api"
	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/flavors"
	"github.com/gophercloud/gophercloud/v2/openstack/compute/v2/servers"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/portsbinding"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/subnets"
)

// GophercloudClient implements OpenStackClient using official Gophercloud SDK.
type GophercloudClient struct {
	computeClient *gophercloud.ServiceClient
	networkClient *gophercloud.ServiceClient
	filter        VMFilterConfig

	mu          sync.RWMutex
	flavorCache map[string]map[string]string // flavorID -> extraSpecs
	subnetCache map[string]string            // subnetID -> prefix length (e.g. "24")
}

// NewGophercloudClient creates and authenticates an OpenStack client.
func NewGophercloudClient(ctx context.Context, authOpts gophercloud.AuthOptions, endpointOpts gophercloud.EndpointOpts, filter VMFilterConfig) (*GophercloudClient, error) {
	provider, err := openstack.AuthenticatedClient(ctx, authOpts)
	if err != nil {
		return nil, fmt.Errorf("keystone authentication failed: %w", err)
	}

	// Automatically renew Keystone token on expiration (401 response)
	provider.ReauthFunc = func(ctx context.Context) error {
		return openstack.Authenticate(ctx, provider, authOpts)
	}

	computeClient, err := openstack.NewComputeV2(provider, endpointOpts)
	if err != nil {
		return nil, fmt.Errorf("initializing Nova client: %w", err)
	}

	networkClient, err := openstack.NewNetworkV2(provider, endpointOpts)
	if err != nil {
		return nil, fmt.Errorf("initializing Neutron client: %w", err)
	}

	return &GophercloudClient{
		computeClient: computeClient,
		networkClient: networkClient,
		filter:        filter,
		flavorCache:   make(map[string]map[string]string),
		subnetCache:   make(map[string]string),
	}, nil
}

// HealthCheck verifies connectivity to Keystone and Nova.
func (c *GophercloudClient) HealthCheck(ctx context.Context) error {
	pages, err := servers.List(c.computeClient, servers.ListOpts{Limit: 1}).AllPages(ctx)
	if err != nil {
		return fmt.Errorf("nova health check failed: %w", err)
	}
	isEmpty, err := pages.IsEmpty()
	if err != nil {
		return fmt.Errorf("checking nova response: %w", err)
	}
	_ = isEmpty
	return nil
}

// DiscoverComputeVMs discovers all active GPU passthrough VMs on the given compute host.
func (c *GophercloudClient) DiscoverComputeVMs(ctx context.Context, computeHost string) ([]api.TargetVM, error) {
	listOpts := servers.ListOpts{
		Host:   computeHost,
		Status: "ACTIVE",
	}

	allPages, err := servers.List(c.computeClient, listOpts).AllPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing servers on %s: %w", computeHost, err)
	}

	allServers, err := servers.ExtractServers(allPages)
	if err != nil {
		return nil, fmt.Errorf("extracting servers: %w", err)
	}

	var targets []api.TargetVM

	for _, s := range allServers {
		// 1. Check if VM matches GPU passthrough criteria
		hasGPU, err := c.serverHasGPUPassthrough(ctx, s)
		if err != nil {
			slog.Warn("Checking GPU passthrough failed", "server_id", s.ID, "error", err)
			continue
		}
		if !hasGPU {
			continue
		}

		// 2. Query VM's ports to find fixed IPv4 address
		portListOpts := ports.ListOpts{
			DeviceID: s.ID,
		}
		portPages, err := ports.List(c.networkClient, portListOpts).AllPages(ctx)
		if err != nil {
			slog.Warn("Listing ports failed", "server_id", s.ID, "error", err)
			continue
		}
		serverPorts, err := ports.ExtractPorts(portPages)
		if err != nil {
			slog.Warn("Extracting ports failed", "server_id", s.ID, "error", err)
			continue
		}

		guestIP := ""
		for _, p := range serverPorts {
			for _, fip := range p.FixedIPs {
				parsed := net.ParseIP(fip.IPAddress)
				if parsed != nil && parsed.To4() != nil {
					guestIP = fip.IPAddress
					break
				}
			}
			if guestIP != "" {
				break
			}
		}

		if guestIP == "" {
			slog.Debug("Server has no IPv4 address, skipping", "server_id", s.ID, "server_name", s.Name)
			continue
		}

		targets = append(targets, api.TargetVM{
			VMID:      s.ID,
			VMName:    s.Name,
			ProjectID: s.TenantID,
			GuestIP:   guestIP,
			Port:      9400,
		})
	}

	return targets, nil
}

// serverHasGPUPassthrough inspects flavor extra specs of the VM to verify GPU passthrough.
func (c *GophercloudClient) serverHasGPUPassthrough(ctx context.Context, s servers.Server) (bool, error) {
	flavorID, ok := s.Flavor["id"].(string)
	if !ok || flavorID == "" {
		return false, nil
	}

	extraSpecs, err := c.getFlavorExtraSpecs(ctx, flavorID)
	if err != nil {
		return false, err
	}

	return c.matchesExtraSpecs(extraSpecs), nil
}

func (c *GophercloudClient) getFlavorExtraSpecs(ctx context.Context, flavorID string) (map[string]string, error) {
	c.mu.RLock()
	cached, ok := c.flavorCache[flavorID]
	c.mu.RUnlock()
	if ok {
		return cached, nil
	}

	specs, err := flavors.ListExtraSpecs(ctx, c.computeClient, flavorID).Extract()
	if err != nil {
		return nil, fmt.Errorf("getting flavor %s extra specs: %w", flavorID, err)
	}

	c.mu.Lock()
	c.flavorCache[flavorID] = specs
	c.mu.Unlock()

	return specs, nil
}

func (c *GophercloudClient) matchesExtraSpecs(specs map[string]string) bool {
	// 1. Match if family is explicitly set to gpu
	if family, exists := specs["family"]; exists && strings.EqualFold(strings.TrimSpace(family), "gpu") {
		return true
	}

	// 2. Check configured ExtraSpecsKey (default pci_passthrough:alias)
	key := c.filter.ExtraSpecsKey
	if key == "" {
		key = "pci_passthrough:alias"
	}

	val, exists := specs[key]
	if !exists || strings.TrimSpace(val) == "" {
		return false
	}

	// If no keyword is configured, presence of the extra spec is sufficient
	if c.filter.ExtraSpecsKeyword == "" {
		return true
	}

	return strings.Contains(strings.ToLower(val), strings.ToLower(c.filter.ExtraSpecsKeyword))
}

// EnsureHostPorts guarantees that a persistent host-side Neutron port exists for each network on the compute host.
func (c *GophercloudClient) EnsureHostPorts(ctx context.Context, computeHost string, networkIDs []string) ([]api.HostNetworkEndpoint, error) {
	var endpoints []api.HostNetworkEndpoint

	for _, netID := range networkIDs {
		if netID == "" {
			continue
		}

		shortNetID := netID
		if len(shortNetID) > 8 {
			shortNetID = shortNetID[:8]
		}
		portName := fmt.Sprintf("dcgm-%s-%s", computeHost, shortNetID)
		vethName := fmt.Sprintf("host-net-%s", shortNetID)

		// 1. Search for existing persistent port
		existingPort, err := c.findPortByNameAndHost(ctx, portName, computeHost)
		if err != nil {
			return nil, fmt.Errorf("searching port %s: %w", portName, err)
		}

		var port *ports.Port
		if existingPort != nil {
			port = existingPort
		} else {
			// 2. Create new persistent host port bound to compute host
			adminUp := true
			baseOpts := ports.CreateOpts{
				NetworkID:    netID,
				Name:         portName,
				AdminStateUp: &adminUp,
				DeviceOwner:  "compute:host-telemetry",
			}
			bindOpts := portsbinding.CreateOptsExt{
				CreateOptsBuilder: baseOpts,
				HostID:            computeHost,
			}

			created, err := ports.Create(ctx, c.networkClient, bindOpts).Extract()
			if err != nil {
				return nil, fmt.Errorf("creating host port %s on network %s: %w", portName, netID, err)
			}
			port = created
			slog.Info("Created host port", "port_name", portName, "port_id", port.ID, "network_id", netID, "host", computeHost)
		}

		// 3. Format CIDR IP address
		ipWithCIDR := c.formatPortIPWithCIDR(ctx, port)

		endpoints = append(endpoints, api.HostNetworkEndpoint{
			NetworkID: netID,
			PortID:    port.ID,
			MAC:       port.MACAddress,
			IP:        ipWithCIDR,
			VethName:  vethName,
		})
	}

	return endpoints, nil
}

func (c *GophercloudClient) findPortByNameAndHost(ctx context.Context, portName, computeHost string) (*ports.Port, error) {
	pages, err := ports.List(c.networkClient, ports.ListOpts{Name: portName}).AllPages(ctx)
	if err != nil {
		return nil, err
	}
	allPorts, err := ports.ExtractPorts(pages)
	if err != nil {
		return nil, err
	}

	for _, p := range allPorts {
		if p.Name == portName {
			return &p, nil
		}
	}
	return nil, nil
}

func (c *GophercloudClient) formatPortIPWithCIDR(ctx context.Context, port *ports.Port) string {
	for _, fip := range port.FixedIPs {
		parsed := net.ParseIP(fip.IPAddress)
		if parsed == nil || parsed.To4() == nil {
			continue
		}

		prefix := c.getSubnetPrefixLength(ctx, fip.SubnetID)
		return fmt.Sprintf("%s/%s", fip.IPAddress, prefix)
	}
	return ""
}

func (c *GophercloudClient) getSubnetPrefixLength(ctx context.Context, subnetID string) string {
	if subnetID == "" {
		return "24"
	}

	c.mu.RLock()
	cached, ok := c.subnetCache[subnetID]
	c.mu.RUnlock()
	if ok {
		return cached
	}

	sub, err := subnets.Get(ctx, c.networkClient, subnetID).Extract()
	if err != nil || sub.CIDR == "" {
		return "24"
	}

	_, ipNet, err := net.ParseCIDR(sub.CIDR)
	if err != nil {
		return "24"
	}

	ones, _ := ipNet.Mask.Size()
	prefix := fmt.Sprintf("%d", ones)

	c.mu.Lock()
	c.subnetCache[subnetID] = prefix
	c.mu.Unlock()

	return prefix
}
