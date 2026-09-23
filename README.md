# DCGM Exporter Collector Stack

A compute-initiated GPU telemetry collection and synchronization stack for OpenStack environments deployed with Kolla-Ansible.

`dcgm-exporter-collector` collects NVIDIA GPU metrics from passthrough virtual machines without requiring NVIDIA GPU drivers, NVML, or DCGM libraries on the physical compute host. Telemetry is collected directly from guest-level `dcgm-exporter` endpoints over an isolated local network datapath attached to Open vSwitch (`br-int`), enriched with authoritative OpenStack metadata, and exposed as a single Prometheus `/metrics` endpoint per compute node.

---

## Architecture Overview

```text
                           CONTROL NODE
                  +----------------------------+
                  |    dcgm-control-service    |
                  |                            |
                  | - Holds OpenStack Admin    |
                  | - Nova VM & Flavor Check   |
                  | - Neutron Host Port Bind   |
                  +-------------^--------------+
                                |
                         HTTPS + mTLS
                     (Compute Client Cert)
                                |
                  +-------------+--------------+
                  |         COMPUTE NODE       |
                  |                            |
                  |     dcgm-compute-agent     |
                  |  +-----------------------+ |
                  |  | Coordinator (mTLS)    | |
                  |  | Network Provisioner   | |
                  |  | Telemetry Scraper     | |
                  |  +-----------+-----------+ |
                  |              |             |
                  |     veth pair via ovs-vsctl|
                  |              v             |
                  |            br-int          |
                  |              |             |
                  |        Local IPv4          |
                  |              v             |
                  |     guest dcgm-exporter    |
                  |       (:9400/metrics)      |
                  +--------------+-------------+
                                 |
                         GET :9405/metrics
                                 v
                     Prometheus Scraper
```

### Separation of Responsibilities

1. **Control Node (`dcgm-control-service`)**:
   - The **only** component holding OpenStack API credentials.
   - Enforces mutually authenticated TLS (mTLS) and verifies the compute node hostname from the client certificate Subject Common Name (`CN`).
   - Queries Nova for active VMs on the requesting compute host and inspects flavor extra specs (`pci_passthrough:alias`, `family: gpu`) to discover GPU passthrough instances.
   - Provisions one persistent host-side Neutron port per `(compute_host, network_id)` pair bound to the compute host (`binding:host_id`).
   - Returns desired network endpoints and VM scrape targets to the compute agent.

2. **Compute Node (`dcgm-compute-agent`)**:
   - Single unified binary executed as a single command.
   - Holds **zero OpenStack credentials** and **zero host GPU device access**.
   - Idempotently creates persistent veth pairs and calls the host `ovs-vsctl` client to attach interfaces to `br-int` with `external_ids:iface-id` for OVN port binding.
   - Concurrently scrapes guest `dcgm-exporter` endpoints over local IPv4 with strict per-target timeouts and fault isolation.
   - Preserves all default `dcgm-exporter` labels while enriching metrics with authoritative hypervisor dimensions (`host`, `vm_id`, `vm_name`, `project_id`).
   - Injects operational health metrics (`dcgm_collector_scrape_success`, scrape latency, target counts) into `/metrics`.

---

## Key Features

- **Zero Host GPU Overhead**: The compute host does not run NVIDIA drivers or DCGM. All GPU hardware communication happens inside the guest VM.
- **Compute-Initiated mTLS**: Compute nodes initiate synchronization with the control node. The control node identifies hosts via client certificate `CN`, preventing spoofing.
- **Direct `ovs-vsctl` CLI Integration**: Reconciles `br-int` ports directly using host `ovs-vsctl` commands without requiring raw OVSDB socket mounts.
- **Flexible Flavor Detection**: Discovers GPU workloads if flavor extra specs define `family: gpu` or `pci_passthrough:alias` with any GPU model name (e.g. `RTX4090:1`, `A100:1`, `H100:1`).
- **Fault Isolation**: A slow, restarting, or failing guest VM exporter does not block or degrade scrapes for other VMs on the compute host.
- **Full Metric Compatibility**: Preserves all standard `dcgm-exporter` labels (`DCGM_FI_DRIVER_VERSION`, `UUID`, `hostname`, `modelName`, `pci_bus_id`, `device`, `gpu`).

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

### 2. Compute Node (`dcgm-compute-agent`)

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

### Step 2: Deploy Compute Agent on Compute Nodes

1. Install binary and client certificates:
   ```bash
   cp bin/dcgm-compute-agent /usr/local/bin/
   mkdir -p /etc/dcgm-compute-agent/certs
   # Place ca.crt, client.crt (CN=<compute_hostname>), client.key in /etc/dcgm-compute-agent/certs/
   ```

2. Create systemd unit `/etc/systemd/system/dcgm-compute-agent.service`:
   ```ini
   [Unit]
   Description=DCGM Exporter Compute Collector Agent
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
| `/metrics` | `GET` | Aggregated, enriched Prometheus metrics across all GPU passthrough VMs |
| `/healthz` | `GET` | Process liveness check (`200 OK`) |
| `/readyz` | `GET` | Process readiness check (`200 READY`) |
| `/status` | `GET` | JSON report of collector state, total/healthy targets, and per-target scrape latency |

---

## Detailed Documentation

- [Compute Node Agent Deployment Guide](docs/compute_node_agent_guide.md)
- [Control Node Service Deployment Guide](docs/control_node_service_guide.md)
