# DCGM Exporter Collector Stack

A compute-initiated GPU telemetry collection and synchronization stack for OpenStack environments deployed with Kolla-Ansible.

`dcgm-exporter-collector` collects GPU metrics from **Target VMs** (OpenStack Nova servers with PCI passthrough GPUs, distinguished from local hypervisor VMs) without requiring GPU drivers, NVML, or DCGM libraries on the physical **Compute Host**. Telemetry is scraped directly from **Guest Exporters** over isolated **Tenant Network Namespaces** attached to Open vSwitch (`br-int`) via **OVS Peer Interfaces**, enriched with authoritative **Hypervisor Metadata**, and exposed as a single Prometheus `/metrics` endpoint per Compute Host alongside operational **Collector Metrics**.


---

## Architecture Overview

```text
                           CONTROL NODE
                  +----------------------------+
                  |       Control Service      |
                  |    (dcgm-control-service)  |
                  |                            |
                  | - Holds OpenStack Admin    |
                  | - Discovers Target VMs     |
                  | - Provisions Host Endpoints|
                  +-------------^--------------+
                                |
                         HTTPS + mTLS
                          (Host Sync)
                                |
                  +-------------+--------------+
                  |         COMPUTE NODE       |
                  |                            |
                  |     dcgm-compute-agent     |
                  |       (Compute Agent)      |
                  |  +-----------------------+ |
                  |  | Host Sync Client      | |
                  |  | Network Provisioner   | |
                  |  | Telemetry Scraper     | |
                  |  +-----------+-----------+ |
                  |              |             |
                  |      OVS Peer Interface    |
                  |              v             |
                  |            br-int          |
                  |              |             |
                  |  Tenant Network Namespace  |
                  |              v             |
                  |       Guest Exporter       |
                  |       (:9400/metrics)      |
                  +--------------+-------------+
                                 |
                         GET :9405/metrics
                                 v
                     Prometheus Scraper
```

### Separation of Responsibilities

1. **Control Service (`dcgm-control-service`)**:
   - The **only** component holding OpenStack API credentials.
   - Enforces mutually authenticated TLS (mTLS) and verifies the Compute Host hostname from the client certificate Subject Common Name (`CN`).
   - Queries Nova for active servers on the requesting Compute Host and inspects flavor extra specs (`pci_passthrough:alias`, `family: gpu`) to discover Target VMs (Nova servers with assigned GPUs). Virtual machines created directly on the hypervisor outside Nova are not Target VMs and are never discovered.
   - Provisions one persistent Host Network Endpoint (Neutron port) per `(compute_host, network_id)` pair bound to the Compute Host (`binding:host_id`).
   - Returns desired Host Network Endpoints and Target VMs to the Compute Agent during Host Sync.

2. **Compute Agent (`dcgm-compute-agent`)**:
   - Single unified binary executed as a daemon on each Compute Host.
   - Holds **zero OpenStack credentials** and **zero host GPU device access**.
   - Idempotently provisions persistent veth pairs, isolates Host Network Endpoints into dedicated Tenant Network Namespaces (`dcgm-<short-id>`) to prevent route collisions across overlapping tenant subnets, and attaches OVS Peer Interfaces to `br-int` with `external_ids:iface-id` for OVN port binding.
   - Concurrently scrapes Guest Exporters over local IPv4 directly within each target's Tenant Network Namespace with strict per-target timeouts, per-netns connection pooling, and fault isolation.
   - Preserves all default Guest Exporter metric labels while applying Metric Enrichment with authoritative Hypervisor Metadata (`host`, `vm_id`, `vm_name`, `project_id`).
   - Injects operational Collector Metrics (`dcgm_collector_scrape_success`, scrape latency, target counts) into `/metrics`.

---

## Key Features

- **Zero Host GPU Overhead**: The Compute Host does not run NVIDIA drivers or DCGM. All GPU hardware communication happens inside the guest VM.
- **Compute-Initiated mTLS Host Sync**: Compute Hosts initiate Host Sync with the Control Service. The Control Service identifies hosts via client certificate `CN`, preventing spoofing.
- **Isolated Tenant Network Namespaces**: Each tenant network host interface is isolated inside `dcgm-<short-id>` on the Compute Host. Overlapping tenant CIDRs never collide in the host root routing table.
- **Direct `ovs-vsctl` CLI Integration**: Reconciles OVS Peer Interfaces on `br-int` directly using host `ovs-vsctl` commands without requiring raw OVSDB socket mounts.
- **Flexible Flavor Detection**: Discovers Target VMs if flavor extra specs define `family: gpu` or `pci_passthrough:alias` with any GPU model name (e.g. `RTX4090:1`, `A100:1`, `H100:1`).
- **Fault Isolation**: A slow, restarting, or failing Guest Exporter does not block or degrade scrapes for other Target VMs on the Compute Host.
- **Full Metric Compatibility**: Preserves all standard `dcgm-exporter` labels (`DCGM_FI_DRIVER_VERSION`, `UUID`, `hostname`, `modelName`, `pci_bus_id`, `device`, `gpu`) alongside injected Hypervisor Metadata.



---

## Repository Structure

```text
dcgm-exporter-collector/
├── cmd/
│   ├── dcgm-compute-agent/        # Compute-node collector daemon entry point
│   └── dcgm-control-service/      # Control-node sync service entry point
├── pkg/
│   ├── api/                       # Shared API models (SyncRequest, SyncResponse, TargetVM)
│   ├── coordinator/               # In-memory target registry & mTLS sync client
│   ├── controller/                # Control node mTLS HTTPS server and sync handlers
│   ├── network/                   # Veth pair manager and ovs-vsctl CLI wrapper
│   ├── openstack/                 # Nova and Neutron Gophercloud client & mock
│   ├── processor/                 # Prometheus parser, label enricher & serializer
│   ├── scraper/                   # Concurrent HTTP target scraper with fault isolation
│   ├── server/                    # Telemetry HTTP server (/metrics, /healthz, /readyz, /status)
│   └── transport/                 # TelemetryTransport interface (IPv4 HTTP implementation)
├── internal/
│   ├── config/                    # Compute agent configuration loader
│   └── controlconfig/             # Control service configuration loader
├── docs/
│   ├── compute_node_agent_guide.md  # Compute agent deployment & operations guide
│   └── control_node_service_guide.md # Control service deployment & operations guide
├── config.sample.json             # Sample configuration for compute agent
├── config.control.sample.json     # Sample configuration for control service
├── Dockerfile                     # Multi-stage Dockerfile (defaults to compute-agent)
├── Dockerfile.compute-agent       # Dedicated Dockerfile for dcgm-compute-agent
├── Dockerfile.control-service     # Dedicated Dockerfile for dcgm-control-service
├── docker-compose.sample.yml      # Sample Docker Compose stack
└── Makefile                       # Build, test, and container targets
```

---

## Building from Source

### Requirements
- Go 1.22+ (tested with Go 1.26)
- Linux amd64

### Build Commands
```bash
# Build both binaries
go build -ldflags "-s -w" -o bin/dcgm-compute-agent ./cmd/dcgm-compute-agent
go build -ldflags "-s -w" -o bin/dcgm-control-service ./cmd/dcgm-control-service

# Or using Makefile
make build
```

### Docker Container Build
```bash
# Build both container images via Makefile
make docker

# Or build individual images
docker build -t dcgm-compute-agent:latest -f Dockerfile.compute-agent .
docker build -t dcgm-control-service:latest -f Dockerfile.control-service .

# Or using the root multi-stage Dockerfile
docker build --target compute-agent -t dcgm-compute-agent:latest .
docker build --target control-service -t dcgm-control-service:latest .
```

### Running Tests
```bash
go test -v -race ./...
```

---

## Configuration

### 1. Control Node (`dcgm-control-service`)

Example [`config.control.sample.json`](file:///run/media/sukkaito/Data/Code/golang/dcgm-exporter-collector/config.control.sample.json):
```json
{
  "listen_addr": ":8443",
  "tls_cert_path": "/etc/dcgm-control-service/certs/server.crt",
  "tls_key_path": "/etc/dcgm-control-service/certs/server.key",
  "client_ca_cert_path": "/etc/dcgm-control-service/certs/ca.crt",
  "auth_url": "https://keystone.internal:5000/v3",
  "username": "admin",
  "password": "your-openstack-admin-password",
  "project_name": "admin",
  "user_domain_name": "Default",
  "project_domain_name": "Default",
  "region": "RegionOne",
  "extra_specs_key": "pci_passthrough:alias",
  "extra_specs_keyword": "",
  "use_mock": false
}
```

### 2. Compute Host (`dcgm-compute-agent`)

Example [`config.sample.json`](file:///run/media/sukkaito/Data/Code/golang/dcgm-exporter-collector/config.sample.json):

```json
{
  "controller_url": "https://control-node.internal:8443",
  "hostname": "hgx087.compute.internal",
  "ca_cert_path": "/etc/dcgm-compute-agent/certs/ca.crt",
  "cert_path": "/etc/dcgm-compute-agent/certs/client.crt",
  "key_path": "/etc/dcgm-compute-agent/certs/client.key",
  "sync_interval": "60s",
  "sync_timeout": "10s",
  "listen_addr": ":9405",
  "scrape_timeout": "3s",
  "cache_ttl": "5s",
  "ovs_bridge": "br-int",
  "ovs_vsctl_path": "ovs-vsctl"
}
```

---

## Deployment Quickstart

### Step 1: Deploy Control Service on Control Node

1. Install binary and certificates:
   ```bash
   cp bin/dcgm-control-service /usr/local/bin/
   mkdir -p /etc/dcgm-control-service/certs
   # Place ca.crt, server.crt, server.key in /etc/dcgm-control-service/certs/
   ```

2. Create systemd unit `/etc/systemd/system/dcgm-control-service.service`:
   ```ini
   [Unit]
   Description=DCGM Exporter Control Service
   After=network.target

   [Service]
   Type=simple
   ExecStart=/usr/local/bin/dcgm-control-service -config /etc/dcgm-control-service/control.json
   Restart=always
   RestartSec=5s

   [Install]
   WantedBy=multi-user.target
   ```

3. Start service:
   ```bash
   systemctl daemon-reload
   systemctl enable --now dcgm-control-service
   ```

Refer to [Control Node Service Guide](file:///run/media/sukkaito/Data/Code/golang/dcgm-exporter-collector/docs/control_node_service_guide.md) for certificate creation and Kolla integration.

---

### Step 2: Deploy Compute Agent on Compute Hosts

1. Install binary and client certificates:

   ```bash
   cp bin/dcgm-compute-agent /usr/local/bin/
   mkdir -p /etc/dcgm-compute-agent/certs
   # Place ca.crt, client.crt (CN=<compute_hostname>), client.key in /etc/dcgm-compute-agent/certs/
   ```

2. Create systemd unit `/etc/systemd/system/dcgm-compute-agent.service`:
   ```ini
   [Unit]
   Description=DCGM Exporter Compute Agent
   After=network.target openvswitch-switch.service
   Wants=network.target

   [Service]
   Type=simple
   ExecStart=/usr/local/bin/dcgm-compute-agent -config /etc/dcgm-compute-agent/agent.json
   Restart=always
   RestartSec=5s
   AmbientCapabilities=CAP_NET_ADMIN
   CapabilityBoundingSet=CAP_NET_ADMIN

   [Install]
   WantedBy=multi-user.target
   ```

3. Start agent:
   ```bash
   systemctl daemon-reload
   systemctl enable --now dcgm-compute-agent
   ```

Refer to [Compute Node Agent Guide](file:///run/media/sukkaito/Data/Code/golang/dcgm-exporter-collector/docs/compute_node_agent_guide.md) for containerized Kolla deployment options.

---

## Observability & Prometheus Scrape Format

### HTTP Endpoints

| Endpoint | Method | Description |
|---|---|---|
| `/metrics` | `GET` | Aggregated Prometheus metrics across all Target VMs enriched with Hypervisor Metadata, plus Collector Metrics |
| `/healthz` | `GET` | Process liveness check (`200 OK`) |
| `/readyz` | `GET` | Process readiness check (`200 READY`) |
| `/status` | `GET` | JSON report of collector state, total/healthy Scrape Targets, and per-target scrape latency |


---

## Detailed Documentation

- [Compute Node Agent Deployment Guide](docs/compute_node_agent_guide.md)
- [Control Node Service Deployment Guide](docs/control_node_service_guide.md)
