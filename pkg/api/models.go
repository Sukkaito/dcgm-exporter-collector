package api

import (
	"time"
)

// SyncRequest represents the payload sent by the compute node to the control node.
type SyncRequest struct {
	Action   string `json:"action"`
	Hostname string `json:"hostname,omitempty"`
}

// HostNetworkEndpoint represents the host-side network interface bound to a Neutron network.
type HostNetworkEndpoint struct {
	NetworkID string `json:"network_id"`
	PortID    string `json:"port_id"`
	MAC       string `json:"mac"`
	IP        string `json:"ip"` // e.g. "10.0.0.254/24"
	VethName  string `json:"veth_name"`
}

// TargetVM represents a GPU-passthrough virtual machine to be scraped.
type TargetVM struct {
	VMID      string   `json:"vm_id"`
	VMName    string   `json:"vm_name"`
	ProjectID string   `json:"project_id,omitempty"`
	GuestIP   string   `json:"guest_ip"`
	Port      int      `json:"port"`
	GPUIDs    []string `json:"gpu_ids,omitempty"`
}

// SyncResponse is returned by the control-node sync API to the compute agent.
type SyncResponse struct {
	Status    string                `json:"status"`
	Endpoints []HostNetworkEndpoint `json:"endpoints"`
	Targets   []TargetVM            `json:"targets"`
}

// ExporterStatus represents the health and scrape status of an individual VM guest exporter.
type ExporterStatus struct {
	VMID             string    `json:"vm_id"`
	VMName           string    `json:"vm_name"`
	Endpoint         string    `json:"endpoint"`
	Healthy          bool      `json:"healthy"`
	LastSuccess      time.Time `json:"last_success,omitempty"`
	LastError        string    `json:"last_error,omitempty"`
	ScrapeDurationMs int64     `json:"scrape_duration_ms"`
}

// CollectorStatus represents the operational status of the collector and all targets.
type CollectorStatus struct {
	CollectorStatus string           `json:"collector"`
	Timestamp       time.Time        `json:"timestamp"`
	TotalTargets    int              `json:"total_targets"`
	HealthyTargets  int              `json:"healthy_targets"`
	Exporters       []ExporterStatus `json:"exporters"`
}
